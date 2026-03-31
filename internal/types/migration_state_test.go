package types

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationState_WriteAndRead_RoundTrip(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{
			MigrationId:         "mig-001",
			CurrentState:        "initialized",
			KubeConfigPath:      "/home/user/.kube/config",
			SourceBootstrap:     "source-broker:9092",
			ClusterBootstrap:    "dest-broker:9092",
			ClusterId:           "lkc-abc123",
			ClusterRestEndpoint: "https://pkc-abc.us-east-1.aws.confluent.cloud:443",
			ClusterLinkName:     "my-link",
			Topics:              []string{"orders", "payments"},
			ClusterLinkTopics:   []string{"orders", "payments"},
			ClusterLinkConfigs:  map[string]string{"consumer.offset.sync.enable": "true"},
			InitialCrName:       "my-gateway-cr",
			K8sNamespace:        "confluent",
			InitialCrYAML:       []byte("apiVersion: v1"),
			FencedCrYAML:        []byte("apiVersion: v1\nfenced: true"),
			SwitchoverCrYAML:    []byte("apiVersion: v1\nswitchover: true"),
		},
		{
			MigrationId:      "mig-002",
			CurrentState:     "executing",
			SourceBootstrap:  "source-broker-2:9092",
			ClusterBootstrap: "dest-broker-2:9092",
			ClusterId:        "lkc-def456",
			ClusterLinkName:  "my-link-2",
			Topics:           []string{"events"},
		},
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "migration-state.json")

	if err := state.WriteToFile(filePath); err != nil {
		t.Fatalf("WriteToFile failed: %v", err)
	}

	loaded, err := NewMigrationStateFromFile(filePath)
	if err != nil {
		t.Fatalf("NewMigrationStateFromFile failed: %v", err)
	}

	if len(loaded.Migrations) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(loaded.Migrations))
	}

	// Verify first migration
	m1 := loaded.Migrations[0]
	if m1.MigrationId != "mig-001" {
		t.Errorf("migration 0: expected MigrationId %q, got %q", "mig-001", m1.MigrationId)
	}
	if m1.CurrentState != "initialized" {
		t.Errorf("migration 0: expected CurrentState %q, got %q", "initialized", m1.CurrentState)
	}
	if m1.KubeConfigPath != "/home/user/.kube/config" {
		t.Errorf("migration 0: expected KubeConfigPath %q, got %q", "/home/user/.kube/config", m1.KubeConfigPath)
	}
	if m1.SourceBootstrap != "source-broker:9092" {
		t.Errorf("migration 0: expected SourceBootstrap %q, got %q", "source-broker:9092", m1.SourceBootstrap)
	}
	if m1.ClusterBootstrap != "dest-broker:9092" {
		t.Errorf("migration 0: expected ClusterBootstrap %q, got %q", "dest-broker:9092", m1.ClusterBootstrap)
	}
	if m1.ClusterId != "lkc-abc123" {
		t.Errorf("migration 0: expected ClusterId %q, got %q", "lkc-abc123", m1.ClusterId)
	}
	if m1.ClusterRestEndpoint != "https://pkc-abc.us-east-1.aws.confluent.cloud:443" {
		t.Errorf("migration 0: expected ClusterRestEndpoint %q, got %q", "https://pkc-abc.us-east-1.aws.confluent.cloud:443", m1.ClusterRestEndpoint)
	}
	if m1.ClusterLinkName != "my-link" {
		t.Errorf("migration 0: expected ClusterLinkName %q, got %q", "my-link", m1.ClusterLinkName)
	}
	if len(m1.Topics) != 2 || m1.Topics[0] != "orders" || m1.Topics[1] != "payments" {
		t.Errorf("migration 0: expected Topics [orders payments], got %v", m1.Topics)
	}
	if len(m1.ClusterLinkTopics) != 2 || m1.ClusterLinkTopics[0] != "orders" || m1.ClusterLinkTopics[1] != "payments" {
		t.Errorf("migration 0: expected ClusterLinkTopics [orders payments], got %v", m1.ClusterLinkTopics)
	}
	if v, ok := m1.ClusterLinkConfigs["consumer.offset.sync.enable"]; !ok || v != "true" {
		t.Errorf("migration 0: expected ClusterLinkConfigs to contain consumer.offset.sync.enable=true, got %v", m1.ClusterLinkConfigs)
	}
	if m1.InitialCrName != "my-gateway-cr" {
		t.Errorf("migration 0: expected InitialCrName %q, got %q", "my-gateway-cr", m1.InitialCrName)
	}
	if m1.K8sNamespace != "confluent" {
		t.Errorf("migration 0: expected K8sNamespace %q, got %q", "confluent", m1.K8sNamespace)
	}
	if string(m1.InitialCrYAML) != "apiVersion: v1" {
		t.Errorf("migration 0: expected InitialCrYAML %q, got %q", "apiVersion: v1", string(m1.InitialCrYAML))
	}
	if string(m1.FencedCrYAML) != "apiVersion: v1\nfenced: true" {
		t.Errorf("migration 0: expected FencedCrYAML %q, got %q", "apiVersion: v1\nfenced: true", string(m1.FencedCrYAML))
	}
	if string(m1.SwitchoverCrYAML) != "apiVersion: v1\nswitchover: true" {
		t.Errorf("migration 0: expected SwitchoverCrYAML %q, got %q", "apiVersion: v1\nswitchover: true", string(m1.SwitchoverCrYAML))
	}

	// Verify second migration
	m2 := loaded.Migrations[1]
	if m2.MigrationId != "mig-002" {
		t.Errorf("migration 1: expected MigrationId %q, got %q", "mig-002", m2.MigrationId)
	}
	if m2.CurrentState != "executing" {
		t.Errorf("migration 1: expected CurrentState %q, got %q", "executing", m2.CurrentState)
	}
	if len(m2.Topics) != 1 || m2.Topics[0] != "events" {
		t.Errorf("migration 1: expected Topics [events], got %v", m2.Topics)
	}

	// Verify build info round-trips (will be empty strings in test, but should match)
	if loaded.KcpBuildInfo.Version != state.KcpBuildInfo.Version {
		t.Errorf("expected build info Version %q, got %q", state.KcpBuildInfo.Version, loaded.KcpBuildInfo.Version)
	}
	if loaded.KcpBuildInfo.Commit != state.KcpBuildInfo.Commit {
		t.Errorf("expected build info Commit %q, got %q", state.KcpBuildInfo.Commit, loaded.KcpBuildInfo.Commit)
	}
	if loaded.Timestamp.IsZero() {
		t.Errorf("expected non-zero Timestamp after round-trip")
	}
}

func TestMigrationState_WriteToFile_AtomicWrite(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{MigrationId: "mig-001", CurrentState: "initialized"},
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "migration-state.json")

	if err := state.WriteToFile(filePath); err != nil {
		t.Fatalf("WriteToFile failed: %v", err)
	}

	// Verify the final file exists
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("expected state file to exist: %v", err)
	}

	// Verify no .tmp file remains after successful write
	tmpFile := filePath + ".tmp"
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Errorf("expected .tmp file to not exist after successful write, but it does")
	}
}

func TestMigrationState_UpsertMigration_Insert(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{MigrationId: "mig-001", CurrentState: "initialized", Topics: []string{"topic-a"}},
	}

	newMigration := MigrationConfig{
		MigrationId:  "mig-002",
		CurrentState: "executing",
		Topics:       []string{"topic-b"},
	}

	state.UpsertMigration(newMigration)

	if len(state.Migrations) != 2 {
		t.Fatalf("expected 2 migrations after insert, got %d", len(state.Migrations))
	}

	if state.Migrations[0].MigrationId != "mig-001" {
		t.Errorf("expected first migration to remain mig-001, got %q", state.Migrations[0].MigrationId)
	}
	if state.Migrations[1].MigrationId != "mig-002" {
		t.Errorf("expected second migration to be mig-002, got %q", state.Migrations[1].MigrationId)
	}
	if state.Migrations[1].CurrentState != "executing" {
		t.Errorf("expected second migration state %q, got %q", "executing", state.Migrations[1].CurrentState)
	}
}

func TestMigrationState_UpsertMigration_Update(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{MigrationId: "mig-001", CurrentState: "initialized", Topics: []string{"topic-a"}},
		{MigrationId: "mig-002", CurrentState: "initialized", Topics: []string{"topic-b"}},
	}

	updated := MigrationConfig{
		MigrationId:  "mig-001",
		CurrentState: "executing",
		Topics:       []string{"topic-a", "topic-c"},
	}

	state.UpsertMigration(updated)

	if len(state.Migrations) != 2 {
		t.Fatalf("expected 2 migrations after update (not duplicated), got %d", len(state.Migrations))
	}

	if state.Migrations[0].CurrentState != "executing" {
		t.Errorf("expected updated migration state %q, got %q", "executing", state.Migrations[0].CurrentState)
	}
	if len(state.Migrations[0].Topics) != 2 {
		t.Errorf("expected updated migration to have 2 topics, got %d", len(state.Migrations[0].Topics))
	}
	// Verify the other migration was not affected
	if state.Migrations[1].MigrationId != "mig-002" {
		t.Errorf("expected second migration to remain mig-002, got %q", state.Migrations[1].MigrationId)
	}
	if state.Migrations[1].CurrentState != "initialized" {
		t.Errorf("expected second migration state to remain %q, got %q", "initialized", state.Migrations[1].CurrentState)
	}
}

func TestMigrationState_GetMigrationById_Found(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{MigrationId: "mig-001", CurrentState: "initialized", ClusterId: "lkc-111"},
		{MigrationId: "mig-002", CurrentState: "executing", ClusterId: "lkc-222", Topics: []string{"orders"}},
	}

	result, err := state.GetMigrationById("mig-002")
	if err != nil {
		t.Fatalf("GetMigrationById returned unexpected error: %v", err)
	}

	if result.MigrationId != "mig-002" {
		t.Errorf("expected MigrationId %q, got %q", "mig-002", result.MigrationId)
	}
	if result.CurrentState != "executing" {
		t.Errorf("expected CurrentState %q, got %q", "executing", result.CurrentState)
	}
	if result.ClusterId != "lkc-222" {
		t.Errorf("expected ClusterId %q, got %q", "lkc-222", result.ClusterId)
	}
	if len(result.Topics) != 1 || result.Topics[0] != "orders" {
		t.Errorf("expected Topics [orders], got %v", result.Topics)
	}

	// Verify returned value is a copy (modifying it should not affect the original)
	result.CurrentState = "completed"
	if state.Migrations[1].CurrentState != "executing" {
		t.Errorf("modifying returned pointer should not affect original state, but original changed to %q", state.Migrations[1].CurrentState)
	}
}

func TestMigrationState_GetMigrationById_NotFound(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{MigrationId: "mig-001", CurrentState: "initialized"},
	}

	result, err := state.GetMigrationById("non-existent")
	if err == nil {
		t.Fatal("expected error for non-existent migration ID, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result for non-existent migration ID, got %+v", result)
	}
}

func TestNewMigrationStateFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "invalid.json")

	if err := os.WriteFile(filePath, []byte("not valid json {{{"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	result, err := NewMigrationStateFromFile(filePath)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result for invalid JSON, got %+v", result)
	}
}

func TestNewMigrationStateFromFile_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "does-not-exist.json")

	result, err := NewMigrationStateFromFile(filePath)
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result for non-existent file, got %+v", result)
	}
}
