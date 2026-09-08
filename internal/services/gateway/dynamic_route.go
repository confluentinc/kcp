package gateway

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// ReplaceRouteRules returns baseCRBytes with routeName's entire `rules:`
// subtree replaced by rulesYAML's `rules:` value. The base is the
// metadata-stripped gateway CR; the result is the fenced (or switched) CR that
// Fence (or, later, Switch) applies.
//
// Unlike FenceRoutes (which injects a fence block onto a static route),
// dynamic routes fence by rules replacement: migplan's reconciliation engine
// already computes the complete replacement rules subtree — see
// reconcile.RulesTree.Serialize()'s doc comment — so the caller here only
// needs to graft it onto the right route. The same function serves both
// fence (fed FenceYAML) and switch/unfence (fed SwitchoverYAML).
//
// The CR tree is walked as plain map[string]any with type assertions, never
// through unstructured.Nested*: those deep-copy via runtime.DeepCopyJSONValue,
// which panics on the uint64 goccy/go-yaml produces for a positive integer
// like nodeIdRanges.start (see mapField's comment in validate.go).
func ReplaceRouteRules(baseCRBytes []byte, routeName string, rulesYAML []byte) ([]byte, error) {
	var obj map[string]any
	if err := yaml.Unmarshal(baseCRBytes, &obj); err != nil {
		return nil, fmt.Errorf("parsing base gateway CR: %w", err)
	}
	return ReplaceRouteRulesObj(obj, routeName, rulesYAML)
}

// ReplaceRouteRulesObj is ReplaceRouteRules for a base CR the caller has
// already parsed into a plain map[string]any, sparing a redundant
// parse/marshal round trip when the caller already holds the tree. obj is
// mutated in place with the replaced rules subtree.
func ReplaceRouteRulesObj(obj map[string]any, routeName string, rulesYAML []byte) ([]byte, error) {
	var patch map[string]any
	if err := yaml.Unmarshal(rulesYAML, &patch); err != nil {
		return nil, fmt.Errorf("parsing rules YAML: %w", err)
	}
	rules, ok := patch["rules"]
	if !ok {
		return nil, fmt.Errorf("rules YAML has no top-level rules key")
	}

	spec, ok := mapField(obj, "spec")
	if !ok {
		return nil, fmt.Errorf("base gateway CR has no spec")
	}
	routes, ok := sliceField(spec, "routes")
	if !ok {
		return nil, fmt.Errorf("base gateway CR has no spec.routes")
	}

	found := false
	for _, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := stringField(route, "name"); name != routeName {
			continue
		}
		found = true
		// Whole-subtree replacement, not a merge: rulesYAML is the operator's
		// complete rules tree with kcp's mutation already applied (see
		// reconcile.RulesTree.Serialize), so a stale field from the route's
		// previous rules must not survive.
		route["rules"] = rules
	}
	if !found {
		return nil, fmt.Errorf("route %q not found in the base gateway CR's spec.routes", routeName)
	}

	patched, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshalling patched gateway CR: %w", err)
	}
	return patched, nil
}
