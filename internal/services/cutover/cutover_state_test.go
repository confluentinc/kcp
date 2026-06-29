package cutover

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCutoverState_WriteAndRead_RoundTrip(t *testing.T) {
	state := NewCutoverState()
	state.Cutovers = []CutoverConfig{
		{
			CutoverId:           "mig-001",
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
			CutoverId:        "mig-002",
			CurrentState:     "executing",
			SourceBootstrap:  "source-broker-2:9092",
			ClusterBootstrap: "dest-broker-2:9092",
			ClusterId:        "lkc-def456",
			ClusterLinkName:  "my-link-2",
			Topics:           []string{"events"},
		},
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "cutover-state.json")

	require.NoError(t, state.WriteToFile(filePath), "WriteToFile failed")

	loaded, err := NewCutoverStateFromFile(filePath)
	require.NoError(t, err, "NewCutoverStateFromFile failed")
	require.Len(t, loaded.Cutovers, 2, "expected 2 cutovers")

	// Use reflect.DeepEqual via testify for full struct comparison
	assert.Equal(t, state.Cutovers[0], loaded.Cutovers[0])
	assert.Equal(t, state.Cutovers[1], loaded.Cutovers[1])

	// Verify build info round-trips (will be empty strings in test, but should match)
	assert.Equal(t, state.KcpBuildInfo.Version, loaded.KcpBuildInfo.Version)
	assert.Equal(t, state.KcpBuildInfo.Commit, loaded.KcpBuildInfo.Commit)
	assert.False(t, loaded.Timestamp.IsZero(), "expected non-zero Timestamp after round-trip")
}

func TestCutoverState_WriteToFile_AtomicWrite(t *testing.T) {
	state := NewCutoverState()
	state.Cutovers = []CutoverConfig{
		{CutoverId: "mig-001", CurrentState: "initialized"},
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "cutover-state.json")

	require.NoError(t, state.WriteToFile(filePath), "WriteToFile failed")

	// Verify the final file exists
	_, err := os.Stat(filePath)
	require.NoError(t, err, "expected state file to exist")

	// Verify no temp file remains after successful write. The writer uses a
	// uniquely-named temp (..cutover-state.json.tmp-*), so match its glob
	// rather than the legacy fixed name the code no longer creates.
	matches, err := filepath.Glob(filepath.Join(dir, "."+filepath.Base(filePath)+".tmp-*"))
	require.NoError(t, err)
	assert.Empty(t, matches, "expected no temp file to remain after successful write")
}

func TestCutoverState_UpsertCutover_Insert(t *testing.T) {
	state := NewCutoverState()
	state.Cutovers = []CutoverConfig{
		{CutoverId: "mig-001", CurrentState: "initialized", Topics: []string{"topic-a"}},
	}

	newCutover := CutoverConfig{
		CutoverId:    "mig-002",
		CurrentState: "executing",
		Topics:       []string{"topic-b"},
	}

	state.UpsertCutover(newCutover)

	require.Len(t, state.Cutovers, 2, "expected 2 cutovers after insert")
	assert.Equal(t, "mig-001", state.Cutovers[0].CutoverId)
	assert.Equal(t, "mig-002", state.Cutovers[1].CutoverId)
	assert.Equal(t, "executing", state.Cutovers[1].CurrentState)
}

func TestCutoverState_UpsertCutover_Update(t *testing.T) {
	state := NewCutoverState()
	state.Cutovers = []CutoverConfig{
		{CutoverId: "mig-001", CurrentState: "initialized", Topics: []string{"topic-a"}},
		{CutoverId: "mig-002", CurrentState: "initialized", Topics: []string{"topic-b"}},
	}

	updated := CutoverConfig{
		CutoverId:    "mig-001",
		CurrentState: "executing",
		Topics:       []string{"topic-a", "topic-c"},
	}

	state.UpsertCutover(updated)

	require.Len(t, state.Cutovers, 2, "expected 2 cutovers after update (not duplicated)")
	assert.Equal(t, "executing", state.Cutovers[0].CurrentState)
	assert.Len(t, state.Cutovers[0].Topics, 2, "expected updated cutover to have 2 topics")
	// Verify the other cutover was not affected
	assert.Equal(t, "mig-002", state.Cutovers[1].CutoverId)
	assert.Equal(t, "initialized", state.Cutovers[1].CurrentState)
}

func TestCutoverState_GetCutoverById_Found(t *testing.T) {
	state := NewCutoverState()
	state.Cutovers = []CutoverConfig{
		{CutoverId: "mig-001", CurrentState: "initialized", ClusterId: "lkc-111"},
		{CutoverId: "mig-002", CurrentState: "executing", ClusterId: "lkc-222", Topics: []string{"orders"}},
	}

	result, err := state.GetCutoverById("mig-002")
	require.NoError(t, err, "GetCutoverById returned unexpected error")
	require.NotNil(t, result)

	assert.Equal(t, "mig-002", result.CutoverId)
	assert.Equal(t, "executing", result.CurrentState)
	assert.Equal(t, "lkc-222", result.ClusterId)
	assert.Equal(t, []string{"orders"}, result.Topics)

	// Verify defensive copy: modifying the returned pointer must not affect the original.
	// GetCutoverById copies the struct before returning a pointer to it,
	// so mutations to the result should be isolated from the state's slice.
	result.CurrentState = "completed"
	assert.Equal(t, "executing", state.Cutovers[1].CurrentState,
		"modifying returned pointer should not affect original state")
}

func TestCutoverState_GetCutoverById_NotFound(t *testing.T) {
	state := NewCutoverState()
	state.Cutovers = []CutoverConfig{
		{CutoverId: "mig-001", CurrentState: "initialized"},
	}

	result, err := state.GetCutoverById("non-existent")
	require.Error(t, err, "expected error for non-existent cutover ID")
	assert.Nil(t, result, "expected nil result for non-existent cutover ID")
}

func TestNewCutoverStateFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "invalid.json")

	require.NoError(t, os.WriteFile(filePath, []byte("not valid json {{{"), 0644), "failed to write test file")

	result, err := NewCutoverStateFromFile(filePath)
	require.Error(t, err, "expected error for invalid JSON")
	assert.Nil(t, result, "expected nil result for invalid JSON")
}

func TestNewCutoverStateFromFile_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "does-not-exist.json")

	result, err := NewCutoverStateFromFile(filePath)
	require.Error(t, err, "expected error for non-existent file")
	assert.Nil(t, result, "expected nil result for non-existent file")
}

// TestCutoverConfig_PauseConsumerOffsetSync_RoundTrip verifies that both new
// fields persist through Write+Read with their explicit values preserved.
func TestCutoverConfig_PauseConsumerOffsetSync_RoundTrip(t *testing.T) {
	state := NewCutoverState()
	state.Cutovers = []CutoverConfig{
		{
			CutoverId:                      "mig-pause-001",
			CurrentState:                   "initialized",
			ClusterLinkConfigs:             map[string]string{"consumer.offset.sync.enable": "true"},
			PauseConsumerOffsetSync:        true,
			PauseConsumerOffsetSyncFlipped: false,
		},
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "cutover-state.json")

	require.NoError(t, state.WriteToFile(filePath), "WriteToFile failed")

	loaded, err := NewCutoverStateFromFile(filePath)
	require.NoError(t, err, "NewCutoverStateFromFile failed")
	require.Len(t, loaded.Cutovers, 1)

	assert.True(t, loaded.Cutovers[0].PauseConsumerOffsetSync, "PauseConsumerOffsetSync should round-trip as true")
	assert.False(t, loaded.Cutovers[0].PauseConsumerOffsetSyncFlipped, "PauseConsumerOffsetSyncFlipped should round-trip as false")
}

// TestCutoverConfig_PauseConsumerOffsetSync_BackwardCompat verifies that
// cutover-state files written before this feature land deserialize cleanly
// with both new fields defaulting to false (the no-op behavior).
func TestCutoverConfig_PauseConsumerOffsetSync_BackwardCompat(t *testing.T) {
	// JSON shaped exactly like a pre-feature state file: no pause_* keys at all.
	legacyJSON := `{
  "cutovers": [
    {
      "cutover_id": "mig-legacy-001",
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

	loaded, err := NewCutoverStateFromFile(filePath)
	require.NoError(t, err, "loading pre-feature state file must succeed")
	require.Len(t, loaded.Cutovers, 1)

	assert.False(t, loaded.Cutovers[0].PauseConsumerOffsetSync, "PauseConsumerOffsetSync should default to false for legacy state files")
	assert.False(t, loaded.Cutovers[0].PauseConsumerOffsetSyncFlipped, "PauseConsumerOffsetSyncFlipped should default to false for legacy state files")
}

// TestCutoverConfig_PauseConsumerOffsetSync_ForwardCompat verifies that the
// false (no-op) state serializes with both fields present — no `omitempty`
// hiding the explicit choice.
func TestCutoverConfig_PauseConsumerOffsetSync_ForwardCompat(t *testing.T) {
	state := NewCutoverState()
	state.Cutovers = []CutoverConfig{
		{
			CutoverId:                      "mig-explicit-false",
			PauseConsumerOffsetSync:        false,
			PauseConsumerOffsetSyncFlipped: false,
		},
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "cutover-state.json")
	require.NoError(t, state.WriteToFile(filePath))

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	contents := string(data)

	assert.Contains(t, contents, `"pause_consumer_offset_sync"`, "field should be present in JSON even when false")
	assert.Contains(t, contents, `"pause_consumer_offset_sync_flipped"`, "flipped field should be present in JSON even when false")
}

// skipIfWindows skips file-mode assertions on Windows, where POSIX permission
// bits are not meaningfully enforced.
func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file-mode semantics do not apply on Windows")
	}
}

// TestCutoverState_WriteToFile_NewFileHasOwnerOnlyPerms verifies the cutover
// state file is written 0600 (owner read/write only), since it holds sensitive
// cutover metadata. (R2)
func TestCutoverState_WriteToFile_NewFileHasOwnerOnlyPerms(t *testing.T) {
	skipIfWindows(t)

	path := filepath.Join(t.TempDir(), ".kcp-cutover-state.json")
	require.NoError(t, NewCutoverState().WriteToFile(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "cutover state file should be 0600")
}

// TestCutoverState_WriteToFile_StaleLooseTempDoesNotLeak guards against
// regressing to the old fixed-name temp scheme, whose bug was that a leftover
// <path>.tmp at 0644 from a crashed run kept its loose mode and the rename
// carried it through. We seed that exact condition and assert (a) the final
// cutover state file is still 0600, (b) the stale fixed-name temp is left
// untouched -- proving the writer created its own unique temp rather than reusing
// the leftover one -- and (c) the writer's own unique temp is cleaned up on
// success. (R3 abuse case)
func TestCutoverState_WriteToFile_StaleLooseTempDoesNotLeak(t *testing.T) {
	skipIfWindows(t)

	dir := t.TempDir()
	path := filepath.Join(dir, ".kcp-cutover-state.json")
	// Simulate a crash leaving a fixed-name temp at loose perms -- the exact
	// condition the old os.WriteFile(path+".tmp", ...) code mishandled.
	stale := path + ".tmp"
	require.NoError(t, os.WriteFile(stale, []byte("{}"), 0644))

	require.NoError(t, NewCutoverState().WriteToFile(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "stale 0644 temp must not leak into result")

	// The writer must not reuse the stale fixed-name temp: it should still exist,
	// untouched at 0644, proving a fresh unique temp was used instead.
	staleInfo, err := os.Stat(stale)
	require.NoError(t, err, "stale fixed-name temp should be left untouched")
	assert.Equal(t, os.FileMode(0o644), staleInfo.Mode().Perm(), "writer must not reuse the fixed-name temp")

	// The writer's own unique temp (..kcp-cutover-state.json.tmp-*) must be
	// cleaned up on success.
	matches, err := filepath.Glob(filepath.Join(dir, "."+filepath.Base(path)+".tmp-*"))
	require.NoError(t, err)
	assert.Empty(t, matches, "unique temp file(s) left behind after success")
}

// TestCutoverState_WriteToFile_SecondWritePreservesPerms verifies a rewrite of
// an existing cutover state file keeps mode 0600 rather than loosening it back
// to 0644. (R4 regression guard)
func TestCutoverState_WriteToFile_SecondWritePreservesPerms(t *testing.T) {
	skipIfWindows(t)

	path := filepath.Join(t.TempDir(), ".kcp-cutover-state.json")
	for i := 0; i < 2; i++ {
		require.NoError(t, NewCutoverState().WriteToFile(path))
	}

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "perms should stay 0600 after second write")
}

// TestCutoverState_WriteToFile_TightensExistingLooseFile verifies an existing
// cutover state file at 0644 is tightened to 0600 on the next write. (R5
// regression guard)
func TestCutoverState_WriteToFile_TightensExistingLooseFile(t *testing.T) {
	skipIfWindows(t)

	path := filepath.Join(t.TempDir(), ".kcp-cutover-state.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0644))

	require.NoError(t, NewCutoverState().WriteToFile(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "existing 0644 file should be tightened to 0600")
}
