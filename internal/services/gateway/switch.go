package gateway

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// RouteSwitchoverTarget names, for one fenced route, the streaming domain and
// bootstrap server the redundant-auth switch flips it to. Because the route's
// security.cluster already carries pre-staged ("redundant") auth for this
// domain (see checkRedundantAuthStaged), the switch changes no secret or auth
// block — only this field flip.
type RouteSwitchoverTarget struct {
	RouteName           string
	StreamingDomainName string
	BootstrapServerId   string
}

// SwitchRoutes returns baseCRBytes with each named route's streamingDomain
// flipped to its switchover target. The base is the metadata-stripped,
// unfenced initial gateway CR; the result is the switched CR that
// SwitchGateway applies. Deriving the switch from the same snapshot fence
// derives from means there is no separate switchover CR to keep in sync with
// the initial one — the only deltas from the initial CR are the flipped
// streamingDomain fields.
//
// The CR tree is walked as plain map[string]any with type assertions, never
// through unstructured.Nested*: those deep-copy via runtime.DeepCopyJSONValue,
// which panics on the uint64 goccy/go-yaml produces for a positive integer
// like nodeIdRanges.start (see mapField's comment in validate.go).
func SwitchRoutes(baseCRBytes []byte, targets []RouteSwitchoverTarget) ([]byte, error) {
	var obj map[string]any
	if err := yaml.Unmarshal(baseCRBytes, &obj); err != nil {
		return nil, fmt.Errorf("parsing base gateway CR: %w", err)
	}
	return SwitchRoutesObj(obj, targets)
}

// SwitchRoutesObj is SwitchRoutes for a base CR the caller has already parsed
// into a plain map[string]any (e.g. migration.cleanInitialCR), sparing a
// redundant parse/marshal round trip when the caller already holds the tree.
// obj is mutated in place with the flipped streamingDomain block(s).
func SwitchRoutesObj(obj map[string]any, targets []RouteSwitchoverTarget) ([]byte, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("no switchover targets given")
	}

	spec, ok := mapField(obj, "spec")
	if !ok {
		return nil, fmt.Errorf("base gateway CR has no spec")
	}
	routes, ok := sliceField(spec, "routes")
	if !ok {
		return nil, fmt.Errorf("base gateway CR has no spec.routes")
	}

	for _, target := range targets {
		found := false
		for _, raw := range routes {
			route, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if routeName, _ := stringField(route, "name"); routeName != target.RouteName {
				continue
			}
			found = true
			// Overwrite wholesale rather than merge: the route's binding
			// belongs entirely to one domain at a time, so a stale field from
			// a previous domain's reference must not survive the flip.
			route["streamingDomain"] = map[string]any{
				"name":              target.StreamingDomainName,
				"bootstrapServerId": target.BootstrapServerId,
			}
		}
		if !found {
			return nil, fmt.Errorf("route %q not found in the base gateway CR's spec.routes", target.RouteName)
		}
	}

	patched, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshalling switched gateway CR: %w", err)
	}
	return patched, nil
}
