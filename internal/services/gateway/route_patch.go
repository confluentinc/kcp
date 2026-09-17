package gateway

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// FragmentValue parses a single-key migplan fragment ({fence: ...},
// {streamingDomain: ...}, {rules: ...}) and returns the value at key — the
// fragment-extraction half of the old ReplaceRoute*Obj splicers, now that the
// graft-onto-CR half is a JSON Patch built by the gateway service.
func FragmentValue(fragment []byte, key string) (any, error) {
	var m map[string]any
	if err := yaml.Unmarshal(fragment, &m); err != nil {
		return nil, fmt.Errorf("parsing gateway fragment: %w", err)
	}
	v, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("gateway fragment has no top-level %s key", key)
	}
	return v, nil
}

// RouteObject parses the captured gateway CR (config.GatewayYAML) and returns
// the route element named routeName from spec.routes. Unfence uses it to
// restore the route to its captured state via a whole-route replace.
func RouteObject(gatewayYAML []byte, routeName string) (map[string]any, error) {
	var obj map[string]any
	if err := yaml.Unmarshal(gatewayYAML, &obj); err != nil {
		return nil, fmt.Errorf("parsing gateway CR: %w", err)
	}
	spec, ok := mapField(obj, "spec")
	if !ok {
		return nil, fmt.Errorf("gateway CR has no spec")
	}
	routes, ok := sliceField(spec, "routes")
	if !ok {
		return nil, fmt.Errorf("gateway CR has no spec.routes")
	}
	for _, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := stringField(route, "name"); name == routeName {
			return route, nil
		}
	}
	return nil, fmt.Errorf("route %q not found in the gateway CR's spec.routes", routeName)
}
