//go:build e2e

package hotreload

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// mustReadFile reads a rendered transition CR whose path setup.sh put in the
// environment. The CRs are read from disk rather than rebuilt here so the suite
// applies exactly the manifests the cluster was set up with.
func mustReadFile(t *testing.T, envKey string) []byte {
	t.Helper()

	path := os.Getenv(envKey)
	require.NotEmpty(t, path, "%s must be set; run setup.sh first", envKey)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s from %s", envKey, path)
	return data
}
