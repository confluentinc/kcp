package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func routesFixture() []any {
	return []any{
		map[string]any{"name": "other-route"},
		map[string]any{"name": "migration-route", "streamingDomain": "cp-a"},
	}
}

func TestRouteIndex(t *testing.T) {
	routes := routesFixture()

	idx, err := routeIndex(routes, "migration-route")
	require.NoError(t, err)
	assert.Equal(t, 1, idx)

	_, err = routeIndex(routes, "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `route "nope" not found`)
}

func TestBuildRoutePatchOps_SetField(t *testing.T) {
	ops, err := buildRoutePatchOps(routesFixture(), RoutePatch{
		RouteName: "migration-route",
		Field:     "fence",
		Value:     map[string]any{"scope": "all"},
	}, "kcp-abc123")
	require.NoError(t, err)

	require.Len(t, ops, 3)
	assert.Equal(t, jsonPatchOp{Op: "test", Path: "/spec/routes/1/name", Value: "migration-route"}, ops[0])
	assert.Equal(t, jsonPatchOp{Op: "add", Path: "/spec/routes/1/fence", Value: map[string]any{"scope": "all"}}, ops[1])
	assert.Equal(t, jsonPatchOp{Op: "add", Path: "/spec/configId", Value: "kcp-abc123"}, ops[2])
}

func TestBuildRoutePatchOps_WholeRouteReplace(t *testing.T) {
	route := map[string]any{"name": "migration-route", "streamingDomain": "cp-a"}
	ops, err := buildRoutePatchOps(routesFixture(), RoutePatch{
		RouteName: "migration-route",
		Value:     route, // Field "" ⇒ whole-route replace
	}, "")
	require.NoError(t, err)

	require.Len(t, ops, 2) // no configId op when configID == ""
	assert.Equal(t, jsonPatchOp{Op: "test", Path: "/spec/routes/1/name", Value: "migration-route"}, ops[0])
	assert.Equal(t, jsonPatchOp{Op: "replace", Path: "/spec/routes/1", Value: route}, ops[1])
}

func TestBuildRoutePatchOps_RejectsBadConfigID(t *testing.T) {
	_, err := buildRoutePatchOps(routesFixture(), RoutePatch{RouteName: "migration-route", Field: "fence", Value: 1}, "bad id with spaces")
	require.Error(t, err)
}
