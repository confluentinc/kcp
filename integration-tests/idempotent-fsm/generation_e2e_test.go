//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"testing"

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
	require.NotContains(t, out1, "removing fence", "run 1 must not roll the fence back with a write")

	e.snapshot(t, ctx, "after-run-1", "AFTER run 1 (expect fenced, every mirror ACTIVE, generation = the change's)", topics)
	gen, _ := e.crGenerations(t, ctx)
	require.Equal(t, bumped, gen, "kcp must write nothing to the gateway CR after the change: metadata.generation is still the change's")
	cr := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must still be fenced", tp)
		require.Equalf(t, "ACTIVE", ms[tp], "%s must not have been promoted", tp)
	}

	out2, err2 := e.runKCP(t, "kcp-run-2-rerun.log", "", args...)
	require.NoErrorf(t, err2, "the plain re-run must complete the migration:\n%s", out2)
	e.snapshot(t, ctx, "after-run-2", "AFTER run 2 (expect mirrors STOPPED, route → destination)", topics)
	ms = e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "STOPPED", ms[tp], "%s must be promoted by the re-run", tp)
	}
}
