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
	"strings"
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

// startTLSServer starts an httptest TLS server and writes its CA cert to a temp
// file so tests can configure a custom trust root. Returns the server and the
// path to the CA PEM file.
func startTLSServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	f, err := os.Create(caPath)
	require.NoError(t, err)
	err = pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return srv, caPath
}

// TestHTTPClient_BasicWithCACert_TrustsPrivateCA confirms a basic-auth client
// with an explicit ca_cert trusts the httptest server's self-signed certificate.
func TestHTTPClient_BasicWithCACert_TrustsPrivateCA(t *testing.T) {
	srv, caPath := startTLSServer(t)
	c := Credentials{Basic: &BasicAuth{Username: "u", Password: "p", CACert: caPath}}
	client, err := c.HTTPClient()
	require.NoError(t, err)
	concreteClient, ok := client.(*http.Client)
	require.True(t, ok)
	resp, err := concreteClient.Get(srv.URL) //nolint:noctx
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestHTTPClient_BasicNoCACert_FailsVerification confirms that a basic-auth
// client without a ca_cert fails the TLS handshake against a private-CA server.
func TestHTTPClient_BasicNoCACert_FailsVerification(t *testing.T) {
	srv, _ := startTLSServer(t)
	c := Credentials{Basic: &BasicAuth{Username: "u", Password: "p"}}
	client, err := c.HTTPClient()
	require.NoError(t, err)
	concreteClient, ok := client.(*http.Client)
	require.True(t, ok)
	_, err = concreteClient.Get(srv.URL) //nolint:noctx
	require.Error(t, err)
	errStr := strings.ToLower(err.Error())
	assert.True(t, strings.Contains(errStr, "x509") || strings.Contains(errStr, "certificate"),
		"expected TLS verification error, got: %v", err)
}

// TestHTTPClient_BasicSkipVerify_Connects confirms insecure_skip_verify bypasses
// certificate verification even without a ca_cert.
func TestHTTPClient_BasicSkipVerify_Connects(t *testing.T) {
	srv, _ := startTLSServer(t)
	c := Credentials{Basic: &BasicAuth{Username: "u", Password: "p", InsecureSkipVerify: true}}
	client, err := c.HTTPClient()
	require.NoError(t, err)
	concreteClient, ok := client.(*http.Client)
	require.True(t, ok)
	resp, err := concreteClient.Get(srv.URL) //nolint:noctx
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestHTTPClient_BearerWithCACert_TrustsPrivateCA confirms a bearer-auth client
// with an explicit ca_cert trusts the httptest server's self-signed certificate.
func TestHTTPClient_BearerWithCACert_TrustsPrivateCA(t *testing.T) {
	srv, caPath := startTLSServer(t)
	c := Credentials{Bearer: &BearerCreds{Token: "t", CACert: caPath}}
	client, err := c.HTTPClient()
	require.NoError(t, err)
	concreteClient, ok := client.(*http.Client)
	require.True(t, ok)
	resp, err := concreteClient.Get(srv.URL) //nolint:noctx
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestHTTPClient_NotDefaultClient confirms HTTPClient never returns the shared
// http.DefaultClient.
func TestHTTPClient_NotDefaultClient(t *testing.T) {
	c := Credentials{Basic: &BasicAuth{Username: "u", Password: "p"}}
	client, err := c.HTTPClient()
	require.NoError(t, err)
	concreteClient, ok := client.(*http.Client)
	require.True(t, ok)
	assert.NotSame(t, http.DefaultClient, concreteClient)
}

// --- ${ENV_VAR} interpolation (opt-in, per file) ---

// TestLoadCredentials_InterpolatesWhenOptedIn is the headline behaviour: with
// interpolate: true, ${VAR} references resolve from the environment.
func TestLoadCredentials_InterpolatesWhenOptedIn(t *testing.T) {
	t.Setenv("CC_API_KEY", "KEY-123")
	t.Setenv("CC_API_SECRET", "SECRET-456")
	yaml := "interpolate: true\napi_key: ${CC_API_KEY}\napi_secret: ${CC_API_SECRET}\n"
	c, err := LoadCredentials(writeTemp(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "KEY-123", c.APIKey)
	assert.Equal(t, "SECRET-456", c.APISecret)
}

// TestLoadCredentials_NoInterpolationByDefault pins that every already-shipped
// credentials file is read byte-for-byte as before — interpolation is a new
// capability, not a behaviour change.
func TestLoadCredentials_NoInterpolationByDefault(t *testing.T) {
	t.Setenv("CC_API_KEY", "KEY-123")
	yaml := "api_key: ${CC_API_KEY}\napi_secret: literal\n"
	c, err := LoadCredentials(writeTemp(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "${CC_API_KEY}", c.APIKey, "absent interpolate: every value is literal")
}

// TestLoadCredentials_InterpolateUndefinedVariableFails — hard error naming the
// variable, never the value of a sibling that did resolve.
func TestLoadCredentials_InterpolateUndefinedVariableFails(t *testing.T) {
	t.Setenv("CC_API_KEY", "KEY-123")
	yaml := "interpolate: true\napi_key: ${CC_API_KEY}\napi_secret: ${KCP_UNSET_SECRET}\n"
	_, err := LoadCredentials(writeTemp(t, yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KCP_UNSET_SECRET")
	assert.NotContains(t, err.Error(), "KEY-123")
}

// TestLoadCredentials_InterpolatesBeforeValidation is the ordering requirement
// from §10: LoadCredentials os.Stats ca_cert, so resolution placed after
// validation would yield `ca_cert file "${CA_PATH}": no such file`.
func TestLoadCredentials_InterpolatesBeforeValidation(t *testing.T) {
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, []byte("pem"), 0600))
	t.Setenv("CA_PATH", caFile)

	yaml := "interpolate: true\nbasic:\n  username: u\n  password: p\n  ca_cert: ${CA_PATH}\n"
	c, err := LoadCredentials(writeTemp(t, yaml))
	require.NoError(t, err, "ca_cert must be resolved before its existence is checked")
	assert.Equal(t, caFile, c.Basic.CACert)
}

// TestLoadCredentials_InterpolatedValueIsNotReparsed proves the post-parse
// design: a secret containing YAML structure cannot alter the document.
func TestLoadCredentials_InterpolatedValueIsNotReparsed(t *testing.T) {
	t.Setenv("EVIL", "p\nusername: attacker")
	yaml := "interpolate: true\nbasic:\n  username: real-user\n  password: ${EVIL}\n"
	c, err := LoadCredentials(writeTemp(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "real-user", c.Basic.Username, "the injected key must not have taken effect")
	assert.Equal(t, "p\nusername: attacker", c.Basic.Password)
}

// --- parse / validate split ---

// TestParseCredentials_AppliesSameValidationAsFile is the point of the split:
// an inline block and a referenced file run the same validation, so a rule can
// never apply to one spelling and not the other.
func TestParseCredentials_AppliesSameValidationAsFile(t *testing.T) {
	body := []byte("basic:\n  username: a\n  password: b\napi_key: K\napi_secret: S\n")

	_, parseErr := ParseCredentials(body)
	_, loadErr := LoadCredentials(writeTemp(t, string(body)))

	require.Error(t, parseErr)
	require.Error(t, loadErr)
	assert.Equal(t, loadErr.Error(), parseErr.Error())
}

// TestParseCredentials_RejectsUnknownFields confirms goccy's strict mode is
// applied on the bytes path too, not only via LoadCredentials.
func TestParseCredentials_RejectsUnknownFields(t *testing.T) {
	_, err := ParseCredentials([]byte("api_key: K\napi_secret: S\ntypo_field: x\n"))
	require.Error(t, err)
}

// TestValidateCredentials_IsReusableOnAnAlreadyBuiltStruct lets the manifest
// path validate a block it assembled itself.
func TestValidateCredentials_IsReusableOnAnAlreadyBuiltStruct(t *testing.T) {
	require.NoError(t, ValidateCredentials(&Credentials{APIKey: "K", APISecret: "S"}))
	require.Error(t, ValidateCredentials(&Credentials{}))
}

// TestHTTPClient_APIKeyWithCACert_TrustsPrivateCA confirms the flat
// api_key/api_secret form honours a sibling ca_cert, so a destination REST
// endpoint behind a private CA is reachable without restating the key/secret as
// a basic block.
func TestHTTPClient_APIKeyWithCACert_TrustsPrivateCA(t *testing.T) {
	srv, caPath := startTLSServer(t)
	c := Credentials{APIKey: "KEY", APISecret: "SECRET", CACert: caPath}
	client, err := c.HTTPClient()
	require.NoError(t, err)
	concreteClient, ok := client.(*http.Client)
	require.True(t, ok)
	resp, err := concreteClient.Get(srv.URL) //nolint:noctx
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestHTTPClient_APIKeySkipVerify_Connects confirms a sibling
// insecure_skip_verify on the api_key form bypasses certificate verification.
// This is the leg --insecure-skip-tls-verify reaches today.
func TestHTTPClient_APIKeySkipVerify_Connects(t *testing.T) {
	srv, _ := startTLSServer(t)
	c := Credentials{APIKey: "KEY", APISecret: "SECRET", InsecureSkipVerify: true}
	client, err := c.HTTPClient()
	require.NoError(t, err)
	concreteClient, ok := client.(*http.Client)
	require.True(t, ok)
	resp, err := concreteClient.Get(srv.URL) //nolint:noctx
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestHTTPClient_APIKeyNoCACert_FailsVerification pins that the api_key form
// still verifies by default — the siblings are opt-in, not a loosening.
func TestHTTPClient_APIKeyNoCACert_FailsVerification(t *testing.T) {
	srv, _ := startTLSServer(t)
	c := Credentials{APIKey: "KEY", APISecret: "SECRET"}
	client, err := c.HTTPClient()
	require.NoError(t, err)
	concreteClient, ok := client.(*http.Client)
	require.True(t, ok)
	_, err = concreteClient.Get(srv.URL) //nolint:noctx
	require.Error(t, err)
}

// TestLoadCredentials_APIKeyWithTLSSiblings parses the full api_key form.
func TestLoadCredentials_APIKeyWithTLSSiblings(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(caFile, []byte("not-a-real-pem"), 0600))

	yaml := "api_key: KEY\napi_secret: SECRET\nca_cert: " + caFile + "\ninsecure_skip_verify: true\n"
	c, err := LoadCredentials(writeTemp(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, caFile, c.CACert)
	assert.True(t, c.InsecureSkipVerify)
}

// TestLoadCredentials_APIKeyBadCACert confirms the api_key form's ca_cert is
// existence-checked like basic's, rather than failing later at handshake time.
func TestLoadCredentials_APIKeyBadCACert(t *testing.T) {
	yaml := "api_key: KEY\napi_secret: SECRET\nca_cert: /no/such/file\n"
	_, err := LoadCredentials(writeTemp(t, yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ca_cert")
}

// TestLoadCredentials_RejectsTopLevelTLSWithoutAPIKey confirms the top-level
// siblings are rejected alongside basic/bearer/mtls rather than silently
// ignored — those blocks carry their own ca_cert, and a value that looks
// applied but is not is the worst outcome for a TLS-trust setting.
func TestLoadCredentials_RejectsTopLevelTLSWithoutAPIKey(t *testing.T) {
	yaml := "basic:\n  username: u\n  password: p\ninsecure_skip_verify: true\n"
	_, err := LoadCredentials(writeTemp(t, yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_key")
}

// TestLoadCredentials_BasicBadCACert confirms LoadCredentials rejects a basic
// block whose ca_cert points to a non-existent file.
func TestLoadCredentials_BasicBadCACert(t *testing.T) {
	yaml := "basic:\n  username: u\n  password: p\n  ca_cert: /no/such/file\n"
	_, err := LoadCredentials(writeTemp(t, yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ca_cert")
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
