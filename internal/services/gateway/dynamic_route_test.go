package gateway

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseRouteCR is a minimal live-read gateway CR with two named routes. The
// nodeIdRanges integers are the goccy uint64 hazard (see mapField's comment):
// route patchers must round-trip them without a DeepCopyJSONValue panic.
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

// routeRules returns the named route's rules subtree, or nil if the route has
// none (or does not exist). It re-parses the marshalled output so the
// assertion is against what a subsequent apply would actually see.
func routeRules(t *testing.T, crBytes []byte, routeName string) map[string]any {
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
			rules, _ := mapField(route, "rules")
			return rules
		}
	}
	return nil
}

func TestReplaceRouteRulesObj_ReplacesNamedRouteRules(t *testing.T) {
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(baseRouteCR), &obj))

	rulesYAML := []byte("rules:\n  routing:\n    default: msk\n  fencing:\n    - topics: [\"t1.order\"]\n")
	patched, err := ReplaceRouteRulesObj(obj, "migration-route", rulesYAML)
	require.NoError(t, err)

	rules := routeRules(t, patched, "migration-route")
	require.NotNil(t, rules, "the named route should carry the replaced rules subtree")
	routing, ok := mapField(rules, "routing")
	require.True(t, ok)
	assert.Equal(t, "msk", routing["default"])
	fencing, ok := sliceField(rules, "fencing")
	require.True(t, ok)
	require.Len(t, fencing, 1)
}

func TestReplaceRouteRulesObj_ErrorsWhenRouteNotFound(t *testing.T) {
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(baseRouteCR), &obj))

	_, err := ReplaceRouteRulesObj(obj, "does-not-exist", []byte("rules:\n  routing: {}\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist")
}

func TestReplaceRouteRulesObj_ErrorsOnMissingRulesKey(t *testing.T) {
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(baseRouteCR), &obj))

	_, err := ReplaceRouteRulesObj(obj, "migration-route", []byte("notRules:\n  foo: bar\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rules")
}

func TestReplaceRouteRulesObj_ReplacesWholeSubtreeNotMerge(t *testing.T) {
	// A second replacement (e.g. fence, then later switch) must fully
	// overwrite the first rules subtree, not merge into it — matching
	// reconcile.RulesTree.Serialize()'s "whole-subtree replacement" contract.
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(baseRouteCR), &obj))

	first, err := ReplaceRouteRulesObj(obj, "migration-route", []byte("rules:\n  routing:\n    default: msk\n    stale: keep-me\n"))
	require.NoError(t, err)

	var firstObj map[string]any
	require.NoError(t, yaml.Unmarshal(first, &firstObj))
	second, err := ReplaceRouteRulesObj(firstObj, "migration-route", []byte("rules:\n  routing:\n    default: cc\n"))
	require.NoError(t, err)

	rules := routeRules(t, second, "migration-route")
	routing, ok := mapField(rules, "routing")
	require.True(t, ok)
	assert.Equal(t, "cc", routing["default"])
	_, hasStale := routing["stale"]
	assert.False(t, hasStale, "the second replacement must fully overwrite the first, not merge into it")
}

func TestReplaceRouteRulesObj_OnlyNamedRouteChanges(t *testing.T) {
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(baseRouteCR), &obj))

	patched, err := ReplaceRouteRulesObj(obj, "migration-route", []byte("rules:\n  routing:\n    default: msk\n"))
	require.NoError(t, err)

	assert.NotNil(t, routeRules(t, patched, "migration-route"))
	assert.Nil(t, routeRules(t, patched, "scram-preregistration"), "an unnamed route's rules must stay untouched")
}

// duplicateNameRouteCR has two spec.routes entries sharing the same name,
// distinguished only by endpoint — a malformed shape ReplaceRouteRulesObj
// must still handle deterministically (see
// TestReplaceRouteRulesObj_OnlyFirstDuplicateNamedRouteChanges).
const duplicateNameRouteCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  routes:
    - name: migration-route
      endpoint: gateway:9595
      security:
        auth: passthrough
    - name: migration-route
      endpoint: gateway:9596
      security:
        auth: passthrough
`

// routeRulesByEndpoint returns the rules subtree of the route matching both
// name and endpoint, distinguishing between two same-named routes.
func routeRulesByEndpoint(t *testing.T, crBytes []byte, routeName, endpoint string) map[string]any {
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
		name, _ := stringField(route, "name")
		ep, _ := stringField(route, "endpoint")
		if name == routeName && ep == endpoint {
			rules, _ := mapField(route, "rules")
			return rules
		}
	}
	return nil
}

// TestReplaceRouteRulesObj_OnlyFirstDuplicateNamedRouteChanges is a
// regression test: the loop used to have no break, so a CR with two
// same-named routes had rules replaced on both, disagreeing with
// replaceRouteField's (static_route.go) single-route, first-match contract
// for the identical CR shape. Only the first occurrence must change now.
func TestReplaceRouteRulesObj_OnlyFirstDuplicateNamedRouteChanges(t *testing.T) {
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(duplicateNameRouteCR), &obj))

	patched, err := ReplaceRouteRulesObj(obj, "migration-route", []byte("rules:\n  routing:\n    default: msk\n"))
	require.NoError(t, err)

	assert.NotNil(t, routeRulesByEndpoint(t, patched, "migration-route", "gateway:9595"),
		"the first name-matching route must be patched")
	assert.Nil(t, routeRulesByEndpoint(t, patched, "migration-route", "gateway:9596"),
		"the second name-matching route must be left untouched")
}

func TestReplaceRouteRules_ParsesBytesAndDelegates(t *testing.T) {
	patched, err := ReplaceRouteRules([]byte(baseRouteCR), "migration-route", []byte("rules:\n  routing:\n    default: msk\n"))
	require.NoError(t, err)

	rules := routeRules(t, patched, "migration-route")
	require.NotNil(t, rules)
}
