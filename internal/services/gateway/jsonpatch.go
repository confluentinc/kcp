package gateway

import "fmt"

// jsonPatchOp is one RFC 6902 operation. Every op this package emits (test,
// add, replace) carries a value.
type jsonPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// RoutePatch is a route-relative description of the single change a migration
// step makes to the Gateway CR: set one route key (Field != ""), or replace the
// whole route element (Field == "", unfence's restore). The gateway service
// resolves RouteName to its spec.routes index and builds the absolute patch.
type RoutePatch struct {
	RouteName string
	Field     string // "rules" | "fence" | "streamingDomain"; "" ⇒ replace the whole route
	Value     any
}

// routeIndex returns the index of the first route whose name equals name,
// mirroring the first-match-by-name rule the old splicers used (route names are
// assumed unique). routes is spec.routes read back from the live CR.
func routeIndex(routes []any, name string) (int, error) {
	for i, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if n, _ := route["name"].(string); n == name {
			return i, nil
		}
	}
	return 0, fmt.Errorf("route %q not found in the gateway CR's spec.routes", name)
}

// configIDOp validates configID and returns the op writing it to spec.configId.
// add upserts, so it works whether or not the field is already present.
func configIDOp(configID string) (jsonPatchOp, error) {
	if err := validateConfigID(configID); err != nil {
		return jsonPatchOp{}, err
	}
	return jsonPatchOp{Op: "add", Path: "/spec/" + gatewayConfigIDField, Value: configID}, nil
}

// buildRoutePatchOps turns a RoutePatch (+ optional configID) into the op list
// for one atomic patch: a test op guarding the resolved index against a
// concurrent reorder, a second test op guarding the current value of
// whatever the mutation op is about to overwrite (so a concurrent edit to
// that same value fails the patch instead of being silently clobbered — see
// the per-case comments below for when that guard is skipped), the mutation
// op itself, and — when configID is non-empty — the spec.configId op.
func buildRoutePatchOps(routes []any, rp RoutePatch, configID string) ([]jsonPatchOp, error) {
	idx, err := routeIndex(routes, rp.RouteName)
	if err != nil {
		return nil, err
	}
	current := routes[idx].(map[string]any) // routeIndex only matches map[string]any entries

	ops := []jsonPatchOp{
		{Op: "test", Path: fmt.Sprintf("/spec/routes/%d/name", idx), Value: rp.RouteName},
	}
	if rp.Field == "" {
		// The replace supplants the whole element, so guard the whole thing —
		// it always exists, since routeIndex just matched it by name.
		routePath := fmt.Sprintf("/spec/routes/%d", idx)
		ops = append(ops,
			jsonPatchOp{Op: "test", Path: routePath, Value: current},
			jsonPatchOp{Op: "replace", Path: routePath, Value: rp.Value},
		)
	} else {
		// Guard the target field's current value too, unless this is the
		// field's first-ever write: a "test" op on a path that doesn't exist
		// yet fails outright, and "add" is used here specifically so it can
		// create the field on that first write.
		fieldPath := fmt.Sprintf("/spec/routes/%d/%s", idx, rp.Field)
		if fieldValue, ok := current[rp.Field]; ok {
			ops = append(ops, jsonPatchOp{Op: "test", Path: fieldPath, Value: fieldValue})
		}
		ops = append(ops, jsonPatchOp{Op: "add", Path: fieldPath, Value: rp.Value})
	}

	if configID != "" {
		op, err := configIDOp(configID)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}
