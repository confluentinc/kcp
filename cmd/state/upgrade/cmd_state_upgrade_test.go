package upgrade

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/state/migrate"
)

func TestUpgradeWritesCurrentSchema(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "old.json")
	out := filepath.Join(dir, "new.json")
	// Era C file lacking schema_version (loads, then re-stamped on write).
	if err := os.WriteFile(in, []byte(`{"msk_sources":{"regions":[]},"kcp_build_info":{"version":"0.8.0","commit":"x","date":"y"},"timestamp":"2026-05-14T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := NewStateUpgradeCmd()
	cmd.SetArgs([]string{"--in", in, "--out", out})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.SchemaVersion != migrate.CurrentSchemaVersion {
		t.Errorf("schema_version = %d, want %d", probe.SchemaVersion, migrate.CurrentSchemaVersion)
	}
}
