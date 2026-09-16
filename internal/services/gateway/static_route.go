package gateway

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// ReplaceRouteFenceObj returns obj with routeName's `fence` key replaced by
// fragment's top-level `fence` value. obj is the metadata-stripped gateway CR
// (migplan.Result.GatewayYAML, parsed); fragment is migplan's
// BuildFenceFragment() output — a small {fence: {scope, errorCode}} block, not
// a whole CR. Mirrors ReplaceRouteRulesObj's shape for the static-route
// strategy's own artifact (see reconcile/staticartifacts.go's
// BuildFenceFragment doc comment: splicing was deferred to this integration
// phase). obj is mutated in place.
func ReplaceRouteFenceObj(obj map[string]any, routeName string, fragment []byte) ([]byte, error) {
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

// ReplaceRouteStreamingDomainObj returns obj with routeName's
// `streamingDomain` key replaced by fragment's top-level `streamingDomain`
// value — the static-route switchover counterpart to ReplaceRouteFenceObj.
// fragment is migplan's BuildSwitchoverFragment() output.
func ReplaceRouteStreamingDomainObj(obj map[string]any, routeName string, fragment []byte) ([]byte, error) {
	var patch map[string]any
	if err := yaml.Unmarshal(fragment, &patch); err != nil {
		return nil, fmt.Errorf("parsing streamingDomain fragment: %w", err)
	}
	domain, ok := patch["streamingDomain"]
	if !ok {
		return nil, fmt.Errorf("switchover fragment has no top-level streamingDomain key")
	}
	if err := replaceRouteField(obj, routeName, "streamingDomain", domain); err != nil {
		return nil, err
	}
	patched, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshalling switched gateway CR: %w", err)
	}
	return patched, nil
}

// replaceRouteField finds routeName in obj's spec.routes and sets key to
// value, mutating obj in place. Shared by both splice helpers above — same
// route lookup ReplaceRouteRulesObj already does, generalized to an arbitrary
// key since fence/streamingDomain each replace a different single key rather
// than the rules subtree.
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
