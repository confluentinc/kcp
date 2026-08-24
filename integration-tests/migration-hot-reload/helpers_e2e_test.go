//go:build e2e

package hotreload

import (
	"os"
	"testing"

	"github.com/goccy/go-yaml"
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

// stripServerFields removes the server-managed metadata a CR fetched from the
// cluster carries, which server-side apply rejects. It mirrors the migration
// workflow's cleanInitialCR; duplicated rather than exported because widening
// kcp's public surface for a test is the wrong trade.
func stripServerFields(t *testing.T, crYAML []byte) []byte {
	t.Helper()

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crYAML, &obj))

	delete(obj, "status")
	if md, ok := obj["metadata"].(map[string]any); ok {
		for _, k := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp", "generation", "selfLink"} {
			delete(md, k)
		}
	}

	out, err := yaml.Marshal(obj)
	require.NoError(t, err)
	return out
}
