package migrate

// step is one upcaster. appliesWhen decides if this step should run for the
// detected (era, buildVersion). transform rewrites the raw JSON forward by one
// shape. Steps are ordered oldest-shape-first; Upgrade runs each matching step
// in sequence until the data is at the current shape.
type step struct {
	name        string
	appliesWhen func(era string, buildVersion string) bool
	transform   func(in map[string]any) (map[string]any, error)
}

// steps is the ordered upcaster registry. The B→C root reshape lands here first; Plan 2
// adds the remaining within-B per-tag upcasters down to the v0.4.0 floor. (Pre-v0.4.0 /
// Era A is out of scope — spec N5 — so no step targets it.)
var steps = []step{
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
