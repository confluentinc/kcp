package migration

import (
	"context"
	"fmt"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/types"
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
// DisableOffsetSync — covers AE6 (no-op when flag off), AE3 (resume), R10
// (no-op after switched), AE1 (happy path), AE4 (drift), R12 (alter failure).
// ---------------------------------------------------------------------------

func TestDisableOffsetSync_FlagOff_NoOp(t *testing.T) {
	mock, rec := newRecordingMock(t, "true", nil, nil)
	cfg := &types.MigrationConfig{ClusterLinkName: "link-1", PauseConsumerOffsetSync: false}

	err := DisableOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.NoError(t, err)
	assert.Equal(t, 0, rec.listConfigs, "must not contact the cluster link when flag is off")
	assert.Len(t, rec.alterConfigs, 0, "must not flip when flag is off")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped)
}

func TestDisableOffsetSync_AlreadySwitched_NoOp(t *testing.T) {
	// R10: re-running execute on a finished migration must not call any APIs.
	mock, rec := newRecordingMock(t, "true", nil, nil)
	cfg := &types.MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
		CurrentState:            types.StateSwitched,
	}

	err := DisableOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.NoError(t, err)
	assert.Equal(t, 0, rec.listConfigs)
	assert.Len(t, rec.alterConfigs, 0)
}

func TestDisableOffsetSync_AlreadyFlipped_Resume(t *testing.T) {
	// AE3 resume: a prior run flipped the config but failed before switchover.
	mock, rec := newRecordingMock(t, "false", nil, nil)
	cfg := &types.MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSync:        true,
		PauseConsumerOffsetSyncFlipped: true,
		CurrentState:                   types.StateFenced,
	}

	err := DisableOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.NoError(t, err)
	assert.Equal(t, 0, rec.listConfigs, "resume must skip drift detection")
	assert.Len(t, rec.alterConfigs, 0, "resume must skip the re-flip")
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped, "marker stays true")
}

func TestDisableOffsetSync_HappyPath_FlipsAndPersists(t *testing.T) {
	mock, rec := newRecordingMock(t, "true", nil, nil)
	cfg := &types.MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
		CurrentState:            types.StateUninitialized,
	}

	err := DisableOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.NoError(t, err)
	assert.Equal(t, 1, rec.listConfigs, "drift detection must query the live state")
	require.Len(t, rec.alterConfigs, 1)
	assert.Equal(t, "consumer.offset.sync.enable", rec.alterConfigs[0].Name)
	assert.Equal(t, "false", rec.alterConfigs[0].Value)
	assert.Equal(t, clusterlink.OperationSet, rec.alterConfigs[0].Operation)
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped, "marker must flip after AlterConfigs success")
	assert.Equal(t, 1, rec.persist, "marker must persist before returning")
}

func TestDisableOffsetSync_DriftDetected_RefusesOnFalse(t *testing.T) {
	// AE4: cluster link drifted (init recorded true, but live is false).
	mock, rec := newRecordingMock(t, "false", nil, nil)
	cfg := &types.MigrationConfig{
		ClusterLinkName:         "link-drifty",
		PauseConsumerOffsetSync: true,
	}

	err := DisableOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "link-drifty")
	assert.Contains(t, err.Error(), "drift")
	assert.Contains(t, err.Error(), `"false"`)
	assert.Len(t, rec.alterConfigs, 0, "no AlterConfigs call on drift detection")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped)
	assert.Equal(t, 0, rec.persist)
}

func TestDisableOffsetSync_DriftDetected_RefusesOnAbsentKey(t *testing.T) {
	mock, rec := newRecordingMock(t, "<missing>", nil, nil)
	cfg := &types.MigrationConfig{
		ClusterLinkName:         "link-keyless",
		PauseConsumerOffsetSync: true,
	}

	err := DisableOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no consumer.offset.sync.enable key")
	assert.Len(t, rec.alterConfigs, 0)
}

func TestDisableOffsetSync_AlterFails_NoMutation(t *testing.T) {
	// R12: AlterConfigs failure must not leave the state file marker set.
	mock, rec := newRecordingMock(t, "true", nil, fmt.Errorf("500 internal"))
	cfg := &types.MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
	}

	err := DisableOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to disable")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped, "marker must NOT be set on AlterConfigs failure")
	assert.Equal(t, 0, rec.persist, "persist must NOT run if alter failed")
}

func TestDisableOffsetSync_AlterSucceeds_PersistFails_Surfaces(t *testing.T) {
	// Edge case from plan: cluster link IS flipped but state file write fails.
	mock, rec := newRecordingMock(t, "true", nil, nil)
	cfg := &types.MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
	}

	err := DisableOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, fmt.Errorf("disk full")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to persist marker")
	assert.Contains(t, err.Error(), "recovery", "error must include recovery hint")
	assert.Contains(t, err.Error(), "link-1", "error must name the cluster link")
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped, "marker IS set in memory because AlterConfigs succeeded")
	require.Len(t, rec.alterConfigs, 1)
}

func TestDisableOffsetSync_ListConfigsFails_Surfaces(t *testing.T) {
	mock, rec := newRecordingMock(t, "", fmt.Errorf("network error"), nil)
	cfg := &types.MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
	}

	err := DisableOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
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
	cfg := &types.MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: false,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	assert.Len(t, rec.alterConfigs, 0, "no restore call if nothing was flipped")
	assert.Equal(t, 0, rec.persist)
}

func TestRestoreOffsetSync_HappyPath_ClearsMarker(t *testing.T) {
	mock, rec := newRecordingMock(t, "", nil, nil)
	cfg := &types.MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSync:        true,
		PauseConsumerOffsetSyncFlipped: true,
		CurrentState:                   types.StateSwitched,
	}

	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))
	require.Len(t, rec.alterConfigs, 1)
	assert.Equal(t, "true", rec.alterConfigs[0].Value, "restore writes value=true")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped, "marker must clear after successful restore")
	assert.Equal(t, 1, rec.persist)
}

func TestRestoreOffsetSync_AlterFails_SoftFailKeepsMarker(t *testing.T) {
	// R13: restore failure is soft. Marker stays true so re-run knows.
	mock, rec := newRecordingMock(t, "", nil, fmt.Errorf("503 unavailable"))
	cfg := &types.MigrationConfig{
		ClusterLinkName:                "link-soft",
		ClusterId:                      "lkc-soft",
		PauseConsumerOffsetSyncFlipped: true,
		CurrentState:                   types.StateSwitched,
	}

	// No panic, no error return. The function takes no error to return.
	RestoreOffsetSync(context.Background(), mock, clusterlink.Config{}, cfg, makePersist(rec, nil))

	require.Len(t, rec.alterConfigs, 1, "the AlterConfigs attempt happened")
	assert.True(t, cfg.PauseConsumerOffsetSyncFlipped, "marker MUST stay true on soft-fail so state file knows restore is owed")
	assert.Equal(t, 0, rec.persist, "no persist call when restore failed")
}

func TestRestoreOffsetSync_AlterSucceedsPersistFails_StillCorrects(t *testing.T) {
	mock, rec := newRecordingMock(t, "", nil, nil)
	cfg := &types.MigrationConfig{
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
	cfg := &types.MigrationConfig{
		ClusterLinkName:                "link-1",
		PauseConsumerOffsetSyncFlipped: true,
		CurrentState:                   types.StateSwitched,
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()

	RestoreOffsetSync(parentCtx, mock, clusterlink.Config{}, cfg, makePersist(rec, nil))

	require.Len(t, rec.alterConfigs, 1, "AlterConfigs must be called even when parent ctx is cancelled")
	assert.NoError(t, ctxErrAtCall, "AlterConfigs must receive a non-cancelled ctx at the moment of call (soft-fail intent)")
	assert.False(t, cfg.PauseConsumerOffsetSyncFlipped, "marker cleared after successful restore")
}

// ---------------------------------------------------------------------------
// BuildClusterLinkConfig — small but worth pinning.
// ---------------------------------------------------------------------------

func TestBuildClusterLinkConfig_CarriesAllFields(t *testing.T) {
	cfg := &types.MigrationConfig{
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
