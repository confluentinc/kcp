package migration

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callRecorder collects the AlterConfigs/ListConfigs invocations a test made
// so each scenario can assert on call count, value passed, and ordering.
// listConfigs stays wired in so any test can assert it was NEVER called —
// the new pause/restore engines must not read the live cluster link to
// decide anything.
type callRecorder struct {
	listConfigs  int
	alterConfigs []clusterlink.ConfigAlteration
}

// newFakeClusterLink builds a mockClusterLinkService that records every
// AlterConfigs call and fails (returns an error) if ListConfigs is ever
// called — the contract under test is that the pause/restore engines never
// read the live cluster link to decide anything.
func newFakeClusterLink() (*mockClusterLinkService, *callRecorder) {
	rec := &callRecorder{}
	mock := &mockClusterLinkService{
		listConfigsFn: func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
			rec.listConfigs++
			return nil, fmt.Errorf("ListConfigs must not be called by the idempotent pause/restore engines")
		},
		alterConfigsFn: func(_ context.Context, _ clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			rec.alterConfigs = append(rec.alterConfigs, alts...)
			return nil
		},
	}
	return mock, rec
}

// newFailingAlterClusterLink is newFakeClusterLink but AlterConfigs returns
// alterErr instead of succeeding.
func newFailingAlterClusterLink(alterErr error) (*mockClusterLinkService, *callRecorder) {
	rec := &callRecorder{}
	mock := &mockClusterLinkService{
		listConfigsFn: func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
			rec.listConfigs++
			return nil, fmt.Errorf("ListConfigs must not be called by the idempotent pause/restore engines")
		},
		alterConfigsFn: func(_ context.Context, _ clusterlink.Config, alts []clusterlink.ConfigAlteration) error {
			rec.alterConfigs = append(rec.alterConfigs, alts...)
			return alterErr
		},
	}
	return mock, rec
}

func (rec *callRecorder) assertNoCalls(t *testing.T) {
	t.Helper()
	assert.Equal(t, 0, rec.listConfigs, "must not call ListConfigs")
	assert.Len(t, rec.alterConfigs, 0, "must not call AlterConfigs")
}

func (rec *callRecorder) assertNoListConfigs(t *testing.T) {
	t.Helper()
	assert.Equal(t, 0, rec.listConfigs, "must not call ListConfigs — no live-link read to decide")
}

func (rec *callRecorder) assertAltered(t *testing.T, name, value string) {
	t.Helper()
	require.Len(t, rec.alterConfigs, 1)
	assert.Equal(t, name, rec.alterConfigs[0].Name)
	assert.Equal(t, value, rec.alterConfigs[0].Value)
	assert.Equal(t, clusterlink.OperationSet, rec.alterConfigs[0].Operation)
}

// ---------------------------------------------------------------------------
// RestoreOffsetSync — the restore_offset_sync step. Plan-driven: it runs only
// when reconcile found a restore owed (config.RestoreOffsetSync), applies one
// idempotent SET of consumer.offset.sync.enable to the declared baseline, and
// never reads the live link. A failed SET fails the step, so a re-run retries it.
// ---------------------------------------------------------------------------

func TestRestoreOffsetSync_SetsToBaseline(t *testing.T) {
	for _, tc := range []struct{ baseline, want string }{
		{manifest.OffsetSyncBaselineEnabled, "true"},
		{manifest.OffsetSyncBaselineDisabled, "false"},
	} {
		t.Run(tc.baseline, func(t *testing.T) {
			cl, rec := newFakeClusterLink()
			cfg := &MigrationConfig{
				ClusterLinkName:            "link-1",
				PauseConsumerOffsetSync:    true,
				ConsumerOffsetSyncBaseline: tc.baseline,
				RestoreOffsetSync:          true,
			}

			require.NoError(t, NewMigrationActions(nil, cl).RestoreOffsetSync(context.Background(), cfg, nil))
			rec.assertAltered(t, offsetSyncEnableKey, tc.want)
			rec.assertNoListConfigs(t)
		})
	}
}

func TestRestoreOffsetSync_UnsetBaselineDefaultsToEnabled(t *testing.T) {
	cl, rec := newFakeClusterLink()
	cfg := &MigrationConfig{ClusterLinkName: "link-1", PauseConsumerOffsetSync: true, RestoreOffsetSync: true}

	require.NoError(t, NewMigrationActions(nil, cl).RestoreOffsetSync(context.Background(), cfg, nil))
	rec.assertAltered(t, offsetSyncEnableKey, "true")
}

func TestRestoreOffsetSync_NotInPlanIsNoop(t *testing.T) {
	// The pause is opted in, but reconcile found the link already at its
	// baseline: nothing to restore, and no call to the link.
	cl, rec := newFakeClusterLink()
	cfg := &MigrationConfig{ClusterLinkName: "link-1", PauseConsumerOffsetSync: true, RestoreOffsetSync: false}

	require.NoError(t, NewMigrationActions(nil, cl).RestoreOffsetSync(context.Background(), cfg, nil))
	rec.assertNoCalls(t)
}

func TestRestoreOffsetSync_ReappliesTheSameSet(t *testing.T) {
	// A re-run applies the same idempotent SET again.
	cl, rec := newFakeClusterLink()
	cfg := &MigrationConfig{
		ClusterLinkName:            "link-1",
		PauseConsumerOffsetSync:    true,
		ConsumerOffsetSyncBaseline: manifest.OffsetSyncBaselineEnabled,
		RestoreOffsetSync:          true,
	}
	actions := NewMigrationActions(nil, cl)

	require.NoError(t, actions.RestoreOffsetSync(context.Background(), cfg, nil))
	require.NoError(t, actions.RestoreOffsetSync(context.Background(), cfg, nil))
	require.Len(t, rec.alterConfigs, 2, "each call re-applies the same idempotent SET")
	assert.Equal(t, "true", rec.alterConfigs[0].Value)
	assert.Equal(t, "true", rec.alterConfigs[1].Value)
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

func TestRestoreOffsetSync_AlterFails_ReturnsError(t *testing.T) {
	cl, rec := newFailingAlterClusterLink(fmt.Errorf("503 unavailable"))
	cfg := &MigrationConfig{ClusterLinkName: "link-hard", PauseConsumerOffsetSync: true, RestoreOffsetSync: true}

	err := NewMigrationActions(nil, cl).RestoreOffsetSync(context.Background(), cfg, nil)

	require.Error(t, err, "a failed restore must fail the step so a re-run retries it")
	require.Len(t, rec.alterConfigs, 1, "the AlterConfigs attempt happened")
	assert.Contains(t, err.Error(), "link-hard", "the error names the cluster link")
	assert.Contains(t, err.Error(), offsetSyncEnableKey, "the error names the key")
	assert.Contains(t, err.Error(), "503 unavailable", "the error carries the cause")
}

// TestRestoreOffsetSync_CancelledCtx_ReturnsError: the restore runs on the
// run's own context, so a Ctrl-C before or during it fails the step (the FSM
// stays at switched) and the re-run retries it.
func TestRestoreOffsetSync_CancelledCtx_ReturnsError(t *testing.T) {
	mock := &mockClusterLinkService{
		alterConfigsFn: func(ctx context.Context, _ clusterlink.Config, _ []clusterlink.ConfigAlteration) error {
			return ctx.Err()
		},
	}
	cfg := &MigrationConfig{ClusterLinkName: "link-1", PauseConsumerOffsetSync: true, RestoreOffsetSync: true}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := NewMigrationActions(nil, mock).RestoreOffsetSync(ctx, cfg, nil)
	require.ErrorIs(t, err, context.Canceled)
}

// ---------------------------------------------------------------------------
// WarnIfPausedOnExecuteFailure — a single generic reminder gated only on the
// manifest-declared intent (config.PauseConsumerOffsetSync).
// ---------------------------------------------------------------------------

func TestWarnIfPaused_NotRequested_NoOutput(t *testing.T) {
	cfg := &MigrationConfig{ClusterLinkName: "link-1", PauseConsumerOffsetSync: false}

	out := captureStderr(t, func() {
		WarnIfPausedOnExecuteFailure(cfg, fmt.Errorf("some failure"))
	})
	assert.Empty(t, out, "no guidance when the operator never opted into pausing")
}

func TestWarnIfPaused_Requested_Warns(t *testing.T) {
	// The guidance is a single generic reminder gated only on the
	// manifest-declared PauseConsumerOffsetSync.
	cfg := &MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
	}

	out := captureStderr(t, func() {
		WarnIfPausedOnExecuteFailure(cfg, fmt.Errorf("some failure"))
	})

	assert.Contains(t, out, "link-1")
	assert.Contains(t, out, offsetSyncEnableKey)
	assert.Contains(t, out, "declared baseline")
	assert.Contains(t, out, "kcp migration execute")
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

	auth := clusterlink.BasicAuth{Username: "key", Password: "secret"}
	cl := BuildClusterLinkConfig(cfg, auth)
	assert.Equal(t, "https://pkc.us-east-1.aws.confluent.cloud:443", cl.RestEndpoint)
	assert.Equal(t, "lkc-abc", cl.ClusterID)
	assert.Equal(t, "link-xyz", cl.LinkName)
	assert.Equal(t, auth, cl.Auth, "carries the resolved Authenticator, not just a scalar key/secret pair")
	assert.Equal(t, []string{"orders", "users"}, cl.Topics)
}

// ---------------------------------------------------------------------------
// PauseOffsetSync — the pause_offset_sync stage's engine. Covers the opt-in
// pass-through, the plan-driven no-op (no cutover in flight this run), the
// idempotent disable, the drain window, and the AlterConfigs failure mode.
// The old ListConfigs-based drift refusal and PauseConsumerOffsetSyncFlipped
// marker are gone — pause is now a manifest+plan-driven idempotent apply.
// ---------------------------------------------------------------------------

// pauseActions builds a MigrationActions with only the cluster-link service
// wired; the pause engine never touches the gateway.
func pauseActions(cl *mockClusterLinkService) *MigrationActions {
	return NewMigrationActions(nil, cl)
}

func TestPauseOffsetSync_NotRequestedIsNoop(t *testing.T) {
	cl, rec := newFakeClusterLink()
	cfg := &MigrationConfig{ClusterLinkName: "link-1", PauseConsumerOffsetSync: false, FenceYAML: testFenceYAML}

	err := pauseActions(cl).PauseOffsetSync(context.Background(), cfg, clusterlink.BasicAuth{Username: "k", Password: "s"})
	require.NoError(t, err)
	rec.assertNoCalls(t)
}

func TestPauseOffsetSync_NoInFlightIsNoop(t *testing.T) {
	// FenceYAML=="" means reconcile emitted no cutover work this run — nothing
	// to pause. Plan-driven: read the plan, never the live link.
	cl, rec := newFakeClusterLink()
	cfg := &MigrationConfig{ClusterLinkName: "link-1", PauseConsumerOffsetSync: true, FenceYAML: ""}

	err := pauseActions(cl).PauseOffsetSync(context.Background(), cfg, clusterlink.BasicAuth{Username: "k", Password: "s"})
	require.NoError(t, err)
	rec.assertNoCalls(t)
}

func TestPauseOffsetSync_DisablesWhenInFlight(t *testing.T) {
	cl, rec := newFakeClusterLink()
	cfg := &MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
		FenceYAML:               testFenceYAML,
	}

	err := pauseActions(cl).PauseOffsetSync(context.Background(), cfg, clusterlink.BasicAuth{Username: "k", Password: "s"})
	require.NoError(t, err)
	rec.assertAltered(t, offsetSyncEnableKey, "false")
	rec.assertNoListConfigs(t)
}

func TestPauseOffsetSync_IdempotentOnResume(t *testing.T) {
	// Unlike the old marker-gated skip, a resume simply re-applies the same
	// idempotent SET rather than reading a flipped marker to decide whether
	// to skip.
	cl, rec := newFakeClusterLink()
	cfg := &MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
		FenceYAML:               testFenceYAML,
	}

	require.NoError(t, pauseActions(cl).PauseOffsetSync(context.Background(), cfg, clusterlink.BasicAuth{Username: "k", Password: "s"}))
	require.NoError(t, pauseActions(cl).PauseOffsetSync(context.Background(), cfg, clusterlink.BasicAuth{Username: "k", Password: "s"}))
	require.Len(t, rec.alterConfigs, 2, "each call re-applies the same idempotent SET")
	assert.Equal(t, "false", rec.alterConfigs[0].Value)
	assert.Equal(t, "false", rec.alterConfigs[1].Value)
}

func TestPauseOffsetSync_AlterFails_Surfaces(t *testing.T) {
	cl, _ := newFailingAlterClusterLink(fmt.Errorf("500 internal"))
	cfg := &MigrationConfig{
		ClusterLinkName:         "link-1",
		PauseConsumerOffsetSync: true,
		FenceYAML:               testFenceYAML,
	}

	err := pauseActions(cl).PauseOffsetSync(context.Background(), cfg, clusterlink.BasicAuth{Username: "k", Password: "s"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to disable")
}

// ---------------------------------------------------------------------------
// PauseOffsetSync drain window (--consumer-offset-sync-drain-duration): with a
// positive drain the stage holds BEFORE disabling sync, so the link can
// propagate the final frozen offsets. A ctx cancellation during the drain
// leaves sync still enabled (nothing altered).
// ---------------------------------------------------------------------------

func TestPauseOffsetSync_Drain_WaitsBeforeDisabling(t *testing.T) {
	cl, rec := newFakeClusterLink()
	const drain = 40 * time.Millisecond
	cfg := &MigrationConfig{
		ClusterLinkName:                 "link-1",
		PauseConsumerOffsetSync:         true,
		FenceYAML:                       testFenceYAML,
		ConsumerOffsetSyncDrainDuration: drain,
	}

	start := time.Now()
	err := pauseActions(cl).PauseOffsetSync(context.Background(), cfg, clusterlink.BasicAuth{Username: "k", Password: "s"})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, drain, "must hold for the full drain before disabling")
	rec.assertAltered(t, offsetSyncEnableKey, "false")
}

func TestPauseOffsetSync_Drain_ContextCancelledLeavesSyncEnabled(t *testing.T) {
	cl, rec := newFakeClusterLink()
	cfg := &MigrationConfig{
		ClusterLinkName:                 "link-1",
		PauseConsumerOffsetSync:         true,
		FenceYAML:                       testFenceYAML,
		ConsumerOffsetSyncDrainDuration: time.Hour, // long enough that cancel wins
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the drain begins

	err := pauseActions(cl).PauseOffsetSync(ctx, cfg, clusterlink.BasicAuth{Username: "k", Password: "s"})

	require.ErrorIs(t, err, context.Canceled)
	assert.Len(t, rec.alterConfigs, 0, "sync must NOT be disabled when the drain is cancelled")
}

func TestPauseOffsetSync_Drain_ZeroDisablesImmediately(t *testing.T) {
	cl, rec := newFakeClusterLink()
	cfg := &MigrationConfig{
		ClusterLinkName:                 "link-1",
		PauseConsumerOffsetSync:         true,
		FenceYAML:                       testFenceYAML,
		ConsumerOffsetSyncDrainDuration: 0, // no drain — prior behaviour
	}

	err := pauseActions(cl).PauseOffsetSync(context.Background(), cfg, clusterlink.BasicAuth{Username: "k", Password: "s"})

	require.NoError(t, err)
	rec.assertAltered(t, offsetSyncEnableKey, "false")
}
