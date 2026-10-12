//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/stretchr/testify/require"
)

// failStep is the workflow step these tests fail with the test-only failure
// hook: the last step before promote in both FSMs, run with the fence up.
const failStep = "verify_fence"

// A step failure on a RESUMED run, before promote. The first run is killed
// right after the fence, so the resume starts from a route that is already
// fenced — the gateway CR it pulls at its start carries the interrupted run's
// fence. The resume's verify_fence step then fails. Nothing is promoted yet, so
// the run rolls back, and the rollback must lift that fence rather than
// re-apply it. Slice tbm-topic-041..045.
func TestRollback_OnResume_LiftsTheFence(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	e.resetStaticRoute(t, ctx) // static: reclaim the whole route from any prior migration (no-op on dynamic)
	topics := e.topicRange(41, 45)
	mani := e.writeManifest(t, "rollback-on-resume", topics)
	e.saveManifest(t, mani)
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpFenced, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted after the fence must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")
	e.snapshot(t, ctx, "after-interrupt", "AFTER interrupt at fenced (expect route fenced, mirrors ACTIVE)", topics)
	cr := e.readCR(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must be fenced — the interrupt came after the fence step", tp)
	}

	out2, err2 := e.runKCPFailingAt(t, "kcp-run-2-resume-fails.log", failStep, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err2, "the resume's failed step must fail the run")
	require.Contains(t, out2, killpoint.FailEnvVar, "the failure must be the hook's")
	require.Contains(t, out2, "removing fence to restore traffic", "nothing is promoted, so the run rolls back")
	require.NotContains(t, out2, "keeping the fence")

	e.snapshot(t, ctx, "after-rollback", "AFTER the resume's rollback (expect unfenced, route → source, mirrors ACTIVE)", topics)
	cr = e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Falsef(t, e.isFenced(t, cr, tp), "%s must be unfenced — the rollback lifts the fence the interrupted run left", tp)
		require.Falsef(t, e.isSwitchedToTarget(t, cr, tp), "%s must still route to the source after a rollback", tp)
		require.Equalf(t, "ACTIVE", ms[tp], "%s must still be ACTIVE — nothing was promoted", tp)
	}
	t.Logf("\n✅ RESULT: the resume's pre-promote failure rolled back and lifted the interrupted run's fence.")
}

// A step failure on a MID-BATCH resume. promoteBatchSize 2 and a kill right
// after the first promote request is accepted leave 2 topics promoting or
// promoted and 3 ACTIVE, with the route fenced. The resume's verify_fence step
// then fails. Part of the batch is already promoted, so the run must not roll
// back — unfencing would send the promoted topics' clients back to the source.
// The fence stays up and the run fails saying so; a plain re-run then completes
// the migration. Slice tbm-topic-036..040.
func TestRollback_OnMidBatchResume_KeepsTheFence(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	topics := e.topicRange(36, 40)
	mani := e.writeManifestWithPromoteBatchSize(t, "rollback-mid-batch", topics, midBatchPromoteSize)
	e.saveManifest(t, mani)
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpPromoteAccepted, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted mid-batch must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")
	ms := e.mirrorStatusAfterPromote(t, ctx, topics, midBatchPromoteSize)
	e.snapshot(t, ctx, "after-interrupt", "AFTER the mid-batch interrupt (expect fenced, 2 promoting/promoted, 3 ACTIVE)", topics)
	cr := e.readCR(t, ctx)
	var promoted int
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must be fenced — promote runs after the fence step", tp)
		if ms[tp] == "PENDING_STOPPED" || ms[tp] == "STOPPED" {
			promoted++
		}
	}
	require.Equalf(t, midBatchPromoteSize, promoted, "exactly one batch of %d must be promoting or promoted before the resume", midBatchPromoteSize)

	out2, err2 := e.runKCPFailingAt(t, "kcp-run-2-resume-fails.log", failStep, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err2, "the resume's failed step must fail the run")
	require.Contains(t, out2, killpoint.FailEnvVar, "the failure must be the hook's")
	require.Contains(t, out2, "keeping the fence", "part of the batch is promoted, so the run must not roll back")
	require.NotContains(t, out2, "removing fence")

	e.snapshot(t, ctx, "after-failure", "AFTER the resume's failure (expect still fenced, route → source)", topics)
	cr = e.readCR(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must still be fenced — the failed resume must not unfence a partly promoted batch", tp)
		require.Falsef(t, e.isSwitchedToTarget(t, cr, tp), "%s must not be switched yet", tp)
	}

	out3, err3 := e.runKCP(t, "kcp-run-3-resume.log", "", "migration", "execute", "--migration-yaml", mani)
	require.NoError(t, err3, "a plain re-run must complete the migration")
	require.Contains(t, strings.ToLower(out3), "migration complete", "the re-run must report completion")

	e.snapshot(t, ctx, "after-resume", "AFTER the re-run (expect mirrors STOPPED, route → destination, fence cleared)", topics)
	cr = e.readCR(t, ctx)
	ms = e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED after the migration completes", tp)
		require.Truef(t, e.isSwitchedToTarget(t, cr, tp), "%s must be switched to the target domain", tp)
		require.Falsef(t, e.isFenced(t, cr, tp), "%s must not be fenced after completion", tp)
	}
	t.Logf("\n✅ RESULT: the partly promoted resume kept its fence on failure, and a re-run completed the migration.")
}

// failBeforeFenceStep is the workflow step TestRollback_OnResume_FailureBeforeTheFenceStep
// fails with the test-only failure hook: the step before fence in both FSMs,
// which a resume runs with the interrupted run's fence already up.
const failBeforeFenceStep = "wait_for_lags"

// A step failure on a RESUMED run, before the resume reaches its own fence
// step. The first run is killed with the fence up (static: after the offset-sync
// pause too), so the resume starts from a fenced route. Its wait_for_lags step
// then fails. Nothing is promoted, so the run rolls back although it never
// fenced: it lifts the interrupted run's fence and, on static, sets consumer
// offset sync back to the declared baseline. Slice tbm-topic-011..015.
func TestRollback_OnResume_FailureBeforeTheFenceStep_LiftsTheFence(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	topics := e.topicRange(11, 15)
	t.Cleanup(func() { e.liftSliceFence(t, context.Background(), topics) })

	killAt := cpFenced
	mani := e.writeManifest(t, "rollback-before-fence", topics)
	if e.mode == "static" {
		killAt = cpOffsetSyncPaused
		mani = e.writeManifestPausingOffsetSync(t, "rollback-before-fence", topics)
		require.Equal(t, "true", e.linkOffsetSync(t, ctx),
			"setup.sh must start the static suite's cluster link with consumer offset sync on")
	}
	e.saveManifest(t, mani)
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", killAt, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the interrupted run must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")
	e.snapshot(t, ctx, "after-interrupt", "AFTER interrupt with the fence up (expect route fenced, mirrors ACTIVE; static: offset sync off)", topics)
	cr := e.readCR(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must be fenced — the interrupt came after the fence step", tp)
	}
	if e.mode == "static" {
		require.Equal(t, "false", e.linkOffsetSync(t, ctx), "offset sync must be paused — the interrupt came after the pause")
	}

	out2, err2 := e.runKCPFailingAt(t, "kcp-run-2-resume-fails.log", failBeforeFenceStep, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err2, "the resume's failed step must fail the run")
	require.Contains(t, out2, killpoint.FailEnvVar, "the failure must be the hook's")
	require.Contains(t, out2, "removing fence to restore traffic", "nothing is promoted, so the run rolls back")
	require.NotContains(t, out2, "keeping the fence")

	e.snapshot(t, ctx, "after-rollback", "AFTER the resume's rollback (expect unfenced, route → source, mirrors ACTIVE; static: offset sync on)", topics)
	cr = e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Falsef(t, e.isFenced(t, cr, tp), "%s must be unfenced — the rollback lifts the fence the interrupted run left", tp)
		require.Falsef(t, e.isSwitchedToTarget(t, cr, tp), "%s must still route to the source after a rollback", tp)
		require.Equalf(t, "ACTIVE", ms[tp], "%s must still be ACTIVE — nothing was promoted", tp)
	}
	if e.mode == "static" {
		require.Equal(t, "true", e.linkOffsetSync(t, ctx), "the rollback must set consumer offset sync back to the baseline (enabled)")
	}
	t.Logf("\n✅ RESULT: the resume failed before its own fence step, rolled back and lifted the interrupted run's fence.")
}

// liftSliceFence puts the slice back as a fresh run finds it, whatever a
// failed test left: on static, the route source-bound and unfenced and the
// cluster link's consumer offset sync on; on dynamic, no kcp fence for exactly
// topics.
func (e *env) liftSliceFence(t *testing.T, ctx context.Context, topics []string) {
	t.Helper()
	if e.mode == "static" {
		e.resetStaticRoute(t, ctx)
		require.NoError(t, e.linkSvc.AlterConfigs(ctx, e.linkConfig(), []clusterlink.ConfigAlteration{
			{Name: offsetSyncEnableKey, Value: "true", Operation: clusterlink.OperationSet},
		}), "set consumer offset sync back on")
		return
	}
	e.patchFencing(t, ctx, func(fencing []any) []any {
		kept := make([]any, 0, len(fencing))
		for _, fe := range fencing {
			if fm, ok := fe.(map[string]any); !ok || !isKcpFenceEntry(fm, topics) {
				kept = append(kept, fe)
			}
		}
		return kept
	})
}
