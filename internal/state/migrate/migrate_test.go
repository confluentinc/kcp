package migrate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectVersion(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		wantSchema int
		wantBuild  string
		wantEra    string
	}{
		{
			name:       "explicit schema_version wins",
			data:       `{"schema_version":1,"kcp_build_info":{"version":"0.8.5"},"msk_sources":{}}`,
			wantSchema: 1, wantBuild: "0.8.5", wantEra: "C",
		},
		{
			name:       "era C by build version when schema_version absent",
			data:       `{"kcp_build_info":{"version":"0.8.0"},"msk_sources":{}}`,
			wantSchema: 0, wantBuild: "0.8.0", wantEra: "C",
		},
		{
			name:       "era C by structure (msk_sources) when no build version",
			data:       `{"msk_sources":{},"osk_sources":{}}`,
			wantSchema: 0, wantBuild: "", wantEra: "C",
		},
		{
			name:       "era B by structure (top-level regions)",
			data:       `{"regions":[],"kcp_build_info":{"version":"0.7.3"}}`,
			wantSchema: 0, wantBuild: "0.7.3", wantEra: "B",
		},
		{
			// Pre-v0.4.0 region-scan file (top-level clusters+region, no State wrapper).
			// It is NOT recognised as a kcp-state.json era (no Era A branch — spec N5):
			// detection assigns no era, so it defaults to the current shape "C" and will
			// fail later at strict decode, exactly like an unrelated JSON file.
			name:       "pre-v0.4.0 region-scan file is not a recognised era (defaults to C)",
			data:       `{"clusters":[],"region":"us-east-1","vpc_connections":[]}`,
			wantSchema: 0, wantBuild: "", wantEra: "C",
		},
		{
			// File STAMPED localdev: build version is a dev sentinel, so era must
			// come from structure, not the (useless) build version. Reader binary
			// version is irrelevant — detection only reads the file (spec §6.2/§6.9).
			name:       "dev-stamped file resolves era by structure",
			data:       `{"regions":[],"kcp_build_info":{"version":"0.0.0-localdev"}}`,
			wantSchema: 0, wantBuild: "0.0.0-localdev", wantEra: "B",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotSchema, gotBuild, gotEra, err := detectVersion([]byte(tc.data))
			if err != nil {
				t.Fatalf("detectVersion returned error: %v", err)
			}
			if gotSchema != tc.wantSchema || gotBuild != tc.wantBuild || gotEra != tc.wantEra {
				t.Errorf("got (schema=%d build=%q era=%q), want (schema=%d build=%q era=%q)",
					gotSchema, gotBuild, gotEra, tc.wantSchema, tc.wantBuild, tc.wantEra)
			}
		})
	}
}

func TestUpgradeForwardIncompatible(t *testing.T) {
	data := `{"schema_version":99,"kcp_build_info":{"version":"0.9.0"},"msk_sources":{}}`
	_, _, err := Upgrade([]byte(data))
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("want ErrNewerSchema, got %v", err)
	}
}

func TestUpgradeForwardIncompatibleDevStamped(t *testing.T) {
	// Symmetric scenario: an official-release binary reads a file STAMPED by a dev
	// build whose schema_version is ahead. Must NOT advise `kcp update` (spec §6.9).
	data := `{"schema_version":99,"kcp_build_info":{"version":"0.0.0-localdev"},"msk_sources":{}}`
	_, _, err := Upgrade([]byte(data))
	if !errors.Is(err, ErrNewerSchemaDev) {
		t.Fatalf("want ErrNewerSchemaDev, got %v", err)
	}
}

func TestUpgradeCurrentIsIdentity(t *testing.T) {
	data := `{"schema_version":1,"msk_sources":{},"kcp_build_info":{"version":"0.8.5"}}`
	got, from, err := Upgrade([]byte(data))
	if err != nil {
		t.Fatalf("Upgrade error: %v", err)
	}
	if from != "schema_version=1" {
		t.Errorf("from label = %q, want schema_version=1", from)
	}
	if string(got) != data {
		t.Errorf("current-version data must pass through unchanged.\n got: %s\nwant: %s", got, data)
	}
}

func TestUpgradeEraBv073ToC(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "era-b-v0.7.3.json"))
	if err != nil {
		t.Fatal(err)
	}
	migrated, from, err := Upgrade(data)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if from != "kcp_build_info.version=0.7.3" {
		t.Errorf("from = %q", from)
	}
	var doc map[string]any
	if err := json.Unmarshal(migrated, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["regions"]; ok {
		t.Error("top-level regions should be gone after B->C")
	}
	msk, ok := doc["msk_sources"].(map[string]any)
	if !ok {
		t.Fatal("msk_sources missing after B->C")
	}
	if _, ok := msk["regions"]; !ok {
		t.Error("regions should be nested under msk_sources")
	}
}

func TestUpgradeUnrecognizedIsNotSpecialCased(t *testing.T) {
	// A pre-v0.4.0 region-scan file (or any unrelated JSON) is NOT detected or migrated
	// (spec N5): no Era A branch exists, so it resolves to the current shape and Upgrade
	// passes it through UNCHANGED with no error. The generic failure happens later, at the
	// strict decode in NewStateFromBytes — exactly as for an unrelated JSON file. Upgrade
	// must not raise ErrUnsupportedLegacy (which would wrongly advise `kcp state upgrade`).
	data := `{"clusters":[],"region":"us-east-1","vpc_connections":[]}`
	got, _, err := Upgrade([]byte(data))
	if err != nil {
		t.Fatalf("unrecognised JSON must not error in Upgrade, got %v", err)
	}
	if string(got) != data {
		t.Errorf("unrecognised JSON must pass through unchanged.\n got: %s\nwant: %s", got, data)
	}
}

func TestUpgradeNormalizesArraySchemaRegistries(t *testing.T) {
	// Era B file (top-level regions) with the old ARRAY-form schema_registries.
	data := `{"regions":[{"name":"us-east-1","clusters":[]}],"schema_registries":[{"type":"confluent","url":"http://sr:8081","subjects":[]}],"kcp_build_info":{"version":"0.5.0"},"timestamp":"2026-01-01T00:00:00Z"}`
	migrated, _, err := Upgrade([]byte(data))
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(migrated, &doc); err != nil {
		t.Fatal(err)
	}
	sr, ok := doc["schema_registries"].(map[string]any)
	if !ok {
		t.Fatalf("schema_registries should be normalized to an object, got %T", doc["schema_registries"])
	}
	csr, ok := sr["confluent_schema_registry"].([]any)
	if !ok || len(csr) != 1 {
		t.Errorf("confluent_schema_registry should carry the 1 array entry, got %#v", sr["confluent_schema_registry"])
	}
	// The B->C reshape still happened.
	if _, ok := doc["msk_sources"].(map[string]any); !ok {
		t.Error("msk_sources missing after B->C")
	}
	if _, ok := doc["regions"]; ok {
		t.Error("top-level regions should be gone after B->C")
	}
}

func TestUpgradeArraySchemaRegistriesNonConfluentErrors(t *testing.T) {
	// Array-form schema_registries only ever held confluent registries; an unexpected type
	// must fail loudly rather than be mis-bucketed.
	data := `{"regions":[],"schema_registries":[{"type":"glue","registry_arn":"arn:x"}],"kcp_build_info":{"version":"0.5.0"}}`
	if _, _, err := Upgrade([]byte(data)); err == nil {
		t.Fatal("expected error for non-confluent type in array-form schema_registries")
	}
}
