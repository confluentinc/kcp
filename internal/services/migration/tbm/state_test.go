package tbm

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashManifest_IsDeterministicSha256Hex(t *testing.T) {
	data := []byte("apiVersion: kcp.confluent.io/v1alpha1\nkind: GatewayMigration\n")
	want := sha256.Sum256(data)
	assert.Equal(t, hex.EncodeToString(want[:]), HashManifest(data))
	assert.Equal(t, HashManifest(data), HashManifest(data))
}

func TestHashManifest_DifferentContentDifferentHash(t *testing.T) {
	assert.NotEqual(t, HashManifest([]byte("a")), HashManifest([]byte("b")))
}

func TestTBMState_WriteAndRead_RoundTrip(t *testing.T) {
	state := NewTBMState()
	state.Migrations = []TBMConfig{
		{MigrationId: "tbm-1", CurrentState: StateInitialized, ManifestHash: "hash1"},
		{MigrationId: "tbm-2", CurrentState: StateSwitched, ManifestHash: "hash2"},
	}

	filePath := filepath.Join(t.TempDir(), "tbm-state.json")
	require.NoError(t, state.WriteToFile(filePath))

	loaded, err := NewTBMStateFromFile(filePath)
	require.NoError(t, err)
	require.Len(t, loaded.Migrations, 2)
	assert.Equal(t, state.Migrations[0], loaded.Migrations[0])
	assert.Equal(t, state.Migrations[1], loaded.Migrations[1])
	assert.False(t, loaded.Timestamp.IsZero())
}

func TestTBMState_WriteToFile_AtomicWrite(t *testing.T) {
	state := NewTBMState()
	filePath := filepath.Join(t.TempDir(), "tbm-state.json")
	require.NoError(t, state.WriteToFile(filePath))

	_, err := os.Stat(filePath)
	require.NoError(t, err)

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(filePath), "."+filepath.Base(filePath)+".tmp-*"))
	require.NoError(t, err)
	assert.Empty(t, matches, "expected no temp file to remain after successful write")
}

func TestTBMState_UpsertMigration_InsertThenUpdate(t *testing.T) {
	state := NewTBMState()
	state.UpsertMigration(TBMConfig{MigrationId: "tbm-1", CurrentState: StateUninitialized})
	require.Len(t, state.Migrations, 1)

	state.UpsertMigration(TBMConfig{MigrationId: "tbm-2", CurrentState: StateUninitialized})
	require.Len(t, state.Migrations, 2)

	state.UpsertMigration(TBMConfig{MigrationId: "tbm-1", CurrentState: StateSwitched, ManifestHash: "h"})
	require.Len(t, state.Migrations, 2, "update must not append a new row")

	got, err := state.GetMigrationById("tbm-1")
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, got.CurrentState)
	assert.Equal(t, "h", got.ManifestHash)
}

func TestTBMState_GetMigrationById_NotFound(t *testing.T) {
	state := NewTBMState()
	_, err := state.GetMigrationById("missing")
	require.Error(t, err)
}
