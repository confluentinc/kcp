package migrate

import "fmt"

// step is one upcaster. appliesWhen decides if this step should run for the
// detected (era, buildVersion). transform rewrites the raw JSON forward by one
// shape. Steps are ordered oldest-shape-first; Upgrade runs each matching step
// in sequence until the data is at the current shape.
type step struct {
	name        string
	appliesWhen func(era string, buildVersion string) bool
	transform   func(in map[string]any) (map[string]any, error)
}

// steps is the ordered upcaster registry, applied in slice order. Era B only (Era A is out
// of scope — spec N5). The schema_registries normalization runs before the B→C root reshape
// so B→C carries the already-normalized object through.
var steps = []step{
	{
		// v0.4.2–v0.7.1 serialized schema_registries as a flat ARRAY of confluent registries;
		// v0.7.2+ (and the current struct) use an object {aws_glue, confluent_schema_registry}.
		// The confluent element shape is identical across the range, so this is a pure wrap.
		// Idempotent: a no-op when schema_registries is already an object, null, or absent.
		name:        "B: normalize array-form schema_registries to object",
		appliesWhen: func(era, _ string) bool { return era == "B" },
		transform: func(in map[string]any) (map[string]any, error) {
			arr, ok := in["schema_registries"].([]any)
			if !ok {
				return in, nil // object / null / absent → nothing to do
			}
			confluent := []any{}
			for _, el := range arr {
				m, ok := el.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("schema_registries array element is not an object (got %T)", el)
				}
				// Array-form predates aws_glue, so every entry must be confluent.
				if t, _ := m["type"].(string); t != "" && t != "confluent" {
					return nil, fmt.Errorf("unexpected schema registry type %q in array-form schema_registries (only confluent existed before the object form)", t)
				}
				confluent = append(confluent, m)
			}
			in["schema_registries"] = map[string]any{"confluent_schema_registry": confluent}
			return in, nil
		},
	},
	{
		name:        "B->C: nest top-level regions under msk_sources",
		appliesWhen: func(era, _ string) bool { return era == "B" },
		transform: func(in map[string]any) (map[string]any, error) {
			out := map[string]any{}
			out["msk_sources"] = map[string]any{"regions": in["regions"]}
			if sr, ok := in["schema_registries"]; ok {
				out["schema_registries"] = sr
			}
			if bi, ok := in["kcp_build_info"]; ok {
				out["kcp_build_info"] = bi
			}
			if ts, ok := in["timestamp"]; ok {
				out["timestamp"] = ts
			}
			return out, nil
		},
	},
}
