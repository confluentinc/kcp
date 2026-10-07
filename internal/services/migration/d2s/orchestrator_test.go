package d2s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testRun is a real orchestrator over a mocked gateway and a fake world, with its terminal output captured.
type testRun struct {
	orch   *D2SOrchestrator
	config *migration.MigrationConfig
	out    *strings.Builder
}

func newTestRun(gw gateway.Service, deps Dependencies) *testRun {
	config := &migration.MigrationConfig{MigrationId: "test-d2s", K8sNamespace: "confluent", InitialCrName: "gateway-initial", Route: "migration-route"}
	actions := NewD2SActions(gw, deps)
	out := &strings.Builder{}
	actions.reporter = &reporter{out: out, err: io.Discard}
	return &testRun{orch: NewD2SOrchestrator(config, actions), config: config, out: out}
}

func (r *testRun) execute(ctx context.Context, res *migplan.Result) error {
	return r.orch.Execute(ctx, res, testPolicy())
}

func (r *testRun) state() string { return r.orch.fsm.Current() }

func stepFor(t *testing.T, event string) WorkflowStep {
	t.Helper()
	for _, s := range canonicalWorkflow {
		if s.Event == event {
			return s
		}
	}
	t.Fatalf("no workflow step for %s", event)
	return WorkflowStep{}
}

func rollbackRules(t *testing.T) any {
	t.Helper()
	v, err := gateway.FragmentValue([]byte(testRollbackFenceYAML), "rules")
	require.NoError(t, err)
	return v
}

func TestD2SOrchestrator_Execute_WalksEveryStepFromUninitialized(t *testing.T) {
	rec := &patchRecorder{}
	run := newTestRun(rec.service(), cleanWorld().deps())

	require.NoError(t, run.execute(context.Background(), convertResult()))

	assert.Equal(t, StateSwitched, run.state())
	assert.Equal(t, []string{"rules", ""}, rec.fields(), "the fence sets the route's rules; the switch replaces the whole route")
	assert.Contains(t, run.out.String(), "Route conversion complete!")
}

func TestD2SOrchestrator_Execute_RefusedReconcilePlanFailsAndStaysUninitialized(t *testing.T) {
	rec := &patchRecorder{}
	run := newTestRun(rec.service(), cleanWorld().deps())
	res := &migplan.Result{Mode: "convert", Refused: true, Reasons: []string{"orders: its mirror is ACTIVE, not promoted"}}

	err := run.execute(context.Background(), res)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "orders: its mirror is ACTIVE, not promoted")
	assert.Equal(t, StateUninitialized, run.state())
	assert.Empty(t, rec.patches)
	assert.Empty(t, run.config.FenceYAML)
}

func TestD2SOrchestrator_Execute_MissingDependenciesFailBeforeAnyStep(t *testing.T) {
	rec := &patchRecorder{}
	deps := cleanWorld().deps()
	deps.Gather = nil
	run := newTestRun(rec.service(), deps)

	err := run.execute(context.Background(), convertResult())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Gather")
	assert.Equal(t, StateUninitialized, run.state())
	assert.Empty(t, rec.patches)
}

// A failure while this run's fence is up rolls back: abort_fence restores reconcile's rollback rules.
func TestD2SOrchestrator_Execute_AFailureWhileFencedRollsBack(t *testing.T) {
	for _, step := range []string{EventVerifyFence, EventSyncOffsets} {
		t.Run(step, func(t *testing.T) {
			t.Setenv(killpoint.FailEnvVar, step)
			rec := &patchRecorder{}
			run := newTestRun(rec.service(), cleanWorld().deps())

			err := run.execute(context.Background(), convertResult())

			require.Error(t, err)
			assert.Contains(t, err.Error(), killpoint.FailEnvVar, "the failure is the hook's")
			assert.Equal(t, StateInitialized, run.state())
			require.Len(t, rec.patches, 2, "the fence, then the rollback")
			assert.Equal(t, "rules", rec.patches[1].Field)
			assert.Equal(t, rollbackRules(t), rec.patches[1].Value, "the rollback sets the rules to reconcile's rollback target")
			assert.Contains(t, run.out.String(), "failed — removing fence")
			assert.Contains(t, run.out.String(), "Gateway unfenced")
		})
	}
}

// Decision 10 puts every fenced state before the switch in abort_fence's sources.
func TestD2SOrchestrator_AbortFenceEdgeCoversEveryFencedState(t *testing.T) {
	run := newTestRun((&patchRecorder{}).service(), cleanWorld().deps())
	for _, s := range []string{StateFenced, StateFenceVerified, StateOffsetsSynced} {
		run.orch.fsm.SetState(s)
		assert.True(t, run.orch.fsm.Can(EventAbortFence), "abort_fence from %s", s)
	}
	for _, s := range []string{StateUninitialized, StateInitialized, StateSwitched} {
		run.orch.fsm.SetState(s)
		assert.False(t, run.orch.fsm.Can(EventAbortFence), "no abort_fence from %s", s)
	}
}

// Decision 25: at switch the route is still fenced or already static, and neither can be safely undone.
func TestD2SOrchestrator_Execute_SwitchFailureKeepsTheFence(t *testing.T) {
	t.Run("the switch patch fails", func(t *testing.T) {
		rec := &patchRecorder{failAt: map[int]error{2: fmt.Errorf("k8s API unavailable")}}
		run := newTestRun(rec.service(), cleanWorld().deps())

		err := run.execute(context.Background(), convertResult())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "k8s API unavailable")
		assert.Equal(t, StateOffsetsSynced, run.state())
		assert.Len(t, rec.patches, 2, "the fence and the failed switch: no rollback patch")
		assert.Contains(t, run.out.String(), "keeping the fence")
		assert.NotContains(t, run.out.String(), "removing fence")
	})
	t.Run("the failure hook fails the switch", func(t *testing.T) {
		t.Setenv(killpoint.FailEnvVar, EventSwitch)
		rec := &patchRecorder{}
		run := newTestRun(rec.service(), cleanWorld().deps())

		require.Error(t, run.execute(context.Background(), convertResult()))

		assert.Equal(t, StateOffsetsSynced, run.state())
		assert.Len(t, rec.patches, 1, "the fence only")
		assert.Contains(t, run.out.String(), "keeping the fence")
	})
}

// fencedAtStartRun fails this run's fence step, with the route fenced by an earlier run or not.
func fencedAtStartRun(t *testing.T, fencedAtStart bool) (*testRun, *patchRecorder, error) {
	t.Helper()
	t.Setenv(killpoint.FailEnvVar, EventFence)
	rec := &patchRecorder{}
	run := newTestRun(rec.service(), cleanWorld().deps())
	res := convertResult()
	res.FencedAtStart = fencedAtStart
	return run, rec, run.execute(context.Background(), res)
}

func TestD2SOrchestrator_Execute_FencedAtStart_AFenceStepFailureUnfences(t *testing.T) {
	run, rec, err := fencedAtStartRun(t, true)

	require.Error(t, err)
	require.Len(t, rec.patches, 1, "the unfence only: this run never reached its own fence")
	assert.Equal(t, rollbackRules(t), rec.patches[0].Value)
	assert.Equal(t, StateInitialized, run.state(), "no FSM edge: the gateway alone is put right")
	assert.Contains(t, run.out.String(), "removing fence")
	assert.Contains(t, run.out.String(), "Gateway unfenced")
}

func TestD2SOrchestrator_Execute_NotFencedAtStart_AFenceStepFailureLiftsNothing(t *testing.T) {
	run, rec, err := fencedAtStartRun(t, false)

	require.Error(t, err)
	assert.Empty(t, rec.patches)
	assert.NotContains(t, run.out.String(), "removing fence")
}

// unconfirmedFenceRun's fence patch lands but its acceptance wait fails; restoreFails makes the patch removing
// it fail too.
func unconfirmedFenceRun(t *testing.T, restoreFails bool) (*testRun, *patchRecorder, error) {
	t.Helper()
	rec := &patchRecorder{}
	if restoreFails {
		rec.failAt = map[int]error{2: fmt.Errorf("k8s API unavailable")}
	}
	gw := rec.service()
	accepts := 0
	gw.waitForGatewayAcceptedFn = func(context.Context, string, string, time.Duration, time.Duration) error {
		accepts++
		if accepts == 1 {
			return fmt.Errorf("operator never reconciled")
		}
		return nil
	}
	run := newTestRun(gw, cleanWorld().deps())
	return run, rec, run.execute(context.Background(), convertResult())
}

func TestD2SOrchestrator_Execute_AnUnconfirmedFenceIsRemoved(t *testing.T) {
	run, rec, err := unconfirmedFenceRun(t, false)

	require.ErrorIs(t, err, migration.ErrFenceUnconfirmed)
	require.Len(t, rec.patches, 2, "the fence, then the patch removing it")
	assert.Equal(t, rollbackRules(t), rec.patches[1].Value)
	assert.Contains(t, run.out.String(), "removing it")
	assert.Contains(t, run.out.String(), "Fence removed")
	assert.Equal(t, StateInitialized, run.state(), "the fence transition never completed")
}

func TestD2SOrchestrator_Execute_AnUnconfirmedFenceWhoseRemovalFailsReportsBoth(t *testing.T) {
	_, rec, err := unconfirmedFenceRun(t, true)

	require.ErrorIs(t, err, migration.ErrFenceUnconfirmed)
	assert.Len(t, rec.patches, 2)
	assert.Contains(t, err.Error(), "operator never reconciled")
	assert.Contains(t, err.Error(), "k8s API unavailable")
}

// Review Focus 2: a rollback whose unfence fails leaves the FSM fenced and says so in the error.
func TestD2SOrchestrator_Execute_RollbackUnfenceFailsReportsBoth(t *testing.T) {
	t.Setenv(killpoint.FailEnvVar, EventVerifyFence)
	rec := &patchRecorder{failAt: map[int]error{2: fmt.Errorf("k8s API unavailable")}}
	run := newTestRun(rec.service(), cleanWorld().deps())

	err := run.execute(context.Background(), convertResult())

	require.Error(t, err)
	assert.Contains(t, err.Error(), killpoint.FailEnvVar, "the step's own failure")
	assert.Contains(t, err.Error(), "k8s API unavailable", "the rollback's failure")
	assert.Contains(t, err.Error(), "removing the fence failed")
	assert.Equal(t, StateFenced, run.state(), "abort_fence was cancelled, so the run stays fenced")
	assert.Len(t, rec.patches, 2)
}

func TestD2SOrchestrator_Execute_RollbackNotAllowedKeepsTheFence(t *testing.T) {
	t.Setenv(killpoint.FailEnvVar, EventVerifyFence)
	rec := &patchRecorder{}
	run := newTestRun(rec.service(), cleanWorld().deps())
	res := convertResult()
	res.RollbackAllowed = false

	require.Error(t, run.execute(context.Background(), res))

	assert.Len(t, rec.patches, 1, "the fence only")
	assert.Contains(t, run.out.String(), "keeping the fence")
	assert.Equal(t, StateFenced, run.state())
}

func TestD2SOrchestrator_Execute_KillPointCancelsAfterTheNamedState(t *testing.T) {
	t.Setenv(killpoint.EnvVar, StateFenced)
	rec := &patchRecorder{}
	run := newTestRun(rec.service(), cleanWorld().deps())

	err := run.execute(context.Background(), convertResult())

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, StateFenced, run.state())
	assert.Len(t, rec.patches, 1, "an abrupt exit leaves the fence for the next run")
}

func TestRollbackReason(t *testing.T) {
	verify, sync := stepFor(t, EventVerifyFence), stepFor(t, EventSyncOffsets)

	assert.Equal(t, "Direct commits detected", rollbackReason(verify, fmt.Errorf("x: %w", groupoffsets.ErrRogueCommits)))
	assert.Equal(t, "Conversion check failed after the fence", rollbackReason(verify, fmt.Errorf("%w: y", ErrVerifyRefused)))
	assert.Equal(t, "Verifying fence failed", rollbackReason(verify, errors.New("timeout")))
	assert.Equal(t, "Syncing committed offsets failed", rollbackReason(sync, errors.New("boom")))
}
func TestD2SOrchestrator_Execute_AVerifyRefusalRollsBackNamingTheCause(t *testing.T) {
	w := cleanWorld()
	w.facts.TargetCanCommitOffsets = false
	rec := &patchRecorder{}
	run := newTestRun(rec.service(), w.deps())

	err := run.execute(context.Background(), convertResult())

	require.ErrorIs(t, err, ErrVerifyRefused)
	assert.Contains(t, run.out.String(), "Conversion check failed after the fence — removing fence")
	assert.Equal(t, StateInitialized, run.state())
	assert.Len(t, rec.patches, 2, "the fence, then the rollback")
	assert.Empty(t, w.dst.commits)
}

func TestD2SOrchestrator_Execute_DirectCommitsRollBackNamingTheCause(t *testing.T) {
	w := cleanWorld()
	w.src.rounds = append(w.src.rounds, groupoffsets.Snapshot{"orders-app": {"orders": {0: {Offset: 9}}}})
	rec := &patchRecorder{}
	run := newTestRun(rec.service(), w.deps())

	err := run.execute(context.Background(), convertResult())

	require.ErrorIs(t, err, groupoffsets.ErrRogueCommits)
	assert.Contains(t, run.out.String(), "Direct commits detected — removing fence")
	assert.Equal(t, StateInitialized, run.state())
	assert.Len(t, rec.patches, 2)
}

// A cancelled context cannot perform the unfence IO, so it leaves the fenced world for the next run.
func TestD2SOrchestrator_Execute_ACancelledVerifyDoesNotRollBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := cleanWorld()
	w.gather = func(context.Context) (*migplan.ConvertFacts, error) {
		cancel()
		return nil, context.Canceled
	}
	rec := &patchRecorder{}
	run := newTestRun(rec.service(), w.deps())

	require.Error(t, run.execute(ctx, convertResult()))

	assert.Equal(t, StateFenced, run.state())
	assert.Len(t, rec.patches, 1, "only the fence: no unfence on a dead context")
	assert.NotContains(t, run.out.String(), "removing fence")
}
