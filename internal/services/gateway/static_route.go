package gateway

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// replaceRouteFenceObj returns obj with routeName's `fence` key replaced by
// fragment's top-level `fence` value. obj is the metadata-stripped gateway CR
// (migplan.Result.GatewayYAML, parsed); fragment is migplan's
// BuildFenceFragment() output — a small {fence: {scope, errorCode}} block, not
// a whole CR. Mirrors replaceRouteRulesObj's shape for the static-route
// strategy's own artifact (see reconcile/staticartifacts.go's
// BuildFenceFragment doc comment: splicing was deferred to this integration
// phase). obj is mutated in place.
func replaceRouteFenceObj(obj map[string]any, routeName string, fragment []byte) ([]byte, error) {
	var patch map[string]any
	if err := yaml.Unmarshal(fragment, &patch); err != nil {
		return nil, fmt.Errorf("parsing fence fragment: %w", err)
	}
	fence, ok := patch["fence"]
	if !ok {
		return nil, fmt.Errorf("fence fragment has no top-level fence key")
	}
	if err := replaceRouteField(obj, routeName, "fence", fence); err != nil {
		return nil, err
	}
	patched, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshalling fenced gateway CR: %w", err)
	}
	return patched, nil
}

// replaceRouteObj returns obj with the route element named routeName replaced
// by artifact's top-level `route` value — a whole {route: …} document, the
// shape migplan emits for a static route's switchover and rollback (both
// remove the fence, which a single-key splice cannot). obj is the
// metadata-stripped gateway CR, parsed; it is mutated in place.
func replaceRouteObj(obj map[string]any, routeName string, artifact []byte) ([]byte, error) {
	route, err := FragmentValue(artifact, "route")
	if err != nil {
		return nil, err
	}
	spec, ok := mapField(obj, "spec")
	if !ok {
		return nil, fmt.Errorf("base gateway CR has no spec")
	}
	routes, ok := sliceField(spec, "routes")
	if !ok {
		return nil, fmt.Errorf("base gateway CR has no spec.routes")
	}
	idx, err := routeIndex(routes, routeName)
	if err != nil {
		return nil, err
	}
	routes[idx] = route
	patched, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshalling gateway CR: %w", err)
	}
	return patched, nil
}

// replaceRouteField finds routeName in obj's spec.routes and sets key to
// value, mutating obj in place — the same route lookup replaceRouteRulesObj
// does, generalized to an arbitrary key for replaceRouteFenceObj's single-key
// splice.
func replaceRouteField(obj map[string]any, routeName, key string, value any) error {
	spec, ok := mapField(obj, "spec")
	if !ok {
		return fmt.Errorf("base gateway CR has no spec")
	}
	routes, ok := sliceField(spec, "routes")
	if !ok {
		return fmt.Errorf("base gateway CR has no spec.routes")
	}
	for _, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := stringField(route, "name"); name != routeName {
			continue
		}
		route[key] = value
		return nil
	}
	return fmt.Errorf("route %q not found in the base gateway CR's spec.routes", routeName)
}
