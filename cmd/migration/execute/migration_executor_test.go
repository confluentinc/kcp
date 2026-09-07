package execute

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSourceClusterAuth_TlsCaCertPlumbedToEveryTLSPath is the regression test for
// review finding #8: --tls-ca-cert must reach the CACert field of every TLS-fronted
// source auth method (SASL/SCRAM and SASL/PLAIN over TLS, unauthenticated-TLS, mTLS),
// not only the mTLS path — so a migration source behind a private CA can be verified.
func TestSourceClusterAuth_TlsCaCertPlumbedToEveryTLSPath(t *testing.T) {
	const ca = "/etc/certs/source-ca.pem"

	t.Run("sasl_scram", func(t *testing.T) {
		a := sourceClusterAuth(MigrationExecutorOpts{
			AuthType: types.AuthTypeSASLSCRAM, TlsCaCert: ca,
			SaslScramUsername: "u", SaslScramPassword: "p", SaslScramMechanism: "SHA512",
		})
		require.NotNil(t, a.AuthMethod.SASLScram)
		assert.Equal(t, ca, a.AuthMethod.SASLScram.CACert)
	})

	t.Run("sasl_plain", func(t *testing.T) {
		a := sourceClusterAuth(MigrationExecutorOpts{
			AuthType: types.AuthTypeSASLPlain, TlsCaCert: ca,
			SaslPlainUsername: "u", SaslPlainPassword: "p",
		})
		require.NotNil(t, a.AuthMethod.SASLPlain)
		assert.Equal(t, ca, a.AuthMethod.SASLPlain.CACert, "ca_cert selects SASL_SSL over cleartext SASL_PLAINTEXT")
	})

	t.Run("tls_mtls", func(t *testing.T) {
		a := sourceClusterAuth(MigrationExecutorOpts{
			AuthType: types.AuthTypeTLS, TlsCaCert: ca,
			TlsClientCert: "c.pem", TlsClientKey: "k.pem",
		})
		require.NotNil(t, a.AuthMethod.TLS)
		assert.Equal(t, ca, a.AuthMethod.TLS.CACert)
	})

	t.Run("unauthenticated_tls", func(t *testing.T) {
		a := sourceClusterAuth(MigrationExecutorOpts{AuthType: types.AuthTypeUnauthenticatedTLS, TlsCaCert: ca})
		require.NotNil(t, a.AuthMethod.UnauthenticatedTLS)
		assert.Equal(t, ca, a.AuthMethod.UnauthenticatedTLS.CACert)
	})

	t.Run("plaintext ignores ca", func(t *testing.T) {
		a := sourceClusterAuth(MigrationExecutorOpts{AuthType: types.AuthTypeUnauthenticatedPlaintext, TlsCaCert: ca})
		require.NotNil(t, a.AuthMethod.UnauthenticatedPlaintext)
	})
}
