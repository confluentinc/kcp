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
// concurrent reorder, the mutation op, and — when configID is non-empty — the
// spec.configId op.
func buildRoutePatchOps(routes []any, rp RoutePatch, configID string) ([]jsonPatchOp, error) {
	idx, err := routeIndex(routes, rp.RouteName)
	if err != nil {
		return nil, err
	}

	ops := []jsonPatchOp{
		{Op: "test", Path: fmt.Sprintf("/spec/routes/%d/name", idx), Value: rp.RouteName},
	}
	if rp.Field == "" {
		ops = append(ops, jsonPatchOp{Op: "replace", Path: fmt.Sprintf("/spec/routes/%d", idx), Value: rp.Value})
	} else {
		ops = append(ops, jsonPatchOp{Op: "add", Path: fmt.Sprintf("/spec/routes/%d/%s", idx, rp.Field), Value: rp.Value})
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
