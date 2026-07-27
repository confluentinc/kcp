package txndiscovery

import (
	"testing"

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
