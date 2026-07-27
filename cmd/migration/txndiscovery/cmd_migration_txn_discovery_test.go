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
