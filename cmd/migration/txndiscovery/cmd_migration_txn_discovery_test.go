package txndiscovery

import (
	"testing"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
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

// R2: the auth flags mirror `kcp migration execute` — mutually exclusive, one required.
func TestTxnDiscovery_TwoAuthMethods_Rejected(t *testing.T) {
	resetFlags()

	cmd := NewMigrationTxnDiscoveryCmd()
	cmd.SetArgs([]string{
		"--source-bootstrap", "broker:9092",
		"--use-unauthenticated-plaintext",
		"--use-unauthenticated-tls",
	})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "were all set")
}

// R2: an auth method is not optional — a run with none would silently pick one.
func TestTxnDiscovery_NoAuthMethod_Rejected(t *testing.T) {
	resetFlags()

	cmd := NewMigrationTxnDiscoveryCmd()
	cmd.SetArgs([]string{"--source-bootstrap", "broker:9092"})

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
			resetFlags()

			cmd := NewMigrationTxnDiscoveryCmd()
			cmd.SetArgs(append([]string{"--source-bootstrap", "broker:9092"}, tc.args...))

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
		})
	}
}

// R6: the two mechanisms kcp supports pass validation. Without this the previous
// test is satisfied by rejecting every mechanism.
func TestTxnDiscovery_SupportedScramMechanism_Accepted(t *testing.T) {
	for _, mechanism := range []string{"SHA256", "SHA512"} {
		t.Run("mechanism="+mechanism, func(t *testing.T) {
			resetFlags()

			cmd := NewMigrationTxnDiscoveryCmd()
			cmd.SetArgs([]string{
				"--source-bootstrap", "broker:9092",
				"--use-sasl-scram",
				"--sasl-scram-username", "reader",
				"--sasl-scram-password", "hunter2",
				"--sasl-scram-mechanism", mechanism,
			})

			require.NoError(t, cmd.Execute())
		})
	}
}

// R3: credentials come from flags or their auto-bound uppercase environment
// equivalents. The environment path is the documented recommendation, because a
// flag value is visible in the process list.
func TestTxnDiscovery_PasswordFromEnvironment_IsPickedUp(t *testing.T) {
	resetFlags()
	t.Setenv("SASL_SCRAM_PASSWORD", "from-the-environment")

	cmd := NewMigrationTxnDiscoveryCmd()
	cmd.SetArgs([]string{
		"--source-bootstrap", "broker:9092",
		"--use-sasl-scram",
		"--sasl-scram-username", "reader",
	})

	require.NoError(t, cmd.Execute(), "the environment value must satisfy the required-flag check")
	assert.Equal(t, "from-the-environment", saslScramPassword)
}

// R3: an explicitly supplied flag beats the environment. An operator overriding
// an exported credential on one invocation must not silently get the exported one.
func TestTxnDiscovery_ExplicitFlag_BeatsEnvironment(t *testing.T) {
	resetFlags()
	t.Setenv("SASL_SCRAM_PASSWORD", "from-the-environment")
	t.Setenv("SOURCE_BOOTSTRAP", "env-broker:9092")

	cmd := NewMigrationTxnDiscoveryCmd()
	cmd.SetArgs([]string{
		"--source-bootstrap", "flag-broker:9092",
		"--use-sasl-scram",
		"--sasl-scram-username", "reader",
		"--sasl-scram-password", "from-the-flag",
	})

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
			resetFlags()

			cmd := NewMigrationTxnDiscoveryCmd()
			cmd.SetArgs(append([]string{"--source-bootstrap", "broker:9092"}, tc.args...))
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
	resetFlags()

	cmd := NewMigrationTxnDiscoveryCmd()
	cmd.SetArgs([]string{"--source-bootstrap", "broker:9092", "--use-unauthenticated-tls"})
	require.NoError(t, cmd.Execute())

	assert.False(t, resolveAuth().SkipTLSVerify)
}

// R2: and it reaches the auth helper's third argument when it is set.
func TestTxnDiscovery_InsecureSkipTLSVerify_IsCarried(t *testing.T) {
	resetFlags()

	cmd := NewMigrationTxnDiscoveryCmd()
	cmd.SetArgs([]string{
		"--source-bootstrap", "broker:9092",
		"--use-sasl-scram",
		"--sasl-scram-username", "reader",
		"--sasl-scram-password", "hunter2",
		"--insecure-skip-tls-verify",
	})
	require.NoError(t, cmd.Execute())

	assert.True(t, resolveAuth().SkipTLSVerify)
}
