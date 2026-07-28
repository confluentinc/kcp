package migrate

import "encoding/json"

// detectVersion inspects raw state-file bytes using the 3-tier scheme:
//  1. explicit schema_version (authoritative, present from CurrentSchemaVersion onward)
//  2. kcp_build_info.version (present in files from v0.3.1 onward)
//  3. structural sniff of top-level keys (only pre-v0.3.1 files lack a build version)
//
// era is "B" or "C" — the only in-scope kcp-state.json root shapes:
//   - C: root has msk_sources/osk_sources (v0.8.0+)
//   - B: root has top-level regions (v0.4.0–v0.7.3)
//
// There is deliberately NO Era A. kcp-state.json was introduced at v0.4.0; pre-v0.4.0
// releases wrote a different file (<region>-region-scan.json, root RegionScanResult). Such
// files (top-level clusters+region, no State wrapper) — and any unrelated JSON — are not
// recognised (spec N5): they are not assigned an era, default to the current shape "C", and
// fail later at the strict decode with the generic error, exactly like a random JSON file.
func detectVersion(data []byte) (schemaVersion int, buildVersion string, era string, err error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
		KcpBuildInfo  struct {
			Version string `json:"version"`
		} `json:"kcp_build_info"`
		MSKSources json.RawMessage `json:"msk_sources"`
		OSKSources json.RawMessage `json:"osk_sources"`
		Regions    json.RawMessage `json:"regions"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return 0, "", "", err
	}

	switch {
	case probe.MSKSources != nil || probe.OSKSources != nil:
		era = "C"
	case probe.Regions != nil:
		era = "B"
	default:
		// Empty/new file, a pre-v0.4.0 region-scan file, or unrelated JSON — all default
		// to the current shape "C" (no Era A; spec N5). A genuine current file decodes
		// cleanly; anything foreign fails the strict decode in NewStateFromBytes.
		era = "C"
	}

	return probe.SchemaVersion, probe.KcpBuildInfo.Version, era, nil
}
