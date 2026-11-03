//go:build e2e

// Pure gateway-CR inspection used by the suite's helpers.
package tracing_e2e

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

// findRoute returns the named route's object from a gateway CR (nil if absent or
// the CR doesn't parse).
func findRoute(cr []byte, route string) map[string]any {
	var obj map[string]any
	if err := yaml.Unmarshal(cr, &obj); err != nil {
		return nil
	}
	spec, _ := obj["spec"].(map[string]any)
	routes, _ := spec["routes"].([]any)
	for _, r := range routes {
		rm, _ := r.(map[string]any)
		if rm["name"] == route {
			return rm
		}
	}
	return nil
}

// stripServerFields removes the server-managed metadata a fetched CR carries.
// Mirrors the migration workflow's cleanInitialCR; duplicated rather than
// exported because widening kcp's public surface for a test is the wrong trade.
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
