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
		// Array-form schema_registries (v0.4.2–v0.7.1). The loader can't yet read this
		// shape, so it currently fails to load. This locks that behavior into CI; flip to
		// `true` when the Plan 2 array→object schema_registries upcaster lands.
		{"era-b-v0.5.0.json", false},
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
