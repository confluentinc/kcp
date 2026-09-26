// Pure gateway-CR inspection used by the live suite's assertions. Deliberately
// untagged (unlike the rest of the package) so crinspect_unit_test.go can pin its
// static/dynamic branches in `make test-go` without a cluster: a wrong answer here
// would let a live resume assertion pass vacuously.
package idempotent_fsm_e2e

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

// staticBinding returns route r's singular streamingDomain when r is a static
// route: one whose singular streamingDomain is named. CFK's Gateway CRD defaults
// an unnamed singular streamingDomain onto every route, dynamic ones included, so
// an unnamed one is not a static binding (the same rule as migplan's
// resolveModeStructurally).
func staticBinding(r map[string]any) (map[string]any, bool) {
	sd, _ := r["streamingDomain"].(map[string]any)
	name, _ := sd["name"].(string)
	return sd, name != ""
}

// routeFences reports whether route r fences topic. Static: the whole route
// carries a `fence` block. Dynamic: a blocked rules.fencing[] entry names the topic.
func routeFences(r map[string]any, topic string) bool {
	if r == nil {
		return false
	}
	if _, static := staticBinding(r); static {
		_, hasFence := r["fence"]
		return hasFence
	}
	rules, _ := r["rules"].(map[string]any)
	fencing, _ := rules["fencing"].([]any)
	for _, fe := range fencing {
		fm, _ := fe.(map[string]any)
		if blocked, _ := fm["blocked"].(bool); !blocked {
			continue
		}
		if listsTopic(fm, topic) {
			return true
		}
	}
	return false
}

// routeTargets reports whether route r routes topic to destDomain. Static:
// route.streamingDomain.name == destDomain (whole route). Dynamic: a routing
// condition binds the topic to destDomain.
func routeTargets(r map[string]any, destDomain, topic string) bool {
	if r == nil {
		return false
	}
	if sd, static := staticBinding(r); static {
		return sd["name"] == destDomain
	}
	rules, _ := r["rules"].(map[string]any)
	routing, _ := rules["routing"].(map[string]any)
	conditions, _ := routing["conditions"].([]any)
	for _, c := range conditions {
		cm, _ := c.(map[string]any)
		if cm["streamingDomain"] == destDomain && listsTopic(cm, topic) {
			return true
		}
	}
	return false
}

// listsTopic reports whether m's literal `topics` list contains topic.
func listsTopic(m map[string]any, topic string) bool {
	topics, _ := m["topics"].([]any)
	for _, tp := range topics {
		if tp == topic {
			return true
		}
	}
	return false
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
