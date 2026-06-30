package clusterlink

import (
	"os"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

func TestLinkAuthFromSource(t *testing.T) {
	tests := []struct {
		name      string
		auth      types.AuthMethodConfig
		wantProto string
		wantMech  string
		jaasHas   string // substring expected in SaslJaasConfig ("" => must be empty)
		wantCA    string // expected CACertPath ("" => must be empty)
	}{
		{
			name:      "unauthenticated_plaintext",
			auth:      types.AuthMethodConfig{UnauthenticatedPlaintext: &types.UnauthenticatedPlaintextConfig{Use: true}},
			wantProto: "PLAINTEXT",
		},
		{
			name:      "unauthenticated_tls",
			auth:      types.AuthMethodConfig{UnauthenticatedTLS: &types.UnauthenticatedTLSConfig{Use: true}},
			wantProto: "SSL",
		},
		{
			name:      "unauthenticated_tls_with_ca",
			auth:      types.AuthMethodConfig{UnauthenticatedTLS: &types.UnauthenticatedTLSConfig{Use: true, CACert: "/certs/ca.pem"}},
			wantProto: "SSL",
			wantCA:    "/certs/ca.pem",
		},
		{
			name:      "sasl_scram_256",
			auth:      types.AuthMethodConfig{SASLScram: &types.SASLScramConfig{Use: true, Username: "kcp", Password: "kcp-secret", Mechanism: "SHA256"}},
			wantProto: "SASL_SSL",
			wantMech:  "SCRAM-SHA-256",
			jaasHas:   `ScramLoginModule required username="kcp" password="kcp-secret"`,
		},
		{
			name:      "sasl_scram_256_with_ca",
			auth:      types.AuthMethodConfig{SASLScram: &types.SASLScramConfig{Use: true, Username: "kcp", Password: "kcp-secret", Mechanism: "SHA256", CACert: "/certs/ca.pem"}},
			wantProto: "SASL_SSL",
			wantMech:  "SCRAM-SHA-256",
			jaasHas:   `ScramLoginModule required username="kcp" password="kcp-secret"`,
			wantCA:    "/certs/ca.pem",
		},
		{
			name:      "sasl_scram_512",
			auth:      types.AuthMethodConfig{SASLScram: &types.SASLScramConfig{Use: true, Username: "kcp", Password: "kcp-secret", Mechanism: "SHA512"}},
			wantProto: "SASL_SSL",
			wantMech:  "SCRAM-SHA-512",
			jaasHas:   `ScramLoginModule required username="kcp" password="kcp-secret"`,
		},
		{
			name:      "sasl_plain",
			auth:      types.AuthMethodConfig{SASLPlain: &types.SASLPlainConfig{Use: true, Username: "admin", Password: "admin-secret"}},
			wantProto: "SASL_PLAINTEXT",
			wantMech:  "PLAIN",
			jaasHas:   `PlainLoginModule required username="admin" password="admin-secret"`,
		},
		{
			// sasl_plain_with_ca (R2-H1): a ca_cert on a SASL/PLAIN source selects
			// SASL_SSL and wires the CA into the link truststore, matching the
			// source-read path (AdminOptionForAuth). Without it, the link would
			// attempt SASL_PLAINTEXT against a TLS listener (or send the password
			// in cleartext) and disagree with how KCP scanned the source.
			name:      "sasl_plain_with_ca",
			auth:      types.AuthMethodConfig{SASLPlain: &types.SASLPlainConfig{Use: true, Username: "admin", Password: "admin-secret", CACert: "/certs/ca.pem"}},
			wantProto: "SASL_SSL",
			wantMech:  "PLAIN",
			jaasHas:   `PlainLoginModule required username="admin" password="admin-secret"`,
			wantCA:    "/certs/ca.pem",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			la, err := LinkAuthFromSource(types.KafkaSourceConn{AuthMethod: tc.auth})
			require.NoError(t, err)
			require.Equal(t, tc.wantProto, la.SecurityProtocol)
			require.Equal(t, tc.wantMech, la.SaslMechanism)
			if tc.jaasHas == "" {
				require.Empty(t, la.SaslJaasConfig)
			} else {
				require.Contains(t, la.SaslJaasConfig, tc.jaasHas)
			}
			require.Equal(t, tc.wantCA, la.CACertPath)
		})
	}
}

func TestLinkAuthFromSource_MTLS(t *testing.T) {
	la, err := LinkAuthFromSource(types.KafkaSourceConn{
		AuthMethod: types.AuthMethodConfig{
			TLS: &types.TLSConfig{Use: true, CACert: "/c/ca.crt", ClientCert: "/c/client.crt", ClientKey: "/c/client.key"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "SSL", la.SecurityProtocol)
	require.Empty(t, la.SaslMechanism)
	require.Equal(t, "/c/ca.crt", la.CACertPath)
	require.Equal(t, "/c/client.crt", la.ClientCertPath)
	require.Equal(t, "/c/client.key", la.ClientKeyPath)
}

func TestLinkAuthFromSource_NoMethod(t *testing.T) {
	_, err := LinkAuthFromSource(types.KafkaSourceConn{})
	require.Error(t, err)
}

func TestLinkAuth_LoadTLS_Plaintext(t *testing.T) {
	m, err := LinkAuth{SecurityProtocol: "PLAINTEXT"}.LoadTLS()
	require.NoError(t, err)
	require.Nil(t, m)
}

func TestLinkAuth_LoadTLS_CAOnly(t *testing.T) {
	dir := t.TempDir()
	ca := dir + "/ca.crt"
	require.NoError(t, os.WriteFile(ca, []byte("CA-PEM"), 0600))
	m, err := LinkAuth{SecurityProtocol: "SASL_SSL", CACertPath: ca}.LoadTLS()
	require.NoError(t, err)
	require.NotNil(t, m)
	require.Equal(t, "CA-PEM", m.CACertPEM)
	require.Empty(t, m.ClientCertPEM)
}

func TestLinkAuth_LoadTLS_MTLS(t *testing.T) {
	dir := t.TempDir()
	write := func(n, c string) string {
		p := dir + "/" + n
		require.NoError(t, os.WriteFile(p, []byte(c), 0600))
		return p
	}
	m, err := LinkAuth{
		SecurityProtocol: "SSL",
		CACertPath:       write("ca", "CA"),
		ClientCertPath:   write("crt", "CERT"),
		ClientKeyPath:    write("key", "KEY"),
	}.LoadTLS()
	require.NoError(t, err)
	require.NotNil(t, m)
	require.Equal(t, "CA", m.CACertPEM)
	require.Equal(t, "CERT", m.ClientCertPEM)
	require.Equal(t, "KEY", m.ClientKeyPEM)
}

func TestLinkAuth_LoadTLS_MissingFile(t *testing.T) {
	_, err := LinkAuth{CACertPath: "/no/such/ca.crt"}.LoadTLS()
	require.Error(t, err)
}
