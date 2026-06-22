package targets

import (
	"os"
	"path/filepath"
	"testing"

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
