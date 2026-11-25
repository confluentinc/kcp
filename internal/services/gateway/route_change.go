package gateway

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// RouteEdit says which part of a route a reconcile artifact replaces.
type RouteEdit int

const (
	// EditRouteFence sets the artifact's "fence" fragment on route.fence
	// (a static route's fence).
	EditRouteFence RouteEdit = iota
	// EditRouteRules sets the artifact's "rules" fragment on route.rules
	// (a dynamic route's rules, fence included).
	EditRouteRules
	// EditRoute replaces the whole route with the artifact's "route" fragment.
	EditRoute
)

// RouteChange is one edit to a gateway route: which part of the route changes
// and the reconcile artifact it comes from. It owns the CR field names and the
// fragment keys, so the FSMs only choose the edit and hand over the artifact.
// The same change yields the RoutePatch a write applies (Patch) and the whole
// CR a capability probe inspects (ApplyTo).
type RouteChange struct {
	Edit     RouteEdit
	Artifact []byte
}

// Patch builds the RoutePatch for routeName. The whole-route edit has an empty
// Field, which the gateway service reads as "replace the route".
func (c RouteChange) Patch(routeName string) (RoutePatch, error) {
	var field, key string
	switch c.Edit {
	case EditRouteFence:
		field, key = "fence", "fence"
	case EditRouteRules:
		field, key = "rules", "rules"
	case EditRoute:
		field, key = "", "route"
	default:
		return RoutePatch{}, fmt.Errorf("unknown route edit %d", c.Edit)
	}
	v, err := FragmentValue(c.Artifact, key)
	if err != nil {
		return RoutePatch{}, err
	}
	return RoutePatch{RouteName: routeName, Field: field, Value: v}, nil
}

// ApplyTo returns crYAML with the change applied to routeName.
func (c RouteChange) ApplyTo(crYAML []byte, routeName string) ([]byte, error) {
	var base map[string]any
	if err := yaml.Unmarshal(crYAML, &base); err != nil {
		return nil, fmt.Errorf("failed to parse gateway CR YAML: %w", err)
	}
	switch c.Edit {
	case EditRouteFence:
		return replaceRouteFenceObj(base, routeName, c.Artifact)
	case EditRouteRules:
		return replaceRouteRulesObj(base, routeName, c.Artifact)
	case EditRoute:
		return replaceRouteObj(base, routeName, c.Artifact)
	default:
		return nil, fmt.Errorf("unknown route edit %d", c.Edit)
	}
}

// RouteProbe builds the fenced and the switched CR capability detection
// inspects, by applying each change to routeName in crYAML.
func RouteProbe(crYAML []byte, routeName string, fenced, switched RouteChange) (fencedCR, switchedCR []byte, err error) {
	f, err := fenced.ApplyTo(crYAML, routeName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to derive fenced gateway CR: %w", err)
	}
	s, err := switched.ApplyTo(crYAML, routeName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to derive switched gateway CR: %w", err)
	}
	return f, s, nil
}
