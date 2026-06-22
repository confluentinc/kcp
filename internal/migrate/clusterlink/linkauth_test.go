package clusterlink

import (
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
		wantTLS   bool   // expect non-nil TLS material
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
			name:      "sasl_scram_256",
			auth:      types.AuthMethodConfig{SASLScram: &types.SASLScramConfig{Use: true, Username: "kcp", Password: "kcp-secret", Mechanism: "SHA256"}},
			wantProto: "SASL_SSL",
			wantMech:  "SCRAM-SHA-256",
			jaasHas:   `ScramLoginModule required username="kcp" password="kcp-secret"`,
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
			wantProto: "SASL_SSL",
			wantMech:  "PLAIN",
			jaasHas:   `PlainLoginModule required username="admin" password="admin-secret"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			la, err := LinkAuthFromSource(types.OSKClusterAuth{AuthMethod: tc.auth})
			require.NoError(t, err)
			require.Equal(t, tc.wantProto, la.SecurityProtocol)
			require.Equal(t, tc.wantMech, la.SaslMechanism)
			if tc.jaasHas == "" {
				require.Empty(t, la.SaslJaasConfig)
			} else {
				require.Contains(t, la.SaslJaasConfig, tc.jaasHas)
			}
		})
	}
}

func TestLinkAuthFromSource_MTLS(t *testing.T) {
	la, err := LinkAuthFromSource(types.OSKClusterAuth{
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
	_, err := LinkAuthFromSource(types.OSKClusterAuth{})
	require.Error(t, err)
}
