//go:build e2e

package routeconversion

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migration/d2s"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/stretchr/testify/require"
)

// Each test fails one step with the test-only failure hook (KCP_TEST_FAIL_AT)
// and checks the compensation the d2s orchestrator's handleStepFailure takes:
// before the switch the fence comes off (abort_fence, or a direct unfence of an
// earlier run's fence); a failed switch keeps it. A plain re-run then finishes.

// requireFailedAt asserts a run failed through the failure hook at step.
func requireFailedAt(t *testing.T, out string, err error, step string) {
	t.Helper()
	require.Error(t, err, "the failed step must fail the run")
	require.Contains(t, out, killpoint.FailEnvVar+"="+step, "the failure must be the hook's")
}

// runUnfencingRollback fails step on a plain run and expects the fence to come
// off with reason in the output, nothing on the destination, and a plain
// re-run to finish.
func runUnfencingRollback(t *testing.T, name, step, reason string) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds(name)...)
	security := e.liveRoute(t, ctx)["security"]
	mani, id := e.writeManifest(t, "rollback-"+name, minDetect)
	e.snapshot(t, ctx, "before", "BEFORE (expect the post-TBM route)", seeds)

	out, err := e.execute(t, "kcp-run-1-fails.log", mani, failAt(step), nil)
	requireFailedAt(t, out, err, step)
	require.Contains(t, out, reason)
	require.Contains(t, out, gatewayUnfenced)
	e.snapshot(t, ctx, "after-rollback", "AFTER the rollback (expect the post-TBM route, no destination offsets)", seeds)
	e.requirePostTBM(t, ctx, "after the rollback")
	e.requireNoDestOffsets(t, seeds, "a rolled-back run")

	out, err = e.execute(t, "kcp-run-2-rerun.log", mani, nil, nil)
	requireCompleted(t, out, err, id)
	e.requireConvertedRoute(t, ctx, security)
	e.requireSynced(t, seeds)
	t.Logf("\n✅ RESULT: %s failed, the fence came off, and a plain re-run converted the route.", step)
}

func TestRollback_FailAtVerifyFence(t *testing.T) {
	runUnfencingRollback(t, "verify", d2s.EventVerifyFence, "Verifying fence failed — removing fence")
}

func TestRollback_FailAtSyncOffsets(t *testing.T) {
	runUnfencingRollback(t, "sync", d2s.EventSyncOffsets, "Syncing committed offsets failed — removing fence")
}

// A failed switch is past the point where unfencing is safe: the fence stays,
// the offsets are already on the destination, and a re-run finishes the switch.
func TestRollback_FailAtSwitchKeepsFence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds("switch")...)
	security := e.liveRoute(t, ctx)["security"]
	mani, id := e.writeManifest(t, "rollback-switch", minDetect)

	out, err := e.execute(t, "kcp-run-1-fails.log", mani, failAt(d2s.EventSwitch), nil)
	requireFailedAt(t, out, err, d2s.EventSwitch)
	require.Contains(t, out, "Switch failed — keeping the fence: the static route never reached the Gateway CR")
	require.NotContains(t, out, gatewayUnfenced)
	e.snapshot(t, ctx, "after-failure", "AFTER the failed switch (expect fenced, offsets on the destination)", seeds)
	e.requireFenced(t, ctx, "a failed switch keeps the fence")
	e.requireSynced(t, seeds)

	out, err = e.execute(t, "kcp-run-2-rerun.log", mani, nil, nil)
	requireCompleted(t, out, err, id)
	e.requireConvertedRoute(t, ctx, security)
	e.requireSynced(t, seeds)
	t.Logf("\n✅ RESULT: the failed switch kept the fence and a plain re-run finished the conversion.")
}

// Run 1 is interrupted after the fence; run 2 fails at its own fence step,
// before it has fenced anything, so there is no abort_fence edge: the earlier
// run's fence is removed directly. Run 3 completes.
func TestRollback_FailAtFenceOnRerunRemovesEarlierFence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds("refence")...)
	security := e.liveRoute(t, ctx)["security"]
	mani, id := e.writeManifest(t, "rollback-refence", minDetect)

	out, err := e.execute(t, "kcp-run-1-interrupted.log", mani, cancelAfter(d2s.StateFenced), nil)
	requireInterrupted(t, out, err)
	e.requireFenced(t, ctx, "interrupted after the fence step")

	out, err = e.execute(t, "kcp-run-2-fails.log", mani, failAt(d2s.EventFence), nil)
	requireFailedAt(t, out, err, d2s.EventFence)
	require.Contains(t, out, "Fencing route failed — removing fence")
	require.Contains(t, out, gatewayUnfenced)
	e.snapshot(t, ctx, "after-rollback", "AFTER run 2's rollback (expect the earlier fence gone)", seeds)
	e.requirePostTBM(t, ctx, "the earlier run's fence must be removed")
	e.requireNoDestOffsets(t, seeds, "nothing was synced")

	out, err = e.execute(t, "kcp-run-3-rerun.log", mani, nil, nil)
	requireCompleted(t, out, err, id)
	e.requireConvertedRoute(t, ctx, security)
	e.requireSynced(t, seeds)
	t.Logf("\n✅ RESULT: a re-run failing at its fence step removed the earlier run's fence, and run 3 converted the route.")
}
