package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
