package gateway

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// fenceScope and fenceErrorCode are the fence parameters kcp injects. Every
// worked example in docs/assets/gateway-switchover uses exactly this block, so
// they are hardcoded rather than exposed as config; making them configurable
// later is an additive change.
const (
	fenceScope     = "ALL"
	fenceErrorCode = "BROKER_NOT_AVAILABLE"
)

// FenceRoutes returns baseCRBytes with a fence block injected onto each route
// named in routeNames. The base is the metadata-stripped initial gateway CR;
// the result is the fenced CR that FenceGateway applies. Deriving the fence
// from the same snapshot the rollback re-applies makes fence and unfence exact
// inverses — the fence block is the only delta.
//
// The CR tree is walked as plain map[string]any with type assertions, never
// through unstructured.Nested*: those deep-copy via runtime.DeepCopyJSONValue,
// which panics on the uint64 goccy/go-yaml produces for a positive integer like
// nodeIdRanges.start (see mapField's comment in validate.go).
func FenceRoutes(baseCRBytes []byte, routeNames []string) ([]byte, error) {
	var obj map[string]any
	if err := yaml.Unmarshal(baseCRBytes, &obj); err != nil {
		return nil, fmt.Errorf("parsing base gateway CR: %w", err)
	}
	return FenceRoutesObj(obj, routeNames)
}

// FenceRoutesObj is FenceRoutes for a base CR the caller has already parsed
// into a plain map[string]any (e.g. migration.cleanInitialCR), sparing a
// redundant parse/marshal round trip when the caller already holds the tree.
// obj is mutated in place with the injected fence block(s).
func FenceRoutesObj(obj map[string]any, routeNames []string) ([]byte, error) {
	if len(routeNames) == 0 {
		return nil, fmt.Errorf("no routes named to fence")
	}

	spec, ok := mapField(obj, "spec")
	if !ok {
		return nil, fmt.Errorf("base gateway CR has no spec")
	}
	routes, ok := sliceField(spec, "routes")
	if !ok {
		return nil, fmt.Errorf("base gateway CR has no spec.routes")
	}

	for _, name := range routeNames {
		found := false
		for _, raw := range routes {
			route, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if routeName, _ := stringField(route, "name"); routeName != name {
				continue
			}
			found = true
			// A nulled `fence` (fence: null) counts as unfenced, matching
			// fencedRoutes' semantics — an explicitly nulled field is one being
			// removed, not one being set.
			if existing, has := route["fence"]; has && existing != nil {
				return nil, fmt.Errorf("route %q is already fenced in the base gateway CR", name)
			}
			route["fence"] = map[string]any{
				"scope":     fenceScope,
				"errorCode": fenceErrorCode,
			}
		}
		if !found {
			return nil, fmt.Errorf("route %q not found in the base gateway CR's spec.routes", name)
		}
	}

	patched, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshalling fenced gateway CR: %w", err)
	}
	return patched, nil
}
