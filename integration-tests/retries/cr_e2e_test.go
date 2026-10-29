//go:build e2e

package retries_e2e

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

// findRoute returns the named route's object from a gateway CR (nil if absent or the
// CR doesn't parse).
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

// routeFences reports whether the dynamic route r fences topic: a blocked
// rules.fencing[] entry names it.
func routeFences(r map[string]any, topic string) bool {
	if r == nil {
		return false
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

// routeTargets reports whether the dynamic route r routes topic to destDomain.
func routeTargets(r map[string]any, destDomain, topic string) bool {
	if r == nil {
		return false
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
