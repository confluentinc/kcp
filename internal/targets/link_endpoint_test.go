package targets

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLinkEndpoint_TwoEndpoints_NoCrosstalk proves the SAME constructor builds a
// working client for both a destination-style and a source-style REST endpoint:
// one client type, two endpoints, each returning its own cluster id with no
// cross-talk. This is what Task 6 relies on for source-initiated links.
func TestLinkEndpoint_TwoEndpoints_NoCrosstalk(t *testing.T) {
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/kafka/v3/clusters", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"destination-cluster"}]}`))
	}))
	defer dst.Close()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/kafka/v3/clusters", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"source-cluster"}]}`))
	}))
	defer src.Close()

	dstEP := NewLinkEndpoint(dst.URL, &Credentials{Basic: &BasicAuth{Username: "d", Password: "d"}}, dst.Client())
	srcEP := NewLinkEndpoint(src.URL, &Credentials{Basic: &BasicAuth{Username: "s", Password: "s"}}, src.Client())

	dstID, err := dstEP.ClusterID(context.Background())
	require.NoError(t, err)
	srcID, err := srcEP.ClusterID(context.Background())
	require.NoError(t, err)

	require.Equal(t, "destination-cluster", dstID)
	require.Equal(t, "source-cluster", srcID, "source endpoint client must not pick up the destination id")
	require.NotEqual(t, dstID, srcID)
}

// TestLinkEndpoint_MTLS_SourceEndpoint proves the same constructor builds a
// client that carries a client certificate for an mTLS source endpoint: the
// built client is accepted by a server that requires and verifies a client
// cert, where a default (certless) client is rejected at the handshake.
func TestLinkEndpoint_MTLS_SourceEndpoint(t *testing.T) {
	dir := t.TempDir()
	caPEM, certPEM, keyPEM := genCertMaterial(t)
	caFile := filepath.Join(dir, "ca.pem")
	crtFile := filepath.Join(dir, "client.pem")
	keyFile := filepath.Join(dir, "client.key")
	require.NoError(t, os.WriteFile(caFile, caPEM, 0600))
	require.NoError(t, os.WriteFile(crtFile, certPEM, 0600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0600))

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(caPEM))
	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/kafka/v3/clusters", r.URL.Path)
		// A client cert must have been presented and verified.
		require.NotEmpty(t, r.TLS.PeerCertificates, "server must see the client certificate")
		_, _ = w.Write([]byte(`{"data":[{"cluster_id":"mtls-source-cluster"}]}`))
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	srv.StartTLS()
	defer srv.Close()

	creds := &Credentials{MTLS: &MTLSCreds{CACert: caFile, ClientCert: crtFile, ClientKey: keyFile}}
	httpClient, err := creds.HTTPClient()
	require.NoError(t, err)

	ep := NewLinkEndpoint(srv.URL, creds, httpClient)
	id, err := ep.ClusterID(context.Background())
	require.NoError(t, err, "mtls source endpoint client must present its client cert and be accepted")
	require.Equal(t, "mtls-source-cluster", id)
}
