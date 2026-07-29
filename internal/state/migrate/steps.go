package migrate

import "fmt"

// step is one upcaster. appliesWhen decides if this step should run for the
// detected (era, buildVersion). transform rewrites the raw JSON forward by one
// shape. Steps are ordered oldest-shape-first; Upgrade runs each matching step
// in sequence until the data is at the current shape.
type step struct {
	name        string
	appliesWhen func(schemaVersion int, era string, buildVersion string) bool
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
		appliesWhen: func(_ int, era, _ string) bool { return era == "B" },
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
		appliesWhen: func(_ int, era, _ string) bool { return era == "B" },
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
	{
		// v1 (era C) → v2: self_managed_connectors {connectors, metrics} becomes
		// connect_clusters: [{connect_rest_url:"", connectors:[...], metrics:{...}}].
		// Legacy files never persisted the Connect REST URL, so it is left empty.
		// connect_host on each connector is carried through unchanged.
		//
		// Also fires for era B: self_managed_connectors was introduced (commit 0c02c469)
		// while files still had the top-level `regions` shape (v0.5.0-v0.7.3, no
		// schema_version field at all), so a real era-B file can carry it. The B->C reshape
		// step above already ran by the time this step executes (steps run in slice order),
		// so msk_sources.regions[].clusters[] exists for eachAdminInfo to walk regardless of
		// era. wrap() is self-gating (no-op when self_managed_connectors is absent/nil), so
		// broadening the guard to era B is safe for era-B files that never had the field.
		name:        "C v1->v2: nest self_managed_connectors under connect_clusters",
		appliesWhen: func(schemaVersion int, era, _ string) bool { return era == "B" || (era == "C" && schemaVersion < 2) },
		transform: func(in map[string]any) (map[string]any, error) {
			wrap := func(admin map[string]any) {
				smc, ok := admin["self_managed_connectors"].(map[string]any)
				delete(admin, "self_managed_connectors")
				if !ok || smc == nil {
					return // nothing scanned for this cluster
				}
				cc := map[string]any{"connect_rest_url": ""}
				if conns, ok := smc["connectors"]; ok && conns != nil {
					cc["connectors"] = conns
				} else {
					cc["connectors"] = []any{}
				}
				if m, ok := smc["metrics"]; ok && m != nil {
					cc["metrics"] = m
				}
				admin["connect_clusters"] = []any{cc}
			}
			eachAdminInfo(in, wrap) // walks MSK regions[].clusters[] and OSK clusters[]
			in["schema_version"] = 2
			return in, nil
		},
	},
}

// eachAdminInfo applies fn to every kafka_admin_client_information object in the
// raw state doc — across MSK regions[].clusters[] and OSK clusters[]. Missing
// branches are skipped; malformed nodes are ignored (a later strict decode catches them).
func eachAdminInfo(in map[string]any, fn func(admin map[string]any)) {
	visitClusters := func(clusters []any) {
		for _, c := range clusters {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if admin, ok := cm["kafka_admin_client_information"].(map[string]any); ok {
				fn(admin)
			}
		}
	}
	if msk, ok := in["msk_sources"].(map[string]any); ok {
		if regions, ok := msk["regions"].([]any); ok {
			for _, r := range regions {
				if rm, ok := r.(map[string]any); ok {
					if clusters, ok := rm["clusters"].([]any); ok {
						visitClusters(clusters)
					}
				}
			}
		}
	}
	if osk, ok := in["osk_sources"].(map[string]any); ok {
		if clusters, ok := osk["clusters"].([]any); ok {
			visitClusters(clusters)
		}
	}
}
