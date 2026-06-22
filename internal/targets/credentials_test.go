package targets

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "target-creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0600))
	return p
}

func TestLoadCredentials_Basic(t *testing.T) {
	c, err := LoadCredentials(writeTemp(t, "basic:\n  username: admin\n  password: admin-secret\n"))
	require.NoError(t, err)
	require.NotNil(t, c.Basic)
	require.Equal(t, "admin", c.Basic.Username)
}

func TestLoadCredentials_CloudApiKey(t *testing.T) {
	c, err := LoadCredentials(writeTemp(t, "api_key: KEY\napi_secret: SECRET\n"))
	require.NoError(t, err)
	require.Equal(t, "KEY", c.APIKey)
}

func TestLoadCredentials_RejectsMultipleBlocks(t *testing.T) {
	_, err := LoadCredentials(writeTemp(t, "basic:\n  username: a\n  password: b\napi_key: K\napi_secret: S\n"))
	require.ErrorContains(t, err, "exactly one")
}

func TestLoadCredentials_RejectsNone(t *testing.T) {
	_, err := LoadCredentials(writeTemp(t, "{}\n"))
	require.ErrorContains(t, err, "exactly one")
}

func TestLoadCredentials_RejectsPartialCloud(t *testing.T) {
	_, err := LoadCredentials(writeTemp(t, "api_key: KEY\n"))
	require.ErrorContains(t, err, "both be set or both omitted")
}

func TestLoadCredentials_Bearer(t *testing.T) {
	c, err := LoadCredentials(writeTemp(t, "bearer:\n  token: jwt-abc\n"))
	require.NoError(t, err)
	require.NotNil(t, c.Bearer)
	require.Equal(t, "jwt-abc", c.Bearer.Token)
}

func TestLoadCredentials_RejectsEmptyBearerToken(t *testing.T) {
	_, err := LoadCredentials(writeTemp(t, "bearer:\n  token: \"\"\n"))
	require.ErrorContains(t, err, "bearer.token must not be empty")
}

func TestLoadCredentials_MTLS(t *testing.T) {
	dir := t.TempDir()
	caPEM, certPEM, keyPEM := genCertMaterial(t)
	ca := filepath.Join(dir, "ca.pem")
	crt := filepath.Join(dir, "client.pem")
	key := filepath.Join(dir, "client.key")
	require.NoError(t, os.WriteFile(ca, caPEM, 0600))
	require.NoError(t, os.WriteFile(crt, certPEM, 0600))
	require.NoError(t, os.WriteFile(key, keyPEM, 0600))

	c, err := LoadCredentials(writeTemp(t,
		"mtls:\n  ca_cert: "+ca+"\n  client_cert: "+crt+"\n  client_key: "+key+"\n"))
	require.NoError(t, err)
	require.NotNil(t, c.MTLS)
	require.Equal(t, crt, c.MTLS.ClientCert)
}

func TestLoadCredentials_MTLS_RejectsMissingFiles(t *testing.T) {
	_, err := LoadCredentials(writeTemp(t,
		"mtls:\n  client_cert: /no/such/cert.pem\n  client_key: /no/such/key.pem\n"))
	require.ErrorContains(t, err, "certificate file")
}

func TestLoadCredentials_MTLS_RejectsMissingKeyField(t *testing.T) {
	_, err := LoadCredentials(writeTemp(t, "mtls:\n  client_cert: /tmp/cert.pem\n"))
	require.ErrorContains(t, err, "requires both client_cert and client_key")
}

func TestLoadCredentials_RejectsBearerPlusMTLS(t *testing.T) {
	_, err := LoadCredentials(writeTemp(t,
		"bearer:\n  token: t\nmtls:\n  client_cert: c\n  client_key: k\n"))
	require.ErrorContains(t, err, "exactly one")
}

func TestCredentials_Authenticator(t *testing.T) {
	require.IsType(t, clusterlink.BasicAuth{}, Credentials{Basic: &BasicAuth{Username: "u", Password: "p"}}.authenticator())
	require.IsType(t, clusterlink.BasicAuth{}, Credentials{APIKey: "k", APISecret: "s"}.authenticator())
	require.IsType(t, clusterlink.BearerAuth{}, Credentials{Bearer: &BearerCreds{Token: "t"}}.authenticator())
	require.IsType(t, clusterlink.NoHeaderAuth{}, Credentials{MTLS: &MTLSCreds{}}.authenticator())
}

// TestCredentials_HTTPClient_MTLS proves the mtls client presents the client
// certificate to a server that requires and verifies one: the built client
// succeeds where a default (certless) client is rejected at the handshake.
func TestCredentials_HTTPClient_MTLS(t *testing.T) {
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
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	srv.StartTLS()
	defer srv.Close()

	creds := Credentials{MTLS: &MTLSCreds{CACert: caFile, ClientCert: crtFile, ClientKey: keyFile}}
	client, err := creds.HTTPClient()
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err, "mtls client must be accepted by the client-auth server")
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// A default client (no client cert, no CA trust) must be rejected.
	_, err = http.DefaultClient.Get(srv.URL)
	require.Error(t, err, "certless client must be rejected")
}

// genCertMaterial returns (caPEM, certPEM, keyPEM) where the leaf cert is signed
// by the CA and is valid for both server and client auth on 127.0.0.1/localhost.
func genCertMaterial(t *testing.T) (caPEM, certPEM, keyPEM []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "kcp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	require.NoError(t, err)

	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER})
	return caPEM, certPEM, keyPEM
}
