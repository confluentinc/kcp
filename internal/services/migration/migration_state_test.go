package migration

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
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
			FenceRoutes:         []string{"migration-route"},
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

	// Verify no temp file remains after successful write. The writer uses a
	// uniquely-named temp (..migration-state.json.tmp-*), so match its glob
	// rather than the legacy fixed name the code no longer creates.
	matches, err := filepath.Glob(filepath.Join(dir, "."+filepath.Base(filePath)+".tmp-*"))
	require.NoError(t, err)
	assert.Empty(t, matches, "expected no temp file to remain after successful write")
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

// TestMigrationConfig_PauseConsumerOffsetSync_RoundTrip verifies that both new
// fields persist through Write+Read with their explicit values preserved.
func TestMigrationConfig_PauseConsumerOffsetSync_RoundTrip(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{
			MigrationId:                    "mig-pause-001",
			CurrentState:                   "initialized",
			ClusterLinkConfigs:             map[string]string{"consumer.offset.sync.enable": "true"},
			PauseConsumerOffsetSync:        true,
			PauseConsumerOffsetSyncFlipped: false,
		},
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "migration-state.json")

	require.NoError(t, state.WriteToFile(filePath), "WriteToFile failed")

	loaded, err := NewMigrationStateFromFile(filePath)
	require.NoError(t, err, "NewMigrationStateFromFile failed")
	require.Len(t, loaded.Migrations, 1)

	assert.True(t, loaded.Migrations[0].PauseConsumerOffsetSync, "PauseConsumerOffsetSync should round-trip as true")
	assert.False(t, loaded.Migrations[0].PauseConsumerOffsetSyncFlipped, "PauseConsumerOffsetSyncFlipped should round-trip as false")
}

// TestMigrationConfig_PauseConsumerOffsetSync_BackwardCompat verifies that
// migration-state files written before this feature land deserialize cleanly
// with both new fields defaulting to false (the no-op behavior).
func TestMigrationConfig_PauseConsumerOffsetSync_BackwardCompat(t *testing.T) {
	// JSON shaped exactly like a pre-feature state file: no pause_* keys at all.
	legacyJSON := `{
  "migrations": [
    {
      "migration_id": "mig-legacy-001",
      "current_state": "initialized",
      "kube_config_path": "/home/user/.kube/config",
      "source_bootstrap": "source-broker:9092",
      "cluster_bootstrap": "dest-broker:9092",
      "cluster_id": "lkc-legacy",
      "cluster_rest_endpoint": "https://pkc.us-east-1.aws.confluent.cloud:443",
      "cluster_link_name": "legacy-link",
      "topics": ["orders"],
      "cluster_link_topics": ["orders"],
      "cluster_link_configs": {"consumer.offset.sync.enable": "true"},
      "initial_cr_name": "gw-cr",
      "k8s_namespace": "confluent"
    }
  ],
  "kcp_build_info": {"version": "", "commit": "", "date": ""},
  "timestamp": "2026-01-01T00:00:00Z"
}`

	dir := t.TempDir()
	filePath := filepath.Join(dir, "legacy-state.json")
	require.NoError(t, os.WriteFile(filePath, []byte(legacyJSON), 0644))

	loaded, err := NewMigrationStateFromFile(filePath)
	require.NoError(t, err, "loading pre-feature state file must succeed")
	require.Len(t, loaded.Migrations, 1)

	assert.False(t, loaded.Migrations[0].PauseConsumerOffsetSync, "PauseConsumerOffsetSync should default to false for legacy state files")
	assert.False(t, loaded.Migrations[0].PauseConsumerOffsetSyncFlipped, "PauseConsumerOffsetSyncFlipped should default to false for legacy state files")
}

// TestMigrationConfig_PauseConsumerOffsetSync_ForwardCompat verifies that the
// false (no-op) state serializes with both fields present — no `omitempty`
// hiding the explicit choice.
func TestMigrationConfig_PauseConsumerOffsetSync_ForwardCompat(t *testing.T) {
	state := NewMigrationState()
	state.Migrations = []MigrationConfig{
		{
			MigrationId:                    "mig-explicit-false",
			PauseConsumerOffsetSync:        false,
			PauseConsumerOffsetSyncFlipped: false,
		},
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "migration-state.json")
	require.NoError(t, state.WriteToFile(filePath))

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	contents := string(data)

	assert.Contains(t, contents, `"pause_consumer_offset_sync"`, "field should be present in JSON even when false")
	assert.Contains(t, contents, `"pause_consumer_offset_sync_flipped"`, "flipped field should be present in JSON even when false")
}

// legacyFencedRouteCR is a minimal fenced gateway CR shaped like the file the
// pre-38f5c974 kcp snapshotted into MigrationConfig.FencedCrYAML: the initial
// CR with a fence block already injected onto the named route.
const legacyFencedRouteCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: my-gateway-cr
spec:
  routes:
    - name: migration-route
      endpoint: gateway:9595
      fence:
        scope: ALL
        errorCode: BROKER_NOT_AVAILABLE
`

// TestMigrationConfig_FenceRoutes_BackwardCompat verifies that a
// migration-state.json written before FenceRoutes existed (fenced_cr_yaml
// instead, no fence_routes key) backfills FenceRoutes from the legacy field
// on load, recovering the fenced route name from the snapshotted fenced CR.
// Without this, a migration already past StateFenced when kcp upgrades could
// never resume: every fenced-family resume re-fences at bootstrap (see
// EventExpireFence), and gateway.FenceRoutes refuses an empty route list.
func TestMigrationConfig_FenceRoutes_BackwardCompat(t *testing.T) {
	legacyJSON := `{
  "migrations": [
    {
      "migration_id": "mig-legacy-fence",
      "current_state": "fenced",
      "initial_cr_name": "my-gateway-cr",
      "k8s_namespace": "confluent",
      "fenced_cr_yaml": "` + base64.StdEncoding.EncodeToString([]byte(legacyFencedRouteCR)) + `"
    }
  ],
  "kcp_build_info": {"version": "", "commit": "", "date": ""},
  "timestamp": "2026-01-01T00:00:00Z"
}`

	dir := t.TempDir()
	filePath := filepath.Join(dir, "legacy-fence-state.json")
	require.NoError(t, os.WriteFile(filePath, []byte(legacyJSON), 0644))

	loaded, err := NewMigrationStateFromFile(filePath)
	require.NoError(t, err, "loading a pre-FenceRoutes state file must succeed")
	require.Len(t, loaded.Migrations, 1)

	assert.Equal(t, []string{"migration-route"}, loaded.Migrations[0].FenceRoutes,
		"FenceRoutes should be recovered from the legacy fenced_cr_yaml snapshot")
}

// TestMigrationConfig_FenceRoutes_PrefersPersistedOverLegacy verifies that a
// state file already carrying fence_routes is never overwritten by a stale
// (or coincidentally present) legacy fenced_cr_yaml key — the new field wins.
func TestMigrationConfig_FenceRoutes_PrefersPersistedOverLegacy(t *testing.T) {
	legacyJSON := `{
  "migrations": [
    {
      "migration_id": "mig-both-fields",
      "current_state": "fenced",
      "fence_routes": ["persisted-route"],
      "fenced_cr_yaml": "` + base64.StdEncoding.EncodeToString([]byte(legacyFencedRouteCR)) + `"
    }
  ],
  "kcp_build_info": {"version": "", "commit": "", "date": ""},
  "timestamp": "2026-01-01T00:00:00Z"
}`

	dir := t.TempDir()
	filePath := filepath.Join(dir, "both-fields-state.json")
	require.NoError(t, os.WriteFile(filePath, []byte(legacyJSON), 0644))

	loaded, err := NewMigrationStateFromFile(filePath)
	require.NoError(t, err)
	require.Len(t, loaded.Migrations, 1)

	assert.Equal(t, []string{"persisted-route"}, loaded.Migrations[0].FenceRoutes)
}

// TestMigrationConfig_FenceRoutes_LegacyFieldDroppedOnNextWrite verifies the
// recovery is a one-time backfill, not a field kcp keeps carrying forward:
// once loaded, writing the state back out produces the new fence_routes shape
// with no fenced_cr_yaml key.
func TestMigrationConfig_FenceRoutes_LegacyFieldDroppedOnNextWrite(t *testing.T) {
	legacyJSON := `{
  "migrations": [
    {
      "migration_id": "mig-legacy-fence",
      "current_state": "fenced",
      "fenced_cr_yaml": "` + base64.StdEncoding.EncodeToString([]byte(legacyFencedRouteCR)) + `"
    }
  ],
  "kcp_build_info": {"version": "", "commit": "", "date": ""},
  "timestamp": "2026-01-01T00:00:00Z"
}`

	dir := t.TempDir()
	filePath := filepath.Join(dir, "legacy-fence-state.json")
	require.NoError(t, os.WriteFile(filePath, []byte(legacyJSON), 0644))

	loaded, err := NewMigrationStateFromFile(filePath)
	require.NoError(t, err)

	rewritten := filepath.Join(dir, "rewritten-state.json")
	require.NoError(t, loaded.WriteToFile(rewritten))

	data, err := os.ReadFile(rewritten)
	require.NoError(t, err)
	contents := string(data)

	assert.Contains(t, contents, `"fence_routes"`, "rewritten state should carry the new field")
	assert.NotContains(t, contents, "fenced_cr_yaml", "rewritten state should drop the retired legacy field")
}

// TestMigrationConfig_FenceRoutes_LegacyFieldFailsLoudlyWhenUnrecoverable
// verifies a corrupt or unparseable legacy fenced_cr_yaml surfaces a load
// error rather than silently leaving FenceRoutes empty — an empty FenceRoutes
// would itself fail, but only much later, at the next fence apply.
func TestMigrationConfig_FenceRoutes_LegacyFieldFailsLoudlyWhenUnrecoverable(t *testing.T) {
	legacyJSON := `{
  "migrations": [
    {
      "migration_id": "mig-corrupt-fence",
      "current_state": "fenced",
      "fenced_cr_yaml": "` + base64.StdEncoding.EncodeToString([]byte("not: [valid")) + `"
    }
  ],
  "kcp_build_info": {"version": "", "commit": "", "date": ""},
  "timestamp": "2026-01-01T00:00:00Z"
}`

	dir := t.TempDir()
	filePath := filepath.Join(dir, "corrupt-fence-state.json")
	require.NoError(t, os.WriteFile(filePath, []byte(legacyJSON), 0644))

	_, err := NewMigrationStateFromFile(filePath)
	require.Error(t, err, "an unrecoverable legacy fenced_cr_yaml must fail the load, not silently drop FenceRoutes")
}

// skipIfWindows skips file-mode assertions on Windows, where POSIX permission
// bits are not meaningfully enforced.
func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file-mode semantics do not apply on Windows")
	}
}

// TestMigrationState_WriteToFile_NewFileHasOwnerOnlyPerms verifies the migration
// state file is written 0600 (owner read/write only), since it holds sensitive
// migration metadata. (R2)
func TestMigrationState_WriteToFile_NewFileHasOwnerOnlyPerms(t *testing.T) {
	skipIfWindows(t)

	path := filepath.Join(t.TempDir(), ".kcp-migration-state.json")
	require.NoError(t, NewMigrationState().WriteToFile(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "migration state file should be 0600")
}

// TestMigrationState_WriteToFile_StaleLooseTempDoesNotLeak guards against
// regressing to the old fixed-name temp scheme, whose bug was that a leftover
// <path>.tmp at 0644 from a crashed run kept its loose mode and the rename
// carried it through. We seed that exact condition and assert (a) the final
// migration state file is still 0600, (b) the stale fixed-name temp is left
// untouched -- proving the writer created its own unique temp rather than reusing
// the leftover one -- and (c) the writer's own unique temp is cleaned up on
// success. (R3 abuse case)
func TestMigrationState_WriteToFile_StaleLooseTempDoesNotLeak(t *testing.T) {
	skipIfWindows(t)

	dir := t.TempDir()
	path := filepath.Join(dir, ".kcp-migration-state.json")
	// Simulate a crash leaving a fixed-name temp at loose perms -- the exact
	// condition the old os.WriteFile(path+".tmp", ...) code mishandled.
	stale := path + ".tmp"
	require.NoError(t, os.WriteFile(stale, []byte("{}"), 0644))

	require.NoError(t, NewMigrationState().WriteToFile(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "stale 0644 temp must not leak into result")

	// The writer must not reuse the stale fixed-name temp: it should still exist,
	// untouched at 0644, proving a fresh unique temp was used instead.
	staleInfo, err := os.Stat(stale)
	require.NoError(t, err, "stale fixed-name temp should be left untouched")
	assert.Equal(t, os.FileMode(0o644), staleInfo.Mode().Perm(), "writer must not reuse the fixed-name temp")

	// The writer's own unique temp (..kcp-migration-state.json.tmp-*) must be
	// cleaned up on success.
	matches, err := filepath.Glob(filepath.Join(dir, "."+filepath.Base(path)+".tmp-*"))
	require.NoError(t, err)
	assert.Empty(t, matches, "unique temp file(s) left behind after success")
}

// TestMigrationState_WriteToFile_SecondWritePreservesPerms verifies a rewrite of
// an existing migration state file keeps mode 0600 rather than loosening it back
// to 0644. (R4 regression guard)
func TestMigrationState_WriteToFile_SecondWritePreservesPerms(t *testing.T) {
	skipIfWindows(t)

	path := filepath.Join(t.TempDir(), ".kcp-migration-state.json")
	for i := 0; i < 2; i++ {
		require.NoError(t, NewMigrationState().WriteToFile(path))
	}

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "perms should stay 0600 after second write")
}

// TestMigrationState_WriteToFile_TightensExistingLooseFile verifies an existing
// migration state file at 0644 is tightened to 0600 on the next write. (R5
// regression guard)
func TestMigrationState_WriteToFile_TightensExistingLooseFile(t *testing.T) {
	skipIfWindows(t)

	path := filepath.Join(t.TempDir(), ".kcp-migration-state.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0644))

	require.NoError(t, NewMigrationState().WriteToFile(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "existing 0644 file should be tightened to 0600")
}

// TestIsReversibleState pins the point-of-no-return boundary: the three
// pre-fence states are reversible, everything from StateFenced on is not, and an
// unknown value is treated as not reversible (fail safe).
func TestIsReversibleState(t *testing.T) {
	for _, s := range []string{StateUninitialized, StateInitialized, StateLagsOk} {
		assert.True(t, IsReversibleState(s), "%s should be reversible", s)
	}
	for _, s := range []string{StateFenced, StateOffsetSyncPaused, StateFenceVerified, StatePromoted, StateSwitched, "bogus"} {
		assert.False(t, IsReversibleState(s), "%s should not be reversible", s)
	}
}
