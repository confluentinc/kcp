package migration

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callRecorder collects the AlterConfigs invocations a test made so each
// scenario can assert on call count, value passed, and ordering.
type callRecorder struct {
	listConfigs  int
	alterConfigs []clusterlink.ConfigAlteration
	persist      int
}

func newRecordingMock(t *testing.T, listValue string, listErr error, alterErr error) (*mockClusterLinkService, *callRecorder) {
	t.Helper()
	rec := &callRecorder{}
	mock := &mockClusterLinkService{
		listConfigsFn: func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
			rec.listConfigs++
			if listErr != nil {
				return nil, listErr
			}
			if listValue == "<missing>" {
				return map[string]string{"other.key": "v"}, nil
			}
			return map[string]string{"consumer.offset.sync.enable": listValue}, nil
		},
		alterConfigsFn: func(_ context.Context, _ clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			rec.alterConfigs = append(rec.alterConfigs, alts...)
			return alterErr
		},
	}
	return mock, rec
}

func makePersist(rec *callRecorder, persistErr error) func() error {
	return func() error {
		rec.persist++
		return persistErr
	}
}

// ---------------------------------------------------------------------------
// PauseOffsetSync — the pause_offset_sync stage's engine (absorbed from the
// former pre-FSM DisableOffsetSync bookend). Covers the opt-in pass-through,
// idempotent resume, drift refusal, flip+inline-persist, and failure modes.
// ---------------------------------------------------------------------------

// pauseActions builds a MigrationActions with only the cluster-link service
// wired; the pause engine never touches the gateway.
func pauseActions(cl *mockClusterLinkService) *MigrationActions {
	return NewMigrationActions(nil, cl)
}

func TestPauseOffsetSync_FlagOff_PassesThrough(t *testing.T) {
	mock, rec := newRecordingMock(t, "true", nil, nil)
	cfg := &MigrationConfig{ClusterLinkName: "link-1", PauseConsumerOffsetSync: false}

	err := pauseActions(mock).PauseOffsetSync(context.Background(), cfg, "k", "s", makePersist(rec, nil))
	require.NoError(t, err)
	assert.Equal(t, 0, rec.listConfigs, "must not contact the cluster link when flag is off")
	assert.Len(t, rec.alterConfigs, 0, "must not flip when flag is off")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped)
	assert.Equal(t, 0, rec.persist)
}

func TestPauseOffsetSync_AlreadyFlipped_SkipsIdempotently(t *testing.T) {
	// Resume, or a legacy state file whose pause ran pre-FSM: a prior run
	// flipped the config; the stage must pass through without any API call.
	mock, rec := newRecordingMock(t, "false", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSync:        true,
		PauseConsumerOffsetSyncFlipped: true,
		CurrentState:                   StateFenced,
	}

	err := pauseActions(mock).PauseOffsetSync(context.Background(), cfg, "k", "s", makePersist(rec, nil))
	require.NoError(t, err)
	assert.Equal(t, 0, rec.listConfigs, "resume must skip drift detection")
	assert.Len(t, rec.alterConfigs, 0, "resume must skip the re-flip")
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped, "marker stays true")
}

func TestPauseOffsetSync_HappyPath_FlipsAndPersistsInline(t *testing.T) {
	mock, rec := newRecordingMock(t, "true", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
		CurrentState:            StateFenced,
	}

	var flippedAtPersist bool
	persist := func() error {
		rec.persist++
		flippedAtPersist = cfg.PauseConsumerOffsetSyncFlipped
		return nil
	}

	err := pauseActions(mock).PauseOffsetSync(context.Background(), cfg, "k", "s", persist)
	require.NoError(t, err)
	assert.Equal(t, 1, rec.listConfigs, "drift detection must query the live state")
	require.Len(t, rec.alterConfigs, 1)
	assert.Equal(t, "consumer.offset.sync.enable", rec.alterConfigs[0].Name)
	assert.Equal(t, "false", rec.alterConfigs[0].Value)
	assert.Equal(t, clusterlink.OperationSet, rec.alterConfigs[0].Operation)
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped, "marker must flip after AlterConfigs success")
	assert.Equal(t, 1, rec.persist, "marker must persist inline before returning")
	assert.True(t, flippedAtPersist, "the inline persist must write the already-set marker")
}

func TestPauseOffsetSync_DriftDetected_RefusesNamingBothCauses(t *testing.T) {
	// Abuse case: the marker says kcp never flipped, yet the link already has
	// the sync disabled. Either a previous kcp attempt was interrupted before
	// recording the flip, or the config was changed externally — refuse and
	// name both causes so the operator is not stranded. Config VALUES stay out
	// of the message (key names only).
	mock, rec := newRecordingMock(t, "false", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:         "link-drifty",
		PauseConsumerOffsetSync: true,
	}

	err := pauseActions(mock).PauseOffsetSync(context.Background(), cfg, "k", "s", makePersist(rec, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "link-drifty")
	assert.Contains(t, err.Error(), "consumer.offset.sync.enable")
	assert.Contains(t, err.Error(), "interrupted", "refusal must name the crashed-prior-attempt cause")
	assert.Contains(t, err.Error(), "externally", "refusal must name the external-change cause")
	assert.NotContains(t, err.Error(), `"false"`, "refusal must not echo the observed config value")
	assert.Len(t, rec.alterConfigs, 0, "no AlterConfigs call on drift refusal")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped)
	assert.Equal(t, 0, rec.persist)
}

func TestPauseOffsetSync_DriftDetected_RefusesOnAbsentKey(t *testing.T) {
	// Abuse case: malformed or unexpected ListConfigs response without the key.
	mock, rec := newRecordingMock(t, "<missing>", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:         "link-keyless",
		PauseConsumerOffsetSync: true,
	}

	err := pauseActions(mock).PauseOffsetSync(context.Background(), cfg, "k", "s", makePersist(rec, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no consumer.offset.sync.enable key")
	assert.Len(t, rec.alterConfigs, 0)
}

func TestPauseOffsetSync_AlterFails_NoMutation(t *testing.T) {
	// AlterConfigs failure must not leave the state file marker set.
	mock, rec := newRecordingMock(t, "true", nil, fmt.Errorf("500 internal"))
	cfg := &MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
	}

	err := pauseActions(mock).PauseOffsetSync(context.Background(), cfg, "k", "s", makePersist(rec, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to disable")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped, "marker must NOT be set on AlterConfigs failure")
	assert.Equal(t, 0, rec.persist, "persist must NOT run if alter failed")
}

func TestPauseOffsetSync_AlterSucceeds_PersistFails_Surfaces(t *testing.T) {
	// Edge case: the cluster link IS flipped but the state file write fails.
	mock, rec := newRecordingMock(t, "true", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
	}

	err := pauseActions(mock).PauseOffsetSync(context.Background(), cfg, "k", "s", makePersist(rec, fmt.Errorf("disk full")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to persist marker")
	assert.Contains(t, err.Error(), "recovery", "error must include recovery hint")
	assert.Contains(t, err.Error(), "link-1", "error must name the cluster link")
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped, "marker IS set in memory because AlterConfigs succeeded")
	require.Len(t, rec.alterConfigs, 1)
}

func TestPauseOffsetSync_ListConfigsFails_Surfaces(t *testing.T) {
	mock, rec := newRecordingMock(t, "", fmt.Errorf("network error"), nil)
	cfg := &MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
	}

	err := pauseActions(mock).PauseOffsetSync(context.Background(), cfg, "k", "s", makePersist(rec, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "drift detection")
	assert.Contains(t, err.Error(), "link-1")
	assert.Len(t, rec.alterConfigs, 0)
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped)
}

// ---------------------------------------------------------------------------
// RestoreOffsetSync — covers AE1 (happy path), AE5 (soft-fail), R13.
// ---------------------------------------------------------------------------

func TestRestoreOffsetSync_NotFlipped_NoOp(t *testing.T) {
	mock, rec := newRecordingMock(t, "", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: false,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	assert.Len(t, rec.alterConfigs, 0, "no restore call if nothing was flipped")
	assert.Equal(t, 0, rec.persist)
}

// newDiffMock builds a mock that returns a caller-supplied current state from
// ListConfigs. Used by the diff-mode RestoreOffsetSync tests where the
// existing newRecordingMock helper (single-value listValue) is too narrow.
func newDiffMock(currentConfigs map[string]string, listErr, alterErr error) (*mockClusterLinkService, *callRecorder) {
	rec := &callRecorder{}
	mock := &mockClusterLinkService{
		listConfigsFn: func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
			rec.listConfigs++
			if listErr != nil {
				return nil, listErr
			}
			out := make(map[string]string, len(currentConfigs))
			for k, v := range currentConfigs {
				out[k] = v
			}
			return out, nil
		},
		alterConfigsFn: func(_ context.Context, _ clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			rec.alterConfigs = append(rec.alterConfigs, alts...)
			return alterErr
		},
	}
	return mock, rec
}

// captureStderr swaps os.Stderr for a pipe while fn runs, returning everything
// that fn wrote to stderr. Used by the soft-fail remediation-message tests so
// we can assert which key names appear in the operator-facing output.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	require.NoError(t, w.Close())
	return <-done
}

// TestRestoreOffsetSync_HappyPath_ClearsMarker (AE1): snapshot has the toggle
// and filters; the disable bookend set the toggle to "false" and CC's
// side-effect cleared the filters. Restore re-applies both, in sorted order.
func TestRestoreOffsetSync_HappyPath_ClearsMarker(t *testing.T) {
	mock, rec := newDiffMock(
		map[string]string{"consumer.offset.sync.enable": "false"},
		nil, nil,
	)
	snapshot := map[string]string{
		"consumer.offset.sync.enable":   "true",
		"consumer.offset.group.filters": `{"groups":["app-*"]}`,
	}
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSync:        true,
		PauseConsumerOffsetSyncFlipped: true,
		CurrentState:                   StateSwitched,
		ClusterLinkConfigs:             snapshot,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	assert.Equal(t, 1, rec.listConfigs, "ListConfigs must run when snapshot is non-empty")
	require.Len(t, rec.alterConfigs, 2, "must restore both filters and enable toggle")
	// Toggle is ordered LAST so a partial restore failure leaves the link in
	// the safer state (filters re-applied with sync still disabled).
	assert.Equal(t, "consumer.offset.group.filters", rec.alterConfigs[0].Name)
	assert.Equal(t, `{"groups":["app-*"]}`, rec.alterConfigs[0].Value)
	assert.Equal(t, clusterlink.OperationSet, rec.alterConfigs[0].Operation)
	assert.Equal(t, "consumer.offset.sync.enable", rec.alterConfigs[1].Name)
	assert.Equal(t, "true", rec.alterConfigs[1].Value)
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped, "marker must clear after successful restore")
	assert.Equal(t, 1, rec.persist)
}

func TestRestoreOffsetSync_HappyPath_MultipleSyncKeysRestoredSorted(t *testing.T) {
	mock, rec := newDiffMock(
		map[string]string{"consumer.offset.sync.enable": "false"},
		nil, nil,
	)
	snapshot := map[string]string{
		"consumer.offset.sync.enable":   "true",
		"consumer.offset.sync.ms":       "1000",
		"consumer.offset.group.filters": `{"groups":["app-*"]}`,
	}
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
		ClusterLinkConfigs:             snapshot,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.Len(t, rec.alterConfigs, 3)
	// Non-toggle keys appear in sorted order; toggle key is forced to the end
	// so a partial-restore failure leaves sync disabled rather than re-enabled
	// with stale filters.
	assert.Equal(t, "consumer.offset.group.filters", rec.alterConfigs[0].Name)
	assert.Equal(t, "consumer.offset.sync.ms", rec.alterConfigs[1].Name)
	assert.Equal(t, "consumer.offset.sync.enable", rec.alterConfigs[2].Name)
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped)
}

// TestRestoreOffsetSync_PrefixScope (AE2): snapshot also carries non-prefix
// keys (e.g. bootstrap.servers); restore must never touch them.
func TestRestoreOffsetSync_PrefixScope_OnlyConsumerOffsetKeys(t *testing.T) {
	mock, rec := newDiffMock(
		map[string]string{"bootstrap.servers": "broker:9092"},
		nil, nil,
	)
	snapshot := map[string]string{
		"bootstrap.servers":           "broker:9092",
		"consumer.offset.sync.enable": "true",
	}
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
		ClusterLinkConfigs:             snapshot,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.Len(t, rec.alterConfigs, 1, "only the consumer.offset.* key is restored")
	assert.Equal(t, "consumer.offset.sync.enable", rec.alterConfigs[0].Name)
	for _, a := range rec.alterConfigs {
		assert.NotEqual(t, "bootstrap.servers", a.Name, "non-prefix key must never appear")
	}
}

func TestRestoreOffsetSync_EmptyDiff_NoAlterCall_ClearsMarker(t *testing.T) {
	// Current state already matches snapshot — nothing to restore. Marker
	// still clears (cleanup) and persist still runs.
	mock, rec := newDiffMock(
		map[string]string{
			"consumer.offset.sync.enable":   "true",
			"consumer.offset.group.filters": `{"groups":["app-*"]}`,
		},
		nil, nil,
	)
	snapshot := map[string]string{
		"consumer.offset.sync.enable":   "true",
		"consumer.offset.group.filters": `{"groups":["app-*"]}`,
	}
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
		ClusterLinkConfigs:             snapshot,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	assert.Equal(t, 1, rec.listConfigs)
	assert.Len(t, rec.alterConfigs, 0, "no AlterConfigs call when snapshot equals current")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped, "marker still clears on empty diff")
	assert.Equal(t, 1, rec.persist)
}

func TestRestoreOffsetSync_OperatorChangedPostDisable_Preserved(t *testing.T) {
	// Operator deliberately set filters to a new value AFTER disable. Current
	// has a non-empty, non-"false" value different from snapshot — treat it
	// as a deliberate operator change and leave it alone.
	mock, rec := newDiffMock(
		map[string]string{
			"consumer.offset.sync.enable":   "false",
			"consumer.offset.group.filters": `{"groups":["operator-override-*"]}`,
		},
		nil, nil,
	)
	snapshot := map[string]string{
		"consumer.offset.sync.enable":   "true",
		"consumer.offset.group.filters": `{"groups":["app-*"]}`,
	}
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
		ClusterLinkConfigs:             snapshot,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.Len(t, rec.alterConfigs, 1, "only the toggle is restored; operator's filters value is preserved")
	assert.Equal(t, "consumer.offset.sync.enable", rec.alterConfigs[0].Name)
	for _, a := range rec.alterConfigs {
		assert.NotEqual(t, "consumer.offset.group.filters", a.Name, "operator's post-disable value must not be overwritten")
	}
}

func TestRestoreOffsetSync_OperatorChangedPreDisable_Overwritten(t *testing.T) {
	// Operator changed filters BEFORE disable. Snapshot has init-time value.
	// Post-disable, filters are missing (cleared by CC side-effect). Restore
	// re-applies the init snapshot — the operator's interim pre-disable
	// change is intentionally overwritten. (AE4.)
	mock, rec := newDiffMock(
		map[string]string{"consumer.offset.sync.enable": "false"},
		nil, nil,
	)
	snapshot := map[string]string{
		"consumer.offset.sync.enable":   "true",
		"consumer.offset.group.filters": `{"groups":["app-*"]}`,
	}
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
		ClusterLinkConfigs:             snapshot,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.Len(t, rec.alterConfigs, 2)
	assert.Equal(t, "consumer.offset.group.filters", rec.alterConfigs[0].Name)
	assert.Equal(t, `{"groups":["app-*"]}`, rec.alterConfigs[0].Value)
}

func TestRestoreOffsetSync_LegacyFallback_NilClusterLinkConfigs(t *testing.T) {
	// AE3: ClusterLinkConfigs is nil → single SET, no ListConfigs.
	mock, rec := newRecordingMock(t, "", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	assert.Equal(t, 0, rec.listConfigs, "legacy fallback must not call ListConfigs")
	require.Len(t, rec.alterConfigs, 1)
	assert.Equal(t, "consumer.offset.sync.enable", rec.alterConfigs[0].Name)
	assert.Equal(t, "true", rec.alterConfigs[0].Value)
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped)
}

func TestRestoreOffsetSync_LegacyFallback_EmptyClusterLinkConfigs(t *testing.T) {
	// Empty (non-nil) map behaves the same as nil.
	mock, rec := newRecordingMock(t, "", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
		ClusterLinkConfigs:             map[string]string{},
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	assert.Equal(t, 0, rec.listConfigs)
	require.Len(t, rec.alterConfigs, 1)
	assert.Equal(t, "consumer.offset.sync.enable", rec.alterConfigs[0].Name)
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped)
}

func TestRestoreOffsetSync_ListConfigsFails_SoftFailKeepsMarker(t *testing.T) {
	mock, rec := newDiffMock(nil, fmt.Errorf("network error"), nil)
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
		ClusterLinkConfigs:             map[string]string{"consumer.offset.sync.enable": "true"},
	}

	out := captureStderr(t, func() {
		RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	})

	assert.Equal(t, 1, rec.listConfigs, "ListConfigs was attempted")
	assert.Len(t, rec.alterConfigs, 0, "AlterConfigs must not run when ListConfigs failed")
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped, "marker stays true on soft-fail")
	assert.Equal(t, 0, rec.persist, "no persist call when restore failed")
	assert.Contains(t, out, "link-1", "remediation message names the cluster link")
}

func TestRestoreOffsetSync_AlterFailsMultiKey_RemediationNamesAllKeys(t *testing.T) {
	// AE5 + R9: AlterConfigs returns 503 on the first per-key call. The bookend
	// short-circuits the loop (toggle would have been last, so the safer state
	// — sync still disabled — is preserved). The remediation message lists
	// every owed key so the operator can re-apply manually.
	mock, rec := newDiffMock(
		map[string]string{},
		nil,
		fmt.Errorf("503 unavailable"),
	)
	snapshot := map[string]string{
		"consumer.offset.sync.enable":   "true",
		"consumer.offset.group.filters": `{"groups":["app-*"]}`,
	}
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-soft",
		PauseConsumerOffsetSyncFlipped: true,
		ClusterLinkConfigs:             snapshot,
	}

	out := captureStderr(t, func() {
		RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	})

	require.Len(t, rec.alterConfigs, 1, "loop short-circuits on first per-key failure")
	assert.Equal(t, "consumer.offset.group.filters", rec.alterConfigs[0].Name, "non-toggle key is attempted first; toggle would have been last")
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped, "marker MUST stay true on soft-fail so state file knows restore is owed")
	assert.Equal(t, 0, rec.persist)
	assert.Contains(t, out, "consumer.offset.sync.enable", "remediation message names the toggle (still owed)")
	assert.Contains(t, out, "consumer.offset.group.filters", "remediation message names the filters key (still owed)")
	assert.Contains(t, out, "link-soft", "remediation message names the cluster link")
	assert.Contains(t, out, "Applied: none", "remediation message reports nothing was applied")
}

func TestRestoreOffsetSync_AlterFails_SoftFailKeepsMarker(t *testing.T) {
	// R13: restore failure is soft. Marker stays true so re-run knows.
	mock, rec := newRecordingMock(t, "", nil, fmt.Errorf("503 unavailable"))
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-soft",
		ClusterId:                      "lkc-soft",
		PauseConsumerOffsetSyncFlipped: true,
		CurrentState:                   StateSwitched,
	}

	// No panic, no error return. The function takes no error to return.
	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))

	require.Len(t, rec.alterConfigs, 1, "the AlterConfigs attempt happened")
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped, "marker MUST stay true on soft-fail so state file knows restore is owed")
	assert.Equal(t, 0, rec.persist, "no persist call when restore failed")
}

func TestRestoreOffsetSync_AlterSucceedsPersistFails_StillCorrects(t *testing.T) {
	mock, rec := newRecordingMock(t, "", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, fmt.Errorf("disk full")))
	require.Len(t, rec.alterConfigs, 1, "restore call must have happened")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped, "in-memory marker cleared even if persist failed (cluster link is correct)")
}

// TestRestoreOffsetSync_ParentCtxCancelled_StillRestores verifies the
// soft-fail semantic survives parent-ctx cancellation. The migration may
// complete successfully and only then have its ctx cancelled (signal arriving
// between Execute returning and the bookend running, future caller that
// cancels on completion, etc.). RestoreOffsetSync must use a fresh ctx so the
// AlterConfigs PUT actually runs.
func TestRestoreOffsetSync_ParentCtxCancelled_StillRestores(t *testing.T) {
	rec := &callRecorder{}
	var ctxErrAtCall error
	mock := &mockClusterLinkService{
		alterConfigsFn: func(ctx context.Context, _ clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			ctxErrAtCall = ctx.Err()
			rec.alterConfigs = append(rec.alterConfigs, alts...)
			return nil
		},
	}
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
		CurrentState:                   StateSwitched,
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()

	RestoreOffsetSync(parentCtx, mock, clusterlink.Config{}, cfg, makePersist(rec, nil))

	require.Len(t, rec.alterConfigs, 1, "AlterConfigs must be called even when parent ctx is cancelled")
	assert.NoError(t, ctxErrAtCall, "AlterConfigs must receive a non-cancelled ctx at the moment of call (soft-fail intent)")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped, "marker cleared after successful restore")
}

// ---------------------------------------------------------------------------
// WarnIfPausedOnExecuteFailure — post-failure guidance shaped by state.
// ---------------------------------------------------------------------------

func TestWarnIfPaused_MarkerClear_NoOutput(t *testing.T) {
	// A clean rollback lands at initialized with the marker cleared — there
	// is nothing to warn about.
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		CurrentState:                   StateInitialized,
		PauseConsumerOffsetSyncFlipped: false,
	}

	out := captureStderr(t, func() {
		WarnIfPausedOnExecuteFailure(cfg, fmt.Errorf("some failure"))
	})
	assert.Empty(t, out, "no guidance when nothing is flipped")
}

func TestWarnIfPaused_StuckAtRollbackSource_UrgentObservableState(t *testing.T) {
	// The gateway is still fenced with sync paused — whether from a failed
	// rollback or a routine resumable stop (ctx-cancel mid-detection, a
	// verify fetch error). The urgent copy describes the observable state
	// and points at re-running; it must not claim a rollback failed, because
	// it cannot know that.
	for _, state := range []string{StateFenced, StateOffsetSyncPaused} {
		t.Run(state, func(t *testing.T) {
			cfg := &MigrationConfig{
				ClusterLinkName:                "link-1",
				CurrentState:                   state,
				PauseConsumerOffsetSyncFlipped: true,
			}

			out := captureStderr(t, func() {
				WarnIfPausedOnExecuteFailure(cfg, fmt.Errorf("some failure"))
			})

			assert.Contains(t, out, "still fenced", "urgent copy names the fenced gateway")
			assert.Contains(t, out, "blocked", "urgent copy names the client impact")
			assert.Contains(t, out, "kcp migration execute", "urgent copy points at the re-run")
			assert.Contains(t, out, "link-1")
			assert.Contains(t, out, "consumer.offset.sync.enable")
			assert.NotContains(t, out, "rollback failed",
				"the same state also arises from routine resumable stops")
			assert.NotContains(t, out, "restore will run after a successful switchover",
				"the soft restore-owed wording undersells a fenced gateway")
		})
	}
}

func TestWarnIfPaused_FenceVerified_UrgentBlockedGuidance(t *testing.T) {
	// A promote failure rests at fence_verified with the fenced CR still live:
	// clients are blocked, and the guidance must say so rather than undersell
	// it as restore-owed. Topics are not yet promoted, so the manual abort
	// (re-apply the initial CR) is still a safe escape hatch.
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		CurrentState:                   StateFenceVerified,
		PauseConsumerOffsetSyncFlipped: true,
	}

	out := captureStderr(t, func() {
		WarnIfPausedOnExecuteFailure(cfg, fmt.Errorf("some failure"))
	})

	assert.Contains(t, out, "still fenced", "urgent copy names the fenced gateway")
	assert.Contains(t, out, "blocked", "urgent copy names the client impact")
	assert.Contains(t, out, "kcp migration execute", "urgent copy points at the re-run")
	assert.Contains(t, out, "re-apply the initial gateway CR",
		"pre-promotion the manual abort is still safe and must be offered")
	assert.Contains(t, out, "link-1")
	assert.Contains(t, out, "consumer.offset.sync.enable")
	assert.NotContains(t, out, "restore will run after a successful switchover",
		"the soft restore-owed wording undersells a fenced gateway")
}

func TestWarnIfPaused_Promoted_UrgentBlockedGuidance(t *testing.T) {
	// A switch failure rests at promoted with the fenced CR still live:
	// clients are blocked, but topics are already promoted, so routing them
	// back to the source would diverge data. The copy must acknowledge the
	// blocked traffic AND warn against the manual unfence.
	cfg := &MigrationConfig{
		ClusterLinkName:                "link-1",
		CurrentState:                   StatePromoted,
		PauseConsumerOffsetSyncFlipped: true,
	}

	out := captureStderr(t, func() {
		WarnIfPausedOnExecuteFailure(cfg, fmt.Errorf("some failure"))
	})

	assert.Contains(t, out, "still fenced", "urgent copy names the fenced gateway")
	assert.Contains(t, out, "blocked", "urgent copy names the client impact")
	assert.Contains(t, out, "complete the switchover",
		"post-promotion the only client-unblocking path is forward")
	assert.Contains(t, out, "Do not re-apply the initial gateway CR",
		"post-promotion a manual unfence would diverge data and must be warned against")
	assert.Contains(t, out, "link-1")
	assert.Contains(t, out, "consumer.offset.sync.enable")
	assert.NotContains(t, out, "restore will run after a successful switchover",
		"the soft restore-owed wording undersells a fenced gateway")
}

func TestWarnIfPaused_UnfencedShapes_RestoreOwed(t *testing.T) {
	// Shapes where the gateway is genuinely not blocking clients keep the
	// softer restore-owed wording: before the fence goes up (initialized and
	// lags_ok — a completed rollback whose restore is still owed, or the
	// legacy pre-FSM-pause cohort failing before the fence) and after the
	// switchover CR replaces it (switched).
	for _, state := range []string{StateInitialized, StateLagsOk, StateSwitched} {
		t.Run(state, func(t *testing.T) {
			cfg := &MigrationConfig{
				ClusterLinkName:                "link-1",
				CurrentState:                   state,
				PauseConsumerOffsetSyncFlipped: true,
			}

			out := captureStderr(t, func() {
				WarnIfPausedOnExecuteFailure(cfg, fmt.Errorf("some failure"))
			})

			assert.Contains(t, out, "restore will run after a successful switchover")
			assert.Contains(t, out, "link-1")
			assert.Contains(t, out, "consumer.offset.sync.enable")
			assert.NotContains(t, out, "still fenced",
				"the urgent fenced-gateway wording is wrong for these shapes")
		})
	}
}

// ---------------------------------------------------------------------------
// BuildClusterLinkConfig — small but worth pinning.
// ---------------------------------------------------------------------------

func TestBuildClusterLinkConfig_CarriesAllFields(t *testing.T) {
	cfg := &MigrationConfig{
		ClusterRestEndpoint: "https://pkc.us-east-1.aws.confluent.cloud:443",
		ClusterId:           "lkc-abc",
		ClusterLinkName:     "link-xyz",
		Topics:              []string{"orders", "users"},
	}

	cl := BuildClusterLinkConfig(cfg, "key", "secret")
	assert.Equal(t, "https://pkc.us-east-1.aws.confluent.cloud:443", cl.RestEndpoint)
	assert.Equal(t, "lkc-abc", cl.ClusterID)
	assert.Equal(t, "link-xyz", cl.LinkName)
	assert.Equal(t, "key", cl.APIKey)
	assert.Equal(t, "secret", cl.APISecret)
	assert.Equal(t, []string{"orders", "users"}, cl.Topics)
}

// ---------------------------------------------------------------------------
// PauseOffsetSync drain window (--consumer-offset-sync-drain-duration): with a
// positive drain the stage holds AFTER the drift check and BEFORE disabling
// sync, so the link can propagate the final frozen offsets. A ctx cancellation
// during the drain leaves sync still enabled (nothing flipped).
// ---------------------------------------------------------------------------

func TestPauseOffsetSync_Drain_WaitsBeforeDisabling(t *testing.T) {
	mock, rec := newRecordingMock(t, "true", nil, nil)
	const drain = 40 * time.Millisecond
	cfg := &MigrationConfig{
		ClusterLinkName:                 "link-1",
		PauseConsumerOffsetSync:         true,
		ConsumerOffsetSyncDrainDuration: drain,
		CurrentState:                    StateFenced,
	}

	start := time.Now()
	err := pauseActions(mock).PauseOffsetSync(context.Background(), cfg, "k", "s", makePersist(rec, nil))
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, drain, "must hold for the full drain before disabling")
	assert.Equal(t, 1, rec.listConfigs, "drift check still runs before the drain")
	require.Len(t, rec.alterConfigs, 1, "sync must still be disabled after the drain")
	assert.Equal(t, "false", rec.alterConfigs[0].Value)
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped)
}

func TestPauseOffsetSync_Drain_ContextCancelledLeavesSyncEnabled(t *testing.T) {
	mock, rec := newRecordingMock(t, "true", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:                 "link-1",
		PauseConsumerOffsetSync:         true,
		ConsumerOffsetSyncDrainDuration: time.Hour, // long enough that cancel wins
		CurrentState:                    StateFenced,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the drain begins

	err := pauseActions(mock).PauseOffsetSync(ctx, cfg, "k", "s", makePersist(rec, nil))

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, rec.listConfigs, "drift check runs before the drain")
	assert.Len(t, rec.alterConfigs, 0, "sync must NOT be disabled when the drain is cancelled")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped, "nothing flipped on cancellation")
	assert.Equal(t, 0, rec.persist)
}

func TestPauseOffsetSync_Drain_ZeroDisablesImmediately(t *testing.T) {
	mock, rec := newRecordingMock(t, "true", nil, nil)
	cfg := &MigrationConfig{
		ClusterLinkName:                 "link-1",
		PauseConsumerOffsetSync:         true,
		ConsumerOffsetSyncDrainDuration: 0, // no drain — prior behaviour
		CurrentState:                    StateFenced,
	}

	err := pauseActions(mock).PauseOffsetSync(context.Background(), cfg, "k", "s", makePersist(rec, nil))

	require.NoError(t, err)
	require.Len(t, rec.alterConfigs, 1, "sync disabled without any drain")
	assert.Equal(t, "false", rec.alterConfigs[0].Value)
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped)
}
