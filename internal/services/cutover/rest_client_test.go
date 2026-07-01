package cutover

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A valid self-signed CA cert (PEM) — any CERTIFICATE block satisfies AppendCertsFromPEM.
const testCAPEM = `-----BEGIN CERTIFICATE-----
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

func tlsConfigOf(t *testing.T, c *http.Client) (rootCAsSet, insecureSkip bool) {
	t.Helper()
	tr, ok := c.Transport.(*http.Transport)
	require.True(t, ok, "expected *http.Transport")
	require.NotNil(t, tr.TLSClientConfig, "expected a TLS config")
	return tr.TLSClientConfig.RootCAs != nil, tr.TLSClientConfig.InsecureSkipVerify
}

func TestNewRESTHTTPClient_DefaultWhenNoTLSOptions(t *testing.T) {
	c, err := NewRESTHTTPClient("", false)
	require.NoError(t, err)
	require.Same(t, http.DefaultClient, c, "no CA + no skip → system-roots default client (CC public-CA case)")
}

func TestNewRESTHTTPClient_SkipVerify(t *testing.T) {
	c, err := NewRESTHTTPClient("", true)
	require.NoError(t, err)
	rootCAs, skip := tlsConfigOf(t, c)
	require.True(t, skip, "insecureSkip must set InsecureSkipVerify")
	require.False(t, rootCAs, "no CA supplied → no custom RootCAs")
}

func TestNewRESTHTTPClient_CustomCA(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(ca, []byte(testCAPEM), 0600))

	c, err := NewRESTHTTPClient(ca, false)
	require.NoError(t, err)
	rootCAs, skip := tlsConfigOf(t, c)
	require.True(t, rootCAs, "custom CA must be loaded into RootCAs")
	require.False(t, skip, "CA without skip must keep verification on")
}

func TestNewRESTHTTPClient_BadCAFailsClosed(t *testing.T) {
	_, err := NewRESTHTTPClient(filepath.Join(t.TempDir(), "nope.pem"), false)
	require.Error(t, err, "an unreadable CA must fail closed, not silently fall back")
}
