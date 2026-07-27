package txndiscovery

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetFlags clears the package-level flag variables between cases.
//
// Cobra binds these once per NewMigrationTxnDiscoveryCmd call, but the variables
// themselves outlive the command, so a value set by one case leaks into the next.
// Mirrors the helper in cmd/migration/init. Cases must therefore not run in
// parallel.
func resetFlags() {
	sourceBootstrap = ""
	awsRegion = ""

	useSaslIam = false
	useSaslScram = false
	useSaslPlain = false
	useTls = false
	useUnauthenticatedTLS = false
	useUnauthenticatedPlaintext = false

	saslScramUsername = ""
	saslScramPassword = ""
	saslScramMechanism = ""
	saslPlainUsername = ""
	saslPlainPassword = ""

	tlsCaCert = ""
	tlsClientCert = ""
	tlsClientKey = ""
	insecureSkipTLSVerify = false
}

// clearFlagEnv blanks the environment variable behind every flag the command
// declares.
//
// R3 binds each flag to its uppercase, underscored name, and several of those —
// AWS_REGION above all — are routinely exported in a developer's shell. Without
// this a case asserting that a missing flag is rejected passes or fails on the
// machine it runs on. Viper treats an empty variable as unset, so blanking is
// the same as unsetting for the binder.
func clearFlagEnv(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		t.Setenv(strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_")), "")
	})
}

// newTestCmd builds a command with the package flag variables reset, the
// ambient environment blanked, and the run itself stubbed out, so a case
// depends only on the args it passes and never reaches a broker.
func newTestCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	resetFlags()
	cmd := NewMigrationTxnDiscoveryCmd()
	clearFlagEnv(t, cmd)
	cmd.SetArgs(args)
	stubRun(t)
	return cmd
}

// stubRun replaces the command's action with a recorder and returns a pointer to
// what it was handed. Flag handling is what these cases exercise; connecting to
// a cluster is the runner's own suite.
func stubRun(t *testing.T) *Opts {
	t.Helper()
	previous := runDiscovery
	var captured Opts
	runDiscovery = func(_ context.Context, o Opts) error {
		captured = o
		return nil
	}
	t.Cleanup(func() { runDiscovery = previous })
	return &captured
}

// R4, R5: the command hands the runner exactly what the flags say, including
// the root --verbose that replaced the POC's --log-level.
func TestTxnDiscovery_TheCommandRunsWithTheParsedOptions(t *testing.T) {
	resetFlags()
	root := &cobra.Command{Use: "kcp"}
	root.PersistentFlags().Bool("verbose", false, "Enable verbose logging to console")
	cmd := NewMigrationTxnDiscoveryCmd()
	root.AddCommand(cmd)
	clearFlagEnv(t, cmd)
	got := stubRun(t)

	root.SetArgs([]string{
		"txn-discovery",
		"--verbose",
		"--source-bootstrap", "b1:9092, b2:9092,",
		"--use-sasl-iam", "--aws-region", "eu-west-2",
		"--duration", "90s",
		"--interval", "15s",
		"--txn-state-topic", "__txn_state_custom",
		"--enrich-consumer-groups=false",
		"--tail-consumer-offsets=false",
		"--include-internal-topics",
		"--out", "/tmp/engagement/groups.yaml",
		"--stats-out", "/tmp/engagement/stats.json",
	})
	require.NoError(t, root.Execute())

	assert.Equal(t, []string{"b1:9092", "b2:9092"}, got.Brokers, "a trailing comma must not become an empty broker")
	assert.Equal(t, "eu-west-2", got.Region)
	assert.Equal(t, types.AuthTypeIAM, got.Auth.AuthType)
	assert.Equal(t, 90*time.Second, got.Duration)
	assert.Equal(t, 15*time.Second, got.Interval)
	assert.Equal(t, "__txn_state_custom", got.TxnStateTopic)
	assert.False(t, got.EnrichConsumerGroups)
	assert.False(t, got.TailConsumerOffsets)
	assert.True(t, got.IncludeInternalTopics)
	assert.Equal(t, "/tmp/engagement/groups.yaml", got.OutPath)
	assert.Equal(t, "/tmp/engagement/stats.json", got.StatsOutPath)
	assert.Equal(t, "/tmp/engagement/"+DefaultAuditBasename, got.AuditLogPath)
	assert.True(t, got.Verbose, "the root --verbose gates the detailed keep-up block")
	assert.NotNil(t, got.Stdout)
}

// R1/R4: --help renders the flags in named groups, as every other migration
// subcommand does. Sixty-odd ungrouped flags in alphabetical order is not a
// usable help page.
func TestTxnDiscovery_Help_RendersGroupedFlags(t *testing.T) {
	resetFlags()
	cmd := NewMigrationTxnDiscoveryCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	require.NoError(t, cmd.Usage())
	help := out.String()

	for _, group := range []string{
		"Required Flags",
		"Observation Flags",
		"Output Flags",
		"Source Cluster Authentication Flags",
		"IAM Flags",
		"SASL/SCRAM Flags",
		"SASL/PLAIN Flags",
		"TLS Flags",
	} {
		assert.Contains(t, help, group+":")
	}

	// Every declared flag appears under some group, so adding one to the
	// command without adding it to a group cannot leave it undocumented.
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		assert.Contains(t, help, "--"+f.Name, "flag is missing from the grouped usage")
	})

	assert.Contains(t, help, "environment variables", "the environment-variable path is the documented one for credentials")

	// A custom usage function replaces cobra's default template, which is what
	// would otherwise have rendered Examples. Without this the examples exist
	// only in the generated docs and an operator running --help never sees them.
	assert.Contains(t, help, "Examples:")
	assert.Contains(t, help, cmd.Example)
}

// R2: the auth flags mirror `kcp migration execute` — mutually exclusive, one required.
func TestTxnDiscovery_TwoAuthMethods_Rejected(t *testing.T) {
	cmd := newTestCmd(t, []string{
		"--source-bootstrap", "broker:9092",
		"--use-unauthenticated-plaintext",
		"--use-unauthenticated-tls",
	}...)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "were all set")
}

// R2: an auth method is not optional — a run with none would silently pick one.
func TestTxnDiscovery_NoAuthMethod_Rejected(t *testing.T) {
	cmd := newTestCmd(t, []string{"--source-bootstrap", "broker:9092"}...)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags")
}

// R2: SCRAM is unusable without credentials, so the omission is a flag error
// rather than an authentication failure discovered against the broker.
func TestTxnDiscovery_ScramWithoutCredentials_Rejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "no username and no password",
			args: []string{"--use-sasl-scram"},
			want: "sasl-scram-username",
		},
		{
			name: "username without password",
			args: []string{"--use-sasl-scram", "--sasl-scram-username", "reader"},
			want: "sasl-scram-password",
		},
		{
			name: "password without username",
			args: []string{"--use-sasl-scram", "--sasl-scram-password", "hunter2"},
			want: "sasl-scram-username",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newTestCmd(t, append([]string{"--source-bootstrap", "broker:9092"}, tc.args...)...)

			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// R6: NewKafkaClient discards the mechanism error (it is assigned to _), so an
// unsupported mechanism there silently produces a client that cannot
// authenticate. Validation therefore has to happen during flag handling.
func TestTxnDiscovery_UnsupportedScramMechanism_Rejected(t *testing.T) {
	for _, mechanism := range []string{"SHA1", "sha512", "PLAIN", ""} {
		t.Run("mechanism="+mechanism, func(t *testing.T) {
			resetFlags()
			cmd := NewMigrationTxnDiscoveryCmd()
			clearFlagEnv(t, cmd)
			reached := false
			previous := runDiscovery
			runDiscovery = func(context.Context, Opts) error { reached = true; return nil }
			t.Cleanup(func() { runDiscovery = previous })

			cmd.SetArgs([]string{
				"--source-bootstrap", "broker:9092",
				"--use-sasl-scram",
				"--sasl-scram-username", "reader",
				"--sasl-scram-password", "hunter2",
				"--sasl-scram-mechanism", mechanism,
			})

			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "--sasl-scram-mechanism")
			assert.Contains(t, err.Error(), "SHA256")
			assert.Contains(t, err.Error(), "SHA512")
			assert.False(t, reached, "rejection happens during flag validation, before any client is built")
		})
	}
}

// R6: the two mechanisms kcp supports pass validation. Without this the previous
// test is satisfied by rejecting every mechanism.
func TestTxnDiscovery_SupportedScramMechanism_Accepted(t *testing.T) {
	for _, mechanism := range []string{"SHA256", "SHA512"} {
		t.Run("mechanism="+mechanism, func(t *testing.T) {
			cmd := newTestCmd(t, []string{
				"--source-bootstrap", "broker:9092",
				"--use-sasl-scram",
				"--sasl-scram-username", "reader",
				"--sasl-scram-password", "hunter2",
				"--sasl-scram-mechanism", mechanism,
			}...)

			require.NoError(t, cmd.Execute())
		})
	}
}

// R3: credentials come from flags or their auto-bound uppercase environment
// equivalents. The environment path is the documented recommendation, because a
// flag value is visible in the process list.
func TestTxnDiscovery_PasswordFromEnvironment_IsPickedUp(t *testing.T) {
	cmd := newTestCmd(t,
		"--source-bootstrap", "broker:9092",
		"--use-sasl-scram",
		"--sasl-scram-username", "reader",
	)
	t.Setenv("SASL_SCRAM_PASSWORD", "from-the-environment")

	require.NoError(t, cmd.Execute(), "the environment value must satisfy the required-flag check")
	assert.Equal(t, "from-the-environment", saslScramPassword)
}

// R3: an explicitly supplied flag beats the environment. An operator overriding
// an exported credential on one invocation must not silently get the exported one.
func TestTxnDiscovery_ExplicitFlag_BeatsEnvironment(t *testing.T) {
	cmd := newTestCmd(t,
		"--source-bootstrap", "flag-broker:9092",
		"--use-sasl-scram",
		"--sasl-scram-username", "reader",
		"--sasl-scram-password", "from-the-flag",
	)
	t.Setenv("SASL_SCRAM_PASSWORD", "from-the-environment")
	t.Setenv("SOURCE_BOOTSTRAP", "env-broker:9092")

	require.NoError(t, cmd.Execute())
	assert.Equal(t, "from-the-flag", saslScramPassword)
	assert.Equal(t, "flag-broker:9092", sourceBootstrap)
}

// R2: each auth flag resolves to the kcp auth type whose branch in
// NewKafkaClient sets the TLS state the flag names. Getting this mapping wrong
// is not a loud failure — resolving --use-unauthenticated-plaintext to the TLS
// variant produces a client that hangs on a handshake the broker never expects.
func TestTxnDiscovery_AuthFlags_ResolveToTheirAuthType(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want types.AuthType
		// tlsEnabled is the TLS state NewKafkaClient's branch for want puts on
		// the sarama config.
		tlsEnabled bool
	}{
		{
			name:       "iam",
			args:       []string{"--use-sasl-iam", "--aws-region", "us-east-1"},
			want:       types.AuthTypeIAM,
			tlsEnabled: true,
		},
		{
			name:       "sasl scram",
			args:       []string{"--use-sasl-scram", "--sasl-scram-username", "reader", "--sasl-scram-password", "hunter2"},
			want:       types.AuthTypeSASLSCRAM,
			tlsEnabled: true,
		},
		{
			name:       "sasl plain",
			args:       []string{"--use-sasl-plain", "--sasl-plain-username", "reader", "--sasl-plain-password", "hunter2"},
			want:       types.AuthTypeSASLPlain,
			tlsEnabled: false,
		},
		{
			name:       "mutual tls",
			args:       []string{"--use-tls", "--tls-ca-cert", "ca.pem", "--tls-client-cert", "client.pem", "--tls-client-key", "client.key"},
			want:       types.AuthTypeTLS,
			tlsEnabled: true,
		},
		{
			name:       "unauthenticated tls",
			args:       []string{"--use-unauthenticated-tls"},
			want:       types.AuthTypeUnauthenticatedTLS,
			tlsEnabled: true,
		},
		{
			name:       "unauthenticated plaintext",
			args:       []string{"--use-unauthenticated-plaintext"},
			want:       types.AuthTypeUnauthenticatedPlaintext,
			tlsEnabled: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newTestCmd(t, append([]string{"--source-bootstrap", "broker:9092"}, tc.args...)...)
			require.NoError(t, cmd.Execute())

			got := resolveAuth()
			assert.Equal(t, tc.want, got.AuthType)
			assert.Equal(t, tc.tlsEnabled, tlsEnabledFor(tc.want), "the auth type must be the one whose branch sets the expected TLS state")

			// AdminOptionForAuthMethod is the erroring variant: it rejects an
			// auth type whose sub-config was left nil rather than degrading to
			// IAM the way AdminOptionForAuth does.
			_, err := client.AdminOptionForAuthMethod(got.AuthType, got.Method, got.SkipTLSVerify)
			require.NoError(t, err, "the resolved method config must be populated for its auth type")
		})
	}
}

// tlsEnabledFor mirrors the TLS decision NewKafkaClient's auth switch makes, so
// the table above states the observable consequence of the mapping rather than
// only restating the constant.
func tlsEnabledFor(a types.AuthType) bool {
	switch a {
	case types.AuthTypeIAM, types.AuthTypeSASLSCRAM, types.AuthTypeTLS, types.AuthTypeUnauthenticatedTLS:
		return true
	default:
		// SASL/PLAIN resolves to kcp's no-TLS PLAIN variant, and unauthenticated
		// plaintext is plaintext by name.
		return false
	}
}

// R2: --insecure-skip-tls-verify defaults to false, so a run that does not name
// it is not quietly making a TLS-verification decision.
func TestTxnDiscovery_InsecureSkipTLSVerify_DefaultsToFalse(t *testing.T) {
	cmd := newTestCmd(t, []string{"--source-bootstrap", "broker:9092", "--use-unauthenticated-tls"}...)
	require.NoError(t, cmd.Execute())

	assert.False(t, resolveAuth().SkipTLSVerify)
}

// R2: and it reaches the auth helper's third argument when it is set.
func TestTxnDiscovery_InsecureSkipTLSVerify_IsCarried(t *testing.T) {
	cmd := newTestCmd(t, []string{
		"--source-bootstrap", "broker:9092",
		"--use-sasl-scram",
		"--sasl-scram-username", "reader",
		"--sasl-scram-password", "hunter2",
		"--insecure-skip-tls-verify",
	}...)
	require.NoError(t, cmd.Execute())

	assert.True(t, resolveAuth().SkipTLSVerify)
}

// R2: an auth method selected without its full credential set is a flag error,
// not a connection failure discovered minutes later against the broker.
func TestTxnDiscovery_IncompleteCredentialSet_Rejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "iam without a region",
			args: []string{"--use-sasl-iam"},
			want: "aws-region",
		},
		{
			name: "plain without credentials",
			args: []string{"--use-sasl-plain"},
			want: "sasl-plain-username",
		},
		{
			name: "plain username without password",
			args: []string{"--use-sasl-plain", "--sasl-plain-username", "reader"},
			want: "sasl-plain-password",
		},
		{
			name: "mutual tls without certificates",
			args: []string{"--use-tls"},
			want: "tls-ca-cert",
		},
		{
			name: "mutual tls missing the client key",
			args: []string{"--use-tls", "--tls-ca-cert", "ca.pem", "--tls-client-cert", "client.pem"},
			want: "tls-client-key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newTestCmd(t, append([]string{"--source-bootstrap", "broker:9092"}, tc.args...)...)

			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// R4, abuse case: a window of zero or less would open every reader, observe
// nothing, and write a confident, empty txn-discovery.yaml — an artifact that
// reads as "this cluster has no transactional coupling".
func TestTxnDiscovery_NonPositiveDuration_Rejected(t *testing.T) {
	for _, d := range []string{"0", "0s", "-1s", "-5m"} {
		t.Run("duration="+d, func(t *testing.T) {
			cmd := newTestCmd(t,
				"--source-bootstrap", "broker:9092",
				"--use-unauthenticated-plaintext",
				"--duration", d,
			)

			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "--duration")
		})
	}
}

// R4, abuse case: --interval feeds time.NewTicker, which panics on a
// non-positive period. A crash mid-observation loses the whole window.
func TestTxnDiscovery_NonPositiveInterval_Rejected(t *testing.T) {
	for _, i := range []string{"0", "0s", "-1s"} {
		t.Run("interval="+i, func(t *testing.T) {
			cmd := newTestCmd(t,
				"--source-bootstrap", "broker:9092",
				"--use-unauthenticated-plaintext",
				"--interval", i,
			)

			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "--interval")
		})
	}
}

// R18: the audit trail follows --out's directory. Pointing --out at an
// engagement folder and leaving the trail in the working directory splits the
// two artifacts an operator has to read together.
func TestTxnDiscovery_AuditLogPath_DefaultsBesideOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "no paths given",
			args: nil,
			want: DefaultAuditBasename,
		},
		{
			name: "out in another directory",
			args: []string{"--out", "/tmp/engagement/groups.yaml"},
			want: "/tmp/engagement/" + DefaultAuditBasename,
		},
		{
			name: "out is a bare filename",
			args: []string{"--out", "groups.yaml"},
			want: DefaultAuditBasename,
		},
		{
			name: "explicit audit path wins",
			args: []string{"--out", "/tmp/engagement/groups.yaml", "--audit-log-out", "/var/log/trail.jsonl"},
			want: "/var/log/trail.jsonl",
		},
		{
			name: "no-audit-log disables it",
			args: []string{"--no-audit-log"},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newTestCmd(t, append([]string{
				"--source-bootstrap", "broker:9092",
				"--use-unauthenticated-plaintext",
			}, tc.args...)...)
			require.NoError(t, cmd.Execute())

			assert.Equal(t, tc.want, parseOpts().AuditLogPath)
		})
	}
}

// R18: naming a path for a trail that will not be written is a contradiction, so
// it is rejected rather than silently resolved one way.
func TestTxnDiscovery_AuditPathAndNoAuditLog_AreMutuallyExclusive(t *testing.T) {
	cmd := newTestCmd(t,
		"--source-bootstrap", "broker:9092",
		"--use-unauthenticated-plaintext",
		"--audit-log-out", "/tmp/trail.jsonl",
		"--no-audit-log",
	)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "were all set")
}
