package version

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A v0.5.0-style file whose schema_registries is the old ARRAY form — the strict loader
// rejects this, but `kcp state version` must report its metadata anyway (lenient).
const arrayFormState = `{
  "regions": [],
  "schema_registries": [{"type":"CONFLUENT","url":"http://sr:8081"}],
  "kcp_build_info": {"version":"0.5.0","commit":"abc1234","date":"2026-01-02T00:00:00Z"},
  "timestamp": "2026-01-01T00:00:00Z"
}`

func TestParseStateMetadata_LenientOnUnloadableFile(t *testing.T) {
	meta, err := parseStateMetadata([]byte(arrayFormState))
	if err != nil {
		t.Fatalf("lenient parse should succeed on an array-schema_registries file, got: %v", err)
	}
	if meta.KcpBuildInfo.Version != "0.5.0" {
		t.Errorf("version = %q, want 0.5.0", meta.KcpBuildInfo.Version)
	}
	if meta.SchemaVersion != 0 {
		t.Errorf("schema_version = %d, want 0 (absent)", meta.SchemaVersion)
	}
	if meta.Timestamp != "2026-01-01T00:00:00Z" {
		t.Errorf("timestamp = %q", meta.Timestamp)
	}
}

func TestParseStateMetadata_InvalidJSON(t *testing.T) {
	if _, err := parseStateMetadata([]byte("not json {{{")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestStateVersionCmd_ReportsMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kcp-state.json")
	body := `{"schema_version":1,"msk_sources":{"regions":[]},"kcp_build_info":{"version":"0.8.5","commit":"deadbee","date":"2026-06-17T00:00:00Z"},"timestamp":"2026-05-14T00:00:00Z","updated_at":"2026-06-26T10:30:00Z","upgraded_from":"kcp_build_info.version=0.7.3"}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := NewStateVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--state-file", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	for _, want := range []string{"Schema version", "1", "0.8.5", "deadbee", "Created", "2026-05-14T00:00:00Z", "Last updated", "Upgraded from", "kcp_build_info.version=0.7.3"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

func TestStateVersionCmd_NonKCPJSON_NoMisleadingSchemaRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "random.json")
	if err := os.WriteFile(path, []byte(`{"foo":"bar"}`), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := NewStateVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--state-file", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "does not look like a kcp-state.json") {
		t.Errorf("expected a 'not a kcp-state.json' notice, got:\n%s", got)
	}
	if strings.Contains(got, "Schema version") {
		t.Errorf("must NOT print a misleading Schema version row for non-KCP JSON, got:\n%s", got)
	}
}

func TestStateVersionCmd_LegacyAndAbsentFieldsHidden(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kcp-state.json")
	// No schema_version, no updated_at/upgraded_from, commit "unknown".
	body := `{"regions":[],"kcp_build_info":{"version":"0.4.0","commit":"unknown","date":"unknown"},"timestamp":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := NewStateVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--state-file", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "unversioned (legacy)") {
		t.Errorf("absent schema_version should render 'unversioned (legacy)', got:\n%s", got)
	}
	if strings.Contains(got, "Commit") || strings.Contains(got, "unknown") {
		t.Errorf("'unknown' commit/date must be hidden, got:\n%s", got)
	}
	if strings.Contains(got, "Last updated") || strings.Contains(got, "Upgraded from") {
		t.Errorf("absent updated_at/upgraded_from rows must be hidden, got:\n%s", got)
	}
}
