//go:build e2e

package routeconversion

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migration/d2s"
	"github.com/stretchr/testify/require"
)

// Each test holds a conversion after a state with the test-only hold seam
// (KCP_TEST_HOLD_AFTER), changes the gateway CR's spec out from under it
// (spec.configId, which moves metadata.generation), and releases it. kcp's
// generation check sees a CR it didn't write and stops with no gateway write
// of its own: no rollback, no sync, the fence left as it is.

// runGenerationStop holds a conversion after state, bumps the CR's generation
// and expects the run to stop non-zero with stopped in its output, kcp's
// fence still on the route, nothing synced, and the generation still the
// bump's.
func runGenerationStop(t *testing.T, name, state, stopped string) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds(name)...)
	mani, _ := e.writeManifest(t, "generation-"+name, minDetect)
	e.snapshot(t, ctx, "before", "BEFORE (expect the post-TBM route, no destination offsets)", seeds)

	var bumped int64
	out, err := e.executeHeldAt(t, "kcp-run-1-held-then-changed.log", mani, state, func() {
		e.requireFenced(t, ctx, "held after "+state)
		bumped = e.bumpGatewayGeneration(t, ctx)
	})
	require.Error(t, err, "the run must exit non-zero: the CR changed under it")
	require.Contains(t, out, "changed since kcp last knew it", "the run must name the CR change")
	require.Contains(t, out, "Stopping with no gateway write — "+stopped)
	require.NotContains(t, out, "attempting to remove the fence", "the run must not roll the fence back with a write")
	require.NotContains(t, out, gatewayUnfenced, "the run must not roll the fence back with a write")

	e.snapshot(t, ctx, "after-stop", "AFTER the stop (expect fenced, no destination offsets, generation = the change's)", seeds)
	e.requireFenced(t, ctx, "a stop on a changed CR keeps kcp's fence")
	e.requireNoDestOffsets(t, seeds, "a run stopped before sync_offsets")
	gen, _ := e.crGenerations(t, ctx)
	require.Equal(t, bumped, gen, "kcp must write nothing to the gateway CR after the change: metadata.generation is still the change's")
	t.Logf("\n✅ RESULT: the CR changed while the run was held after %s; kcp stopped with no gateway write (generation %d), the fence up and nothing synced.", state, gen)
}

// Held after fenced: verify_fence's gather reads the CR and finds the change.
func TestGeneration_CRChangedAfterFenceRefusesVerify(t *testing.T) {
	runGenerationStop(t, "verify", d2s.StateFenced, "fence applied, not verified")
}

// Held after fence_verified: the check before sync_offsets finds the change.
func TestGeneration_CRChangedBeforeSyncOffsetsStopsWithNoWrite(t *testing.T) {
	runGenerationStop(t, "sync", d2s.StateFenceVerified, "fence applied, offsets not synced")
}
