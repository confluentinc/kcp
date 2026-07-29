package types

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadFixtures loads each hand-built state-file fixture through the full
// migrate-then-decode path and asserts the documented outcome. Fixtures live in
// internal/state/migrate/testdata (where the migration engine's own tests use
// them too); this package is internal/types, so the path is ../state/migrate/testdata.
func TestLoadFixtures(t *testing.T) {
	cases := []struct {
		file     string
		wantLoad bool
	}{
		{"era-c-v0.8.0.json", true},
		{"era-c-v0.8.5.json", true},
		{"era-b-v0.7.3.json", true},
		// Array-form schema_registries (v0.4.2–v0.7.1) — recovered to the object form by the
		// schema_registries array→object upcaster, so it now loads.
		{"era-b-v0.5.0.json", true},
		// Regression: a real era-B file (top-level regions, pre-schema_version) carrying
		// self_managed_connectors (introduced inside the era-B window, commit 0c02c469).
		// Proves the v1->v2 nesting upcaster's guard also fires for era B, not just
		// schema_version==1/era==C — otherwise self_managed_connectors survives migration
		// and this strict (DisallowUnknownFields) decode fails.
		{"era-b-self-managed-connectors.json", true},
		// Regression: an era-C file (osk_sources shape) with NO schema_version at all — an
		// early v0.8.x file predating schema_version on era C — carrying self_managed_connectors.
		// Upgrade used to short-circuit "schemaVersion == 0 && era == C" as an already-current
		// passthrough, skipping the v1->v2 nesting step and leaving self_managed_connectors in
		// place; this strict (DisallowUnknownFields) decode then rejected it with an unknown
		// field error. This is the durable guard for that fix (see
		// TestUpgrade_EraCUnversionedSelfManagedConnectorsToConnectClusters in
		// internal/state/migrate for the migration-shape half of the regression proof).
		{"era-c-v0-self-managed-connectors.json", true},
	}
	base := filepath.Join("..", "state", "migrate", "testdata")
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(base, tc.file))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			_, err = NewStateFromBytes(data)
			if tc.wantLoad && err != nil {
				t.Errorf("fixture should load, got error: %v", err)
			}
			if !tc.wantLoad && err == nil {
				t.Errorf("fixture should have failed to load")
			}
		})
	}
}
