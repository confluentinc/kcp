package gateway

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// switchRouteDomain returns the route's streamingDomain block, or nil if the
// route has none (or does not exist). Re-parses the marshalled output so the
// assertion is against what a subsequent apply would actually see.
func switchRouteDomain(t *testing.T, crBytes []byte, routeName string) map[string]any {
	t.Helper()
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crBytes, &obj))
	spec, ok := mapField(obj, "spec")
	require.True(t, ok, "spec missing from switched CR")
	routes, ok := sliceField(spec, "routes")
	require.True(t, ok, "spec.routes missing from switched CR")
	for _, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := stringField(route, "name"); name == routeName {
			domain, _ := mapField(route, "streamingDomain")
			return domain
		}
	}
	return nil
}

func TestSwitchRoutes_FlipsStreamingDomainOnNamedRoute(t *testing.T) {
	patched, err := SwitchRoutes([]byte(baseRouteCR), []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "confluent-cloud", BootstrapServerId: "SASL_PLAIN"},
	})
	require.NoError(t, err)

	domain := switchRouteDomain(t, patched, "migration-route")
	require.NotNil(t, domain, "the named route should carry a streamingDomain after switching")
	assert.Equal(t, "confluent-cloud", domain["name"])
	assert.Equal(t, "SASL_PLAIN", domain["bootstrapServerId"])
}

// TestSwitchRoutes_OverwritesExistingStreamingDomain proves the flip REPLACES
// a route's current binding rather than merging into it — the whole point is
// moving the route from its source domain to the target.
func TestSwitchRoutes_OverwritesExistingStreamingDomain(t *testing.T) {
	withSourceDomain := `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
spec:
  routes:
    - name: migration-route
      endpoint: gateway:9595
      streamingDomain:
        name: source-kafka-cluster
        bootstrapServerId: SCRAM
`
	patched, err := SwitchRoutes([]byte(withSourceDomain), []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "confluent-cloud", BootstrapServerId: "SASL_PLAIN"},
	})
	require.NoError(t, err)

	domain := switchRouteDomain(t, patched, "migration-route")
	require.NotNil(t, domain)
	assert.Equal(t, "confluent-cloud", domain["name"])
	assert.Equal(t, "SASL_PLAIN", domain["bootstrapServerId"])
}

// TestSwitchRoutes_FlipsOnlyNamedRoutes — a route not named in the target list
// keeps whatever streamingDomain it already had.
func TestSwitchRoutes_FlipsOnlyNamedRoutes(t *testing.T) {
	patched, err := SwitchRoutes([]byte(baseRouteCR), []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "confluent-cloud", BootstrapServerId: "SASL_PLAIN"},
	})
	require.NoError(t, err)

	assert.NotNil(t, switchRouteDomain(t, patched, "migration-route"))
	assert.Nil(t, switchRouteDomain(t, patched, "scram-preregistration"), "an unnamed route is untouched")
}

// TestSwitchRoutes_MultipleRoutesDifferentTargets — each route in the same CR
// may switch to a DIFFERENT target domain in one call (the multi-route,
// multi-target case the plan calls out).
func TestSwitchRoutes_MultipleRoutesDifferentTargets(t *testing.T) {
	patched, err := SwitchRoutes([]byte(baseRouteCR), []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "confluent-cloud", BootstrapServerId: "SASL_PLAIN"},
		{RouteName: "scram-preregistration", StreamingDomainName: "confluent-cloud-2", BootstrapServerId: "SCRAM"},
	})
	require.NoError(t, err)

	a := switchRouteDomain(t, patched, "migration-route")
	require.NotNil(t, a)
	assert.Equal(t, "confluent-cloud", a["name"])

	b := switchRouteDomain(t, patched, "scram-preregistration")
	require.NotNil(t, b)
	assert.Equal(t, "confluent-cloud-2", b["name"])
}

// TestSwitchRoutes_ErrorsWhenRouteNotFound — a target naming a route absent
// from the CR is a hard error, not a silent no-op that reports a completed
// switch while the named route never moved.
func TestSwitchRoutes_ErrorsWhenRouteNotFound(t *testing.T) {
	_, err := SwitchRoutes([]byte(baseRouteCR), []RouteSwitchoverTarget{
		{RouteName: "does-not-exist", StreamingDomainName: "confluent-cloud", BootstrapServerId: "SASL_PLAIN"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist")
}

// TestSwitchRoutes_ErrorsOnEmptyTargets — switching nothing would apply the
// bare initial CR and report success while every fenced route stays on its
// source domain.
func TestSwitchRoutes_ErrorsOnEmptyTargets(t *testing.T) {
	_, err := SwitchRoutes([]byte(baseRouteCR), nil)
	require.Error(t, err)
}

// TestSwitchRoutes_PreservesUint64NodeIdRanges is the same goccy-uint64
// regression fence_test.go guards: any walk that reached for
// unstructured.Nested* would panic in runtime.DeepCopyJSONValue.
func TestSwitchRoutes_PreservesUint64NodeIdRanges(t *testing.T) {
	patched, err := SwitchRoutes([]byte(baseRouteCR), []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "confluent-cloud", BootstrapServerId: "SASL_PLAIN"},
	})
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

// TestSwitchRoutesObj_MatchesSwitchRoutes proves SwitchRoutesObj (the
// already-parsed-tree entry point workflow.SwitchGateway uses to avoid a
// redundant parse) produces the same result as SwitchRoutes given the same
// input, just pre-parsed.
func TestSwitchRoutesObj_MatchesSwitchRoutes(t *testing.T) {
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(baseRouteCR), &obj))

	patched, err := SwitchRoutesObj(obj, []RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "confluent-cloud", BootstrapServerId: "SASL_PLAIN"},
	})
	require.NoError(t, err)

	domain := switchRouteDomain(t, patched, "migration-route")
	require.NotNil(t, domain)
	assert.Equal(t, "confluent-cloud", domain["name"])
	assert.Equal(t, "SASL_PLAIN", domain["bootstrapServerId"])
}
