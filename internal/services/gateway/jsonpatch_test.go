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
	// "fence" is absent on the fixture's migration-route: this is a first-ever
	// write to the field, so there's no current value to guard — only the name
	// test.
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

func TestBuildRoutePatchOps_SetField_GuardsExistingValue(t *testing.T) {
	// "streamingDomain" already has a value on the fixture's migration-route,
	// so the patch must also test that current value before overwriting it —
	// a concurrent edit to the same field must fail the patch, not be
	// silently clobbered.
	ops, err := buildRoutePatchOps(routesFixture(), RoutePatch{
		RouteName: "migration-route",
		Field:     "streamingDomain",
		Value:     "cp-b",
	}, "")
	require.NoError(t, err)

	require.Len(t, ops, 3)
	assert.Equal(t, jsonPatchOp{Op: "test", Path: "/spec/routes/1/name", Value: "migration-route"}, ops[0])
	assert.Equal(t, jsonPatchOp{Op: "test", Path: "/spec/routes/1/streamingDomain", Value: "cp-a"}, ops[1])
	assert.Equal(t, jsonPatchOp{Op: "add", Path: "/spec/routes/1/streamingDomain", Value: "cp-b"}, ops[2])
}

func TestBuildRoutePatchOps_WholeRouteReplace(t *testing.T) {
	route := map[string]any{"name": "migration-route", "streamingDomain": "cp-a"}
	current := routesFixture()[1] // the live route this replace must not clobber
	ops, err := buildRoutePatchOps(routesFixture(), RoutePatch{
		RouteName: "migration-route",
		Value:     route, // Field "" ⇒ whole-route replace
	}, "")
	require.NoError(t, err)

	require.Len(t, ops, 3) // no configId op when configID == ""
	assert.Equal(t, jsonPatchOp{Op: "test", Path: "/spec/routes/1/name", Value: "migration-route"}, ops[0])
	assert.Equal(t, jsonPatchOp{Op: "test", Path: "/spec/routes/1", Value: current}, ops[1])
	assert.Equal(t, jsonPatchOp{Op: "replace", Path: "/spec/routes/1", Value: route}, ops[2])
}

func TestBuildRoutePatchOps_RejectsBadConfigID(t *testing.T) {
	_, err := buildRoutePatchOps(routesFixture(), RoutePatch{RouteName: "migration-route", Field: "fence", Value: 1}, "bad id with spaces")
	require.Error(t, err)
}
