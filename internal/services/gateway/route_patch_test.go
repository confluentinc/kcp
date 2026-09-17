package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFragmentValue(t *testing.T) {
	v, err := FragmentValue([]byte("fence:\n  scope: all\n  errorCode: 42\n"), "fence")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"scope": "all", "errorCode": uint64(42)}, v)

	_, err = FragmentValue([]byte("streamingDomain: cp-b\n"), "fence")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no top-level fence key")
}

func TestRouteObject(t *testing.T) {
	cr := []byte(`
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  routes:
    - name: other
      streamingDomain: cp-x
    - name: migration-route
      streamingDomain: cp-a
`)
	route, err := RouteObject(cr, "migration-route")
	require.NoError(t, err)
	assert.Equal(t, "cp-a", route["streamingDomain"])

	_, err = RouteObject(cr, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `route "missing" not found`)
}
