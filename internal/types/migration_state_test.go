package types

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	require.NoError(t, state.WriteToFile(filePath), "WriteToFile failed")

	loaded, err := NewMigrationStateFromFile(filePath)
	require.NoError(t, err, "NewMigrationStateFromFile failed")
	require.Len(t, loaded.Migrations, 2, "expected 2 migrations")

	// Use reflect.DeepEqual via testify for full struct comparison
	assert.Equal(t, state.Migrations[0], loaded.Migrations[0])
	assert.Equal(t, state.Migrations[1], loaded.Migrations[1])

	// Verify build info round-trips (will be empty strings in test, but should match)
	assert.Equal(t, state.KcpBuildInfo.Version, loaded.KcpBuildInfo.Version)
	assert.Equal(t, state.KcpBuildInfo.Commit, loaded.KcpBuildInfo.Commit)
	assert.False(t, loaded.Timestamp.IsZero(), "expected non-zero Timestamp after round-trip")
}

func TestMigrationState_WriteToFile_AtomicWrite(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{MigrationId: "mig-001", CurrentState: "initialized"},
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "migration-state.json")

	require.NoError(t, state.WriteToFile(filePath), "WriteToFile failed")

	// Verify the final file exists
	_, err := os.Stat(filePath)
	require.NoError(t, err, "expected state file to exist")

	// Verify no .tmp file remains after successful write
	tmpFile := filePath + ".tmp"
	_, err = os.Stat(tmpFile)
	assert.True(t, os.IsNotExist(err), "expected .tmp file to not exist after successful write")
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

	require.Len(t, state.Migrations, 2, "expected 2 migrations after insert")
	assert.Equal(t, "mig-001", state.Migrations[0].MigrationId)
	assert.Equal(t, "mig-002", state.Migrations[1].MigrationId)
	assert.Equal(t, "executing", state.Migrations[1].CurrentState)
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

	require.Len(t, state.Migrations, 2, "expected 2 migrations after update (not duplicated)")
	assert.Equal(t, "executing", state.Migrations[0].CurrentState)
	assert.Len(t, state.Migrations[0].Topics, 2, "expected updated migration to have 2 topics")
	// Verify the other migration was not affected
	assert.Equal(t, "mig-002", state.Migrations[1].MigrationId)
	assert.Equal(t, "initialized", state.Migrations[1].CurrentState)
}

func TestMigrationState_GetMigrationById_Found(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{MigrationId: "mig-001", CurrentState: "initialized", ClusterId: "lkc-111"},
		{MigrationId: "mig-002", CurrentState: "executing", ClusterId: "lkc-222", Topics: []string{"orders"}},
	}

	result, err := state.GetMigrationById("mig-002")
	require.NoError(t, err, "GetMigrationById returned unexpected error")
	require.NotNil(t, result)

	assert.Equal(t, "mig-002", result.MigrationId)
	assert.Equal(t, "executing", result.CurrentState)
	assert.Equal(t, "lkc-222", result.ClusterId)
	assert.Equal(t, []string{"orders"}, result.Topics)

	// Verify defensive copy: modifying the returned pointer must not affect the original.
	// GetMigrationById copies the struct before returning a pointer to it,
	// so mutations to the result should be isolated from the state's slice.
	result.CurrentState = "completed"
	assert.Equal(t, "executing", state.Migrations[1].CurrentState,
		"modifying returned pointer should not affect original state")
}

func TestMigrationState_GetMigrationById_NotFound(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{MigrationId: "mig-001", CurrentState: "initialized"},
	}

	result, err := state.GetMigrationById("non-existent")
	require.Error(t, err, "expected error for non-existent migration ID")
	assert.Nil(t, result, "expected nil result for non-existent migration ID")
}

func TestNewMigrationStateFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "invalid.json")

	require.NoError(t, os.WriteFile(filePath, []byte("not valid json {{{"), 0644), "failed to write test file")

	result, err := NewMigrationStateFromFile(filePath)
	require.Error(t, err, "expected error for invalid JSON")
	assert.Nil(t, result, "expected nil result for invalid JSON")
}

func TestNewMigrationStateFromFile_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "does-not-exist.json")

	result, err := NewMigrationStateFromFile(filePath)
	require.Error(t, err, "expected error for non-existent file")
	assert.Nil(t, result, "expected nil result for non-existent file")
}
