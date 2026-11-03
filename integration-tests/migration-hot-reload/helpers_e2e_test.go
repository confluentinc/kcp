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

// routeObject returns the route named routeName from a rendered gateway CR's
// spec.routes.
func routeObject(t *testing.T, cr []byte, routeName string) map[string]any {
	t.Helper()
	var obj struct {
		Spec struct {
			Routes []map[string]any `yaml:"routes"`
		} `yaml:"spec"`
	}
	require.NoError(t, yaml.Unmarshal(cr, &obj), "parsing the rendered gateway CR")
	for _, route := range obj.Spec.Routes {
		if route["name"] == routeName {
			return route
		}
	}
	require.Failf(t, "route not found", "route %q is not in the rendered gateway CR's spec.routes", routeName)
	return nil
}
