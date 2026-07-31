package utils

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writeTestKeyPair generates a self-signed ECDSA cert + key and writes them as PEM,
// returning (certPath, keyPath) — enough for tls.LoadX509KeyPair to succeed.
func writeTestKeyPair(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"kcp test"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certPath := filepath.Join(dir, "client.crt")
	keyPath := filepath.Join(dir, "client.key")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600))
	return certPath, keyPath
}

// a self-signed CA PEM is overkill here; AppendCertsFromPEM accepts any valid
// CERTIFICATE block. We generate one cheaply via the same path the kafka_admin
// tests use is not importable, so embed a minimal valid cert at test time.
func writePEM(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0600))
	return p
}

func TestCACertPool_EmptyPath(t *testing.T) {
	_, err := CACertPool("")
	require.Error(t, err)
}

func TestCACertPool_UnreadableFile(t *testing.T) {
	_, err := CACertPool(filepath.Join(t.TempDir(), "nope.pem"))
	require.Error(t, err, "a missing file must error (fail closed)")
}

func TestCACertPool_NoValidCert(t *testing.T) {
	dir := t.TempDir()
	bad := writePEM(t, dir, "bad.pem", "not a certificate")
	_, err := CACertPool(bad)
	require.Error(t, err, "a file with no valid PEM cert must error (fail closed)")
	require.Contains(t, err.Error(), "no valid PEM certificate")
}

func TestCACertPool_Valid(t *testing.T) {
	// A valid self-signed CA cert (PEM). Generated once; any valid CERTIFICATE
	// block satisfies AppendCertsFromPEM.
	const caPEM = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----
`
	dir := t.TempDir()
	good := writePEM(t, dir, "ca.pem", caPEM)
	pool, err := CACertPool(good)
	require.NoError(t, err)
	require.NotNil(t, pool)
}

func TestOptionalCACertPool_EmptyPathIsSystemRoots(t *testing.T) {
	pool, err := OptionalCACertPool("")
	require.NoError(t, err, "an empty path is not an error — it means 'use system roots'")
	require.Nil(t, pool, "an empty path yields a nil pool (system roots)")
}

func TestOptionalCACertPool_BadPathFailsClosed(t *testing.T) {
	_, err := OptionalCACertPool(filepath.Join(t.TempDir(), "nope.pem"))
	require.Error(t, err, "a non-empty but unreadable path must still fail closed")
}

func TestOptionalCACertPool_ValidPathLoads(t *testing.T) {
	dir := t.TempDir()
	good := writePEM(t, dir, "ca.pem", `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----
`)
	pool, err := OptionalCACertPool(good)
	require.NoError(t, err)
	require.NotNil(t, pool)
}

func TestTLSClientConfig_PoolAndSkip(t *testing.T) {
	pool := x509.NewCertPool()

	cfg := TLSClientConfig(pool, true)
	require.NotNil(t, cfg)
	require.True(t, cfg.InsecureSkipVerify)
	require.Same(t, pool, cfg.RootCAs, "the supplied pool must be used as RootCAs")
	require.Empty(t, cfg.Certificates)

	// nil pool → system roots (RootCAs nil), skip off.
	cfg = TLSClientConfig(nil, false)
	require.False(t, cfg.InsecureSkipVerify)
	require.Nil(t, cfg.RootCAs)
}

func TestAppendClientCert_Valid(t *testing.T) {
	// Reuse the CA fixture from the certificate helper suite: any valid key pair
	// is fine — here we generate one via createTestKeyPair.
	certFile, keyFile := writeTestKeyPair(t)

	cfg := TLSClientConfig(nil, false)
	require.NoError(t, AppendClientCert(cfg, certFile, keyFile))
	require.Len(t, cfg.Certificates, 1, "client cert must be appended for mTLS")
}

func TestAppendClientCert_BadPairFailsClosed(t *testing.T) {
	cfg := TLSClientConfig(nil, false)
	err := AppendClientCert(cfg, filepath.Join(t.TempDir(), "nope.crt"), filepath.Join(t.TempDir(), "nope.key"))
	require.Error(t, err, "a missing/unreadable cert pair must error, not be silently ignored")
	require.Empty(t, cfg.Certificates)
}
