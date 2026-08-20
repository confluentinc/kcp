package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseRouteCR is a minimal live-read gateway CR with two named routes. The
// nodeIdRanges integers are the goccy uint64 hazard (see mapField's comment):
// FenceRoutes must round-trip them without a DeepCopyJSONValue panic.
const baseRouteCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  streamingDomains:
    - name: source-kafka-cluster
      type: kafka
      kafkaCluster:
        nodeIdRanges:
          - name: pool-1
            start: 1
            end: 3
  routes:
    - name: migration-route
      endpoint: gateway:9595
      security:
        auth: passthrough
    - name: scram-preregistration
      endpoint: gateway:9599
      security:
        auth: passthrough
`

// routeFenceBlock returns the fence block on the named route, or nil if the
// route has no fence (or does not exist). It re-parses the marshalled output so
// the assertion is against what a subsequent apply would actually see.
func routeFenceBlock(t *testing.T, crBytes []byte, routeName string) map[string]any {
	t.Helper()
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crBytes, &obj))
	spec, ok := mapField(obj, "spec")
	require.True(t, ok, "spec missing from patched CR")
	routes, ok := sliceField(spec, "routes")
	require.True(t, ok, "spec.routes missing from patched CR")
	for _, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := stringField(route, "name"); name == routeName {
			fence, _ := mapField(route, "fence")
			return fence
		}
	}
	return nil
}

func TestFenceRoutes_InjectsFenceOnNamedRoute(t *testing.T) {
	patched, err := FenceRoutes([]byte(baseRouteCR), []string{"migration-route"})
	require.NoError(t, err)

	fence := routeFenceBlock(t, patched, "migration-route")
	require.NotNil(t, fence, "the named route should carry a fence block after patching")
	assert.Equal(t, "ALL", fence["scope"])
	assert.Equal(t, "BROKER_NOT_AVAILABLE", fence["errorCode"])
}

// TestFenceRoutes_ErrorsWhenRouteNotFound — a name that matches no route is a
// hard error at fence time, not a silent no-op that promotes topics behind a
// fence that was never applied. The single user-supplied name is echoed (it is
// the operator's own input, not a discovered resource list).
func TestFenceRoutes_ErrorsWhenRouteNotFound(t *testing.T) {
	_, err := FenceRoutes([]byte(baseRouteCR), []string{"does-not-exist"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist")
}

// TestFenceRoutes_ErrorsWhenRouteAlreadyFenced — the fence is purely additive,
// so a route the base CR already fences cannot be fenced again; that state means
// the base is not the clean initial snapshot this function assumes.
func TestFenceRoutes_ErrorsWhenRouteAlreadyFenced(t *testing.T) {
	alreadyFenced := strings.Replace(baseRouteCR,
		"    - name: migration-route\n      endpoint: gateway:9595\n",
		"    - name: migration-route\n      endpoint: gateway:9595\n      fence:\n        scope: ALL\n        errorCode: BROKER_NOT_AVAILABLE\n",
		1)
	_, err := FenceRoutes([]byte(alreadyFenced), []string{"migration-route"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-route")
}

// TestFenceRoutes_ErrorsOnEmptyRouteNames — fencing nothing would apply the
// bare initial CR and report the gateway "fenced" while every client keeps
// flowing. Upstream validation already rejects an empty spec.gateway.fence.routes;
// this is defence in depth at the patcher itself.
func TestFenceRoutes_ErrorsOnEmptyRouteNames(t *testing.T) {
	_, err := FenceRoutes([]byte(baseRouteCR), nil)
	require.Error(t, err)
}

// TestFenceRoutes_FencesOnlyNamedRoutes — a gateway legitimately keeps one
// route serving while fencing another (the SCRAM pre-registration route in the
// worked examples stays available), so only the named routes gain a fence.
func TestFenceRoutes_FencesOnlyNamedRoutes(t *testing.T) {
	patched, err := FenceRoutes([]byte(baseRouteCR), []string{"migration-route"})
	require.NoError(t, err)

	assert.NotNil(t, routeFenceBlock(t, patched, "migration-route"), "the named route is fenced")
	assert.Nil(t, routeFenceBlock(t, patched, "scram-preregistration"), "an unnamed route stays unfenced")
}

// TestFenceRoutes_FencesMultipleRoutes — every named route is fenced in one call.
func TestFenceRoutes_FencesMultipleRoutes(t *testing.T) {
	patched, err := FenceRoutes([]byte(baseRouteCR), []string{"migration-route", "scram-preregistration"})
	require.NoError(t, err)

	assert.NotNil(t, routeFenceBlock(t, patched, "migration-route"))
	assert.NotNil(t, routeFenceBlock(t, patched, "scram-preregistration"))
}

// TestFenceRoutes_PreservesUint64NodeIdRanges is the goccy-uint64 regression the
// plan calls out: goccy parses a positive integer like nodeIdRanges.start as
// uint64, and any walk that reaches for unstructured.Nested* would panic in
// runtime.DeepCopyJSONValue. This test running to completion is the proof no
// such walk was introduced; the value assertions guard the round-trip too.
func TestFenceRoutes_PreservesUint64NodeIdRanges(t *testing.T) {
	patched, err := FenceRoutes([]byte(baseRouteCR), []string{"migration-route"})
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(patched, &obj))
	spec, _ := mapField(obj, "spec")
	domains, ok := sliceField(spec, "streamingDomains")
	require.True(t, ok)
	domain := domains[0].(map[string]any)
	cluster, _ := mapField(domain, "kafkaCluster")
	ranges, ok := sliceField(cluster, "nodeIdRanges")
	require.True(t, ok)
	r := ranges[0].(map[string]any)
	assert.EqualValues(t, 1, r["start"])
	assert.EqualValues(t, 3, r["end"])
}

// TestFenceRoutesObj_MatchesFenceRoutes proves FenceRoutesObj (the
// already-parsed-tree entry point workflow.FenceGateway uses to avoid a
// redundant parse) produces the same result as FenceRoutes given the same
// input, just pre-parsed.
func TestFenceRoutesObj_MatchesFenceRoutes(t *testing.T) {
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(baseRouteCR), &obj))

	patched, err := FenceRoutesObj(obj, []string{"migration-route"})
	require.NoError(t, err)

	fence := routeFenceBlock(t, patched, "migration-route")
	require.NotNil(t, fence, "the named route should carry a fence block after patching")
	assert.Equal(t, "ALL", fence["scope"])
	assert.Equal(t, "BROKER_NOT_AVAILABLE", fence["errorCode"])
}

// TestFenceRoutes_FenceIsTheOnlyDelta proves the design's central claim: the
// patched CR equals the base with nothing changed but the injected fence block.
// This is what makes fence and unfence exact inverses — unfence re-applies the
// same base and SSA prunes the one field this function added. Stripping the
// fence back off the patched tree must reproduce the base tree exactly.
func TestFenceRoutes_FenceIsTheOnlyDelta(t *testing.T) {
	patched, err := FenceRoutes([]byte(baseRouteCR), []string{"migration-route"})
	require.NoError(t, err)

	var baseTree, patchedTree map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(baseRouteCR), &baseTree))
	require.NoError(t, yaml.Unmarshal(patched, &patchedTree))

	// Remove the one field FenceRoutes added; what remains must equal the base.
	spec, _ := mapField(patchedTree, "spec")
	routes, _ := sliceField(spec, "routes")
	for _, raw := range routes {
		route := raw.(map[string]any)
		if name, _ := stringField(route, "name"); name == "migration-route" {
			delete(route, "fence")
		}
	}
	assert.True(t, reflect.DeepEqual(baseTree, patchedTree),
		"after removing the injected fence, the patched CR must equal the base CR byte-for-byte in structure")
}
