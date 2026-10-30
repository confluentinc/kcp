//go:build e2e

package routeconversion

import (
	"context"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/migration/d2s"
	"github.com/stretchr/testify/require"
)

// stalledSwitchTimeout bounds the switch's wait for the stopped operator
// (--rollout-timeout) and for the pods' reload (--hot-reload-timeout).
const stalledSwitchTimeout = 45 * time.Second

// TestDrift_StalledSwitchRerunIsRefused leaves the gateway CR holding the
// static route while the CFK operator never observed it, and checks kcp
// refuses to act on it. One run, held after offsets_synced: a new run's first
// gateway step runs the hot-reload smoke test, which would time out with the
// operator stopped and stop the run before its switch, so the operator is
// stopped while the run is held, with the smoke test, the fence, its
// verification and the sync already done. On release the switch patch lands
// on the CR and its wait for the operator times out; a failed switch keeps the
// fence and rolls nothing back. kcp's pod check then sees the operator hasn't
// caught up with the CR, so a dry-run and a re-run both refuse, and neither
// writes to the gateway. The operator is restored when the test ends.
func TestDrift_StalledSwitchRerunIsRefused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds("drift")...)
	security := e.liveRoute(t, ctx)["security"]
	mani, _ := e.writeManifest(t, "drift-stalled-switch", minDetect)
	e.snapshot(t, ctx, "before", "BEFORE (expect the post-TBM route, no destination offsets)", seeds)

	var gen0, obs0 int64
	timeout := stalledSwitchTimeout.String()
	out1, err1 := e.executeHeldAt(t, "kcp-run-1-switch-stalls.log", mani, d2s.StateOffsetsSynced, func() {
		e.requireFenced(t, ctx, "held after offsets_synced")
		e.requireSynced(t, seeds)
		e.snapshot(t, ctx, "held-after-offsets-synced", "HELD after offsets_synced (expect fenced, offsets on the destination)", seeds)
		e.stopOperator(t, ctx)
		gen0, obs0 = e.crGenerations(t, ctx)
		require.Equal(t, gen0, obs0, "the operator must have observed the fenced CR before it stopped")
	}, "--rollout-timeout", timeout, "--hot-reload-timeout", timeout)
	require.Error(t, err1, "the switch must fail: the operator never observes it")
	require.Contains(t, out1, "Static route applied", "the run must reach the switch and patch the CR")
	require.Contains(t, out1, "to be observed by the operator", "the switch must fail waiting for the operator")
	require.Contains(t, out1, "Switch could not be confirmed", "the failure must be the unconfirmed switch")
	require.NotContains(t, out1, gatewayUnfenced, "a failed switch rolls nothing back")
	e.requireConvertedRoute(t, ctx, security)
	gen1, obs1 := e.crGenerations(t, ctx)
	require.Lessf(t, obs1, gen1, "the stopped operator must not observe the switch (observedGeneration %d, generation %d)", obs1, gen1)
	require.Greater(t, gen1, gen0, "the switch patch must bump the CR's generation")
	require.Equal(t, obs0, obs1, "the operator is stopped")
	e.snapshot(t, ctx, "after-stalled-switch", "AFTER the stalled switch (CR static on the destination; operator behind)", seeds)

	out2, err2 := e.dryRun(t, "kcp-run-2-dry-run.log", mani)
	require.Error(t, err2, "the dry-run must refuse: the pods may not serve the CR")
	require.Contains(t, out2, "gateway pods serve the CR", "the dry-run must name the failed pod check")
	require.Contains(t, out2, "the operator has not caught up with the CR", "the dry-run must say the operator is behind the CR")
	gen2, obs2 := e.crGenerations(t, ctx)
	require.Equal(t, gen1, gen2, "the dry-run must make no gateway write")
	require.Equal(t, obs1, obs2, "the operator is still stopped")

	out3, err3 := e.execute(t, "kcp-run-3-rerun.log", mani, nil, nil)
	require.Error(t, err3, "the re-run must refuse: the pods may not serve the CR")
	require.Contains(t, out3, "the operator has not caught up with the CR", "the re-run must say the operator is behind the CR")
	require.NotContains(t, out3, nothingToDoSuffix, "the re-run must not report nothing to do from the CR alone")
	gen3, obs3 := e.crGenerations(t, ctx)
	require.Equal(t, gen1, gen3, "the re-run must make no gateway write")
	require.Equal(t, obs1, obs3, "the operator is still stopped")
	e.requireConvertedRoute(t, ctx, security)
	e.snapshot(t, ctx, "after-rerun", "AFTER the refused re-run (CR static; operator behind; nothing written)", seeds)

	t.Logf("\n✅ RESULT: drift refused — the CR is static at generation %d, the operator last observed generation %d, "+
		"and the dry-run and the re-run both refused without writing to the gateway.", gen1, obs1)
}
