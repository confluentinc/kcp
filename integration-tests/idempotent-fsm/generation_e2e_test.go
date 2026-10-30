//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/stretchr/testify/require"
)

// TestGeneration_CRChangedBeforePromoteStopsWithNoWrite proves kcp's
// generation check: when something else changes the gateway CR after kcp's
// fence, the run stops before promote with no gateway write of its own,
// instead of patching over the change. Run 1 is held after fence_verified,
// the CR's spec is changed (spec.configId, which moves metadata.generation),
// and kcp is released. Its pre-promote check sees a generation it didn't
// write, stops non-zero, and leaves the fence applied and every mirror
// ACTIVE. Run 2, a plain re-run, completes the migration. Both modes. Slice
// tbm-topic-086..090.
func TestGeneration_CRChangedBeforePromoteStopsWithNoWrite(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	topics := e.topicRange(86, 90)
	// A failure before run 2 must not leave the slice fenced or offset sync paused for later tests.
	t.Cleanup(func() { e.liftSliceFence(t, context.Background(), topics) })
	if e.mode == "static" {
		e.resetStaticRoute(t, ctx)
	}
	m := e.writeManifest(t, "generation-cr-changed.yaml", topics)
	args := []string{"migration", "cutover", "execute", "--migration-yaml", m}

	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source, no fence)", topics)

	var bumped int64
	out1, err1 := e.runKCPHeldAt(t, "kcp-run-1-held-then-changed.log", cpFenceVerified, func() {
		bumped = e.bumpGatewayGeneration(t, ctx)
	}, args...)
	require.Error(t, err1, "run 1 must exit non-zero: the CR changed under it")
	require.Contains(t, out1, "changed since kcp last knew it", "run 1 must name the CR change")
	require.Contains(t, out1, "Stopping with no gateway write — fence applied, not promoted")
	require.NotContains(t, out1, "attempting to remove the fence", "run 1 must not roll the fence back with a write")

	e.snapshot(t, ctx, "after-run-1", "AFTER run 1 (expect fenced, every mirror ACTIVE, generation = the change's)", topics)
	gen, _ := e.crGenerations(t, ctx)
	require.Equal(t, bumped, gen, "kcp must write nothing to the gateway CR after the change: metadata.generation is still the change's")
	cr := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must still be fenced", tp)
		require.Equalf(t, "ACTIVE", ms[tp], "%s must not have been promoted", tp)
	}

	e.waitForGatewayInSync(t, ctx)
	out2, err2 := e.runKCP(t, "kcp-run-2-rerun.log", "", args...)
	require.NoErrorf(t, err2, "the plain re-run must complete the migration:\n%s", out2)
	e.snapshot(t, ctx, "after-run-2", "AFTER run 2 (expect mirrors STOPPED, route → destination)", topics)
	ms = e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "STOPPED", ms[tp], "%s must be promoted by the re-run", tp)
	}
}

// TestGeneration_CRChangedBeforeRollbackStopsWithNoWrite proves the write
// guard on a compensation: when the CR changes after kcp's fence and a later
// step then fails, the rollback's unfence is refused instead of written, and
// the paused offset sync is not restored. Run 1 sets both the failure hook on
// verify_fence and the hold after fenced; while held, the CR's spec is changed
// (spec.configId, which moves metadata.generation), and kcp is released. It
// pauses offset sync, verify_fence fails, and abort_fence's unfence is refused
// by the generation guard: kcp stops non-zero with the fence applied, every
// mirror ACTIVE, the CR at the change's generation and offset sync still
// paused. Run 2, a plain re-run once the gateway settles, completes the
// migration. Static only: the manifest pauses offset sync, which only a
// staticMigration can. Slice tbm-topic-096..100.
func TestGeneration_CRChangedBeforeRollbackStopsWithNoWrite(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: the offset-sync pause exists only under staticMigration")
	}
	ctx := context.Background()
	topics := e.topicRange(96, 100)
	// A failure before run 2 must not leave the slice fenced or offset sync paused for later tests.
	t.Cleanup(func() { e.liftSliceFence(t, context.Background(), topics) })
	e.resetStaticRoute(t, ctx)
	require.Equal(t, "true", e.linkOffsetSync(t, ctx),
		"setup.sh must start the static suite's cluster link with consumer offset sync on")
	m := e.writeManifestPausingOffsetSync(t, "generation-cr-changed-rollback.yaml", topics)
	args := []string{"migration", "cutover", "execute", "--migration-yaml", m}

	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source, no fence)", topics)

	var bumped int64
	failAt := []string{killpoint.FailEnvVar + "=" + failStep}
	out1, err1 := e.runKCPHeldAtWith(t, "kcp-run-1-held-changed-then-fails.log", cpFenced, failAt, func() {
		bumped = e.bumpGatewayGeneration(t, ctx)
	}, args...)
	require.Error(t, err1, "run 1 must exit non-zero: verify_fence fails and the rollback is refused")
	require.Contains(t, out1, "test failure hook: "+failStep+" failed", "run 1 must fail at verify_fence")
	require.Contains(t, out1, "changed since kcp last knew it", "run 1 must name the CR change")
	require.Contains(t, out1, "Stopping with no gateway write — fence applied, not promoted")
	require.NotContains(t, out1, "Gateway unfenced", "run 1 must not unfence over the change")

	e.snapshot(t, ctx, "after-run-1", "AFTER run 1 (expect fenced, every mirror ACTIVE, generation = the change's, offset sync paused)", topics)
	gen, _ := e.crGenerations(t, ctx)
	require.Equal(t, bumped, gen, "kcp must write nothing to the gateway CR after the change: metadata.generation is still the change's")
	cr := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must still be fenced", tp)
		require.Equalf(t, "ACTIVE", ms[tp], "%s must not have been promoted", tp)
	}
	require.Equal(t, "false", e.linkOffsetSync(t, ctx),
		"offset sync must still be paused: a refused rollback skips the restore")

	e.waitForGatewayInSync(t, ctx)
	out2, err2 := e.runKCP(t, "kcp-run-2-rerun.log", "", args...)
	require.NoErrorf(t, err2, "the plain re-run must complete the migration:\n%s", out2)
	e.snapshot(t, ctx, "after-run-2", "AFTER run 2 (expect mirrors STOPPED, route → destination, offset sync on)", topics)
	ms = e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "STOPPED", ms[tp], "%s must be promoted by the re-run", tp)
	}
	require.Equal(t, "true", e.linkOffsetSync(t, ctx), "the re-run must restore consumer offset sync")
}
