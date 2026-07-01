package upgrade

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/state/migrate"
)

func TestUpgradeWritesCurrentSchemaInPlace(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "kcp-state.json")
	// Era C file lacking schema_version (loads, then re-stamped on write).
	if err := os.WriteFile(stateFile, []byte(`{"msk_sources":{"regions":[]},"kcp_build_info":{"version":"0.8.0","commit":"x","date":"y"},"timestamp":"2026-05-14T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := NewStateUpgradeCmd()
	cmd.SetArgs([]string{"--state-file", stateFile})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// The file is overwritten in place at the current schema.
	data, err := os.ReadFile(stateFile)
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

	// Upgrading an older-schema file in place leaves a timestamped .bak of the original.
	matches, err := filepath.Glob(stateFile + ".*.bak")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one .bak backup, found %d: %v", len(matches), matches)
	}
}
