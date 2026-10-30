package d2s

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Gateway CR generation checks ---

// changedCR is the refusal a guarded write or check returns once the CR moved.
func changedCR(expected, observed int64) *gateway.GenerationChangedError {
	return &gateway.GenerationChangedError{Namespace: "confluent", Gateway: "gateway-initial", Expected: expected, Observed: observed}
}

// scriptedRoutePatches answers the n-th route patch (0-based) with results[n], recording the patches; calls past
// the script succeed at the expected generation.
func scriptedRoutePatches(patches *[]gateway.RoutePatch, results ...func(expected int64) (gateway.PatchResult, error)) func(context.Context, string, string, gateway.RoutePatch, string, int64) (gateway.PatchResult, error) {
	return func(_ context.Context, _, _ string, rp gateway.RoutePatch, configID string, expected int64) (gateway.PatchResult, error) {
		n := len(*patches)
		*patches = append(*patches, rp)
		if n < len(results) {
			return results[n](expected)
		}
		return gateway.PatchResult{ConfigID: configID, Generation: expected}, nil
	}
}

// patchedTo answers a route patch as a write that left the CR at gen.
func patchedTo(gen int64) func(int64) (gateway.PatchResult, error) {
	return func(int64) (gateway.PatchResult, error) { return gateway.PatchResult{Generation: gen}, nil }
}

// refusedAs answers a route patch as refused: the CR is at observed.
func refusedAs(observed int64) func(int64) (gateway.PatchResult, error) {
	return func(expected int64) (gateway.PatchResult, error) {
		return gateway.PatchResult{}, changedCR(expected, observed)
	}
}

// generationRun builds a run whose reconcile result carries generation 4, over a world the caller may edit
// before executing; the gateway mock is the caller's to script.
func generationRun(w *world, fencedAtStart bool) (*testRun, *mockGatewayService, *migplan.Result) {
	gw := &mockGatewayService{}
	run := newTestRun(gw, w.deps())
	res := convertResult()
	res.GatewayGeneration = 4
	res.FencedAtStart = fencedAtStart
	return run, gw, res
}

func TestD2SActions_Initialize_CopiesTheGatewayGeneration(t *testing.T) {
	a := gatewayActions(&mockGatewayService{})
	res := convertResult()
	res.GatewayGeneration = 9

	require.NoError(t, a.Initialize(context.Background(), &migration.MigrationConfig{}, res))

	assert.Equal(t, int64(9), a.gatewayGeneration)
}

func TestD2SOrchestrator_Execute_FencePatchIsGuardedOnTheReconcileGeneration(t *testing.T) {
	w := cleanWorld()
	w.facts.Gateway.Generation = 4
	run, gw, res := generationRun(w, false)
	var patches []gateway.RoutePatch
	gw.patchRouteGenFn = scriptedRoutePatches(&patches)
	gw.getGatewayGenerationFn = func(context.Context, string, string) (int64, error) { return 4, nil }

	require.NoError(t, run.execute(context.Background(), res))

	require.NotEmpty(t, gw.routeExpected)
	assert.Equal(t, int64(4), gw.routeExpected[0], "the fence patch is guarded on the generation reconcile read")
}

func TestD2SOrchestrator_Execute_VerifyFenceGatherOnAMovedCR_StopsWithNoWrite(t *testing.T) {
	w := cleanWorld()
	w.facts.Gateway.Generation = 7
	run, gw, res := generationRun(w, false)
	var patches []gateway.RoutePatch
	gw.patchRouteGenFn = scriptedRoutePatches(&patches, patchedTo(6))

	err := run.execute(context.Background(), res)

	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrGatewayChanged)
	assert.NotErrorIs(t, err, ErrVerifyRefused)
	assert.Zero(t, w.src.lists, "no snapshot is taken")
	require.Len(t, patches, 1, "the fence only: no unfence")
	assert.Contains(t, run.out.String(), changedCR(6, 7).Summary("fence applied, not verified"))
	assert.NotContains(t, run.out.String(), "removing fence")
	assert.Equal(t, StateFenced, run.state())
}

func TestD2SOrchestrator_Execute_VerifyFenceGatherWithGenerationUnknown_Proceeds(t *testing.T) {
	w := cleanWorld()
	w.facts.Gateway.Generation = 0
	run, gw, res := generationRun(w, false)
	var patches []gateway.RoutePatch
	gw.patchRouteGenFn = scriptedRoutePatches(&patches)
	gw.getGatewayGenerationFn = func(context.Context, string, string) (int64, error) { return 4, nil }

	require.NoError(t, run.execute(context.Background(), res))

	assert.Equal(t, StateSwitched, run.state())
}

func TestD2SOrchestrator_Execute_CRMovedBeforeSyncOffsets_StopsWithNoWrite(t *testing.T) {
	w := cleanWorld()
	w.facts.Gateway.Generation = 6
	run, gw, res := generationRun(w, false)
	var patches []gateway.RoutePatch
	gw.patchRouteGenFn = scriptedRoutePatches(&patches, patchedTo(6))
	gw.getGatewayGenerationFn = func(context.Context, string, string) (int64, error) { return 8, nil }

	err := run.execute(context.Background(), res)

	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrGatewayChanged)
	assert.Zero(t, w.dst.builds, "no committer is built")
	assert.Empty(t, w.dst.commits)
	require.Len(t, patches, 1, "the fence only: no unfence")
	assert.Contains(t, run.out.String(), changedCR(6, 8).Summary("fence applied, offsets not synced"))
	assert.Equal(t, StateFenceVerified, run.state())
}

func TestD2SOrchestrator_Execute_RefusalAtFenceWrite_StopsWithNoWrite(t *testing.T) {
	run, gw, res := generationRun(cleanWorld(), true)
	var patches []gateway.RoutePatch
	gw.patchRouteGenFn = scriptedRoutePatches(&patches, refusedAs(7))

	err := run.execute(context.Background(), res)

	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrGatewayChanged)
	require.Len(t, patches, 1, "the refused fence only: the earlier run's rollback is skipped")
	assert.NotContains(t, run.out.String(), "removing fence")
	assert.Contains(t, run.out.String(), "Stopping with no gateway write — an earlier run's fence applied.")
}

func TestD2SOrchestrator_Execute_EarlierRunsFence_RefusalAtRollback_StopsWithNoWrite(t *testing.T) {
	t.Setenv(killpoint.FailEnvVar, EventFence)
	run, gw, res := generationRun(cleanWorld(), true)
	var patches []gateway.RoutePatch
	gw.patchRouteGenFn = scriptedRoutePatches(&patches, refusedAs(7))

	err := run.execute(context.Background(), res)

	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrGatewayChanged)
	assert.Contains(t, err.Error(), killpoint.FailEnvVar, "the originating failure still surfaces")
	require.Len(t, patches, 1, "the refused unfence only")
	assert.NotContains(t, run.out.String(), "Gateway unfenced")
	assert.Contains(t, run.out.String(), "Stopping with no gateway write — an earlier run's fence applied.")
}

// unconfirmedFenceGenerationRun fails the fence's acceptance wait once, so the fence lands but is never
// confirmed and the orchestrator compensates.
func unconfirmedFenceGenerationRun(t *testing.T) (*testRun, *mockGatewayService, *migplan.Result) {
	t.Helper()
	run, gw, res := generationRun(cleanWorld(), false)
	accepts := 0
	gw.waitForGatewayAcceptedFn = func(context.Context, string, string, time.Duration, time.Duration) error {
		accepts++
		if accepts == 1 {
			return fmt.Errorf("gateway spec not accepted")
		}
		return nil
	}
	return run, gw, res
}

func TestD2SOrchestrator_Execute_UnconfirmedFence_CRUnchanged_CompensatesGated(t *testing.T) {
	run, gw, res := unconfirmedFenceGenerationRun(t)
	var patches []gateway.RoutePatch
	gw.patchRouteGenFn = scriptedRoutePatches(&patches, patchedTo(5), patchedTo(6))

	err := run.execute(context.Background(), res)

	require.ErrorIs(t, err, migration.ErrFenceUnconfirmed)
	assert.NotErrorIs(t, err, gateway.ErrGatewayChanged)
	require.Len(t, patches, 2, "the fence, then the removing apply")
	assert.Equal(t, []int64{4, 5}, gw.routeExpected, "the removal is guarded on the generation the fence left")
}

func TestD2SOrchestrator_Execute_UnconfirmedFence_CRMoved_StopsWithNoWrite(t *testing.T) {
	run, gw, res := unconfirmedFenceGenerationRun(t)
	var patches []gateway.RoutePatch
	gw.patchRouteGenFn = scriptedRoutePatches(&patches, patchedTo(5), refusedAs(6))

	err := run.execute(context.Background(), res)

	require.Error(t, err)
	assert.ErrorIs(t, err, migration.ErrFenceUnconfirmed)
	assert.ErrorIs(t, err, gateway.ErrGatewayChanged)
	require.Len(t, patches, 2, "the fence, then the refused removal")
	assert.Contains(t, run.out.String(), "metadata.generation is 6, kcp expected 5. Stopping with no gateway write — fence applied but not confirmed on every gateway pod.")
}

func TestD2SOrchestrator_Execute_RefusalDuringAbortFence_StopsWithNoWrite(t *testing.T) {
	t.Setenv(killpoint.FailEnvVar, EventVerifyFence)
	run, gw, res := generationRun(cleanWorld(), false)
	var patches []gateway.RoutePatch
	gw.patchRouteGenFn = scriptedRoutePatches(&patches, patchedTo(5), refusedAs(6))

	err := run.execute(context.Background(), res)

	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrGatewayChanged)
	assert.Contains(t, err.Error(), killpoint.FailEnvVar, "the originating failure still surfaces")
	require.Len(t, patches, 2, "the fence, then the refused unfence")
	assert.Contains(t, run.out.String(), changedCR(5, 6).Summary("fence applied, not verified"))
	assert.Equal(t, StateFenced, run.state())
}

func TestD2SOrchestrator_Execute_SwitchRefusal_StopsWithNoFenceKeptMessage(t *testing.T) {
	run, gw, res := generationRun(cleanWorld(), false)
	var patches []gateway.RoutePatch
	gw.patchRouteGenFn = scriptedRoutePatches(&patches, patchedTo(5), refusedAs(6))
	gw.getGatewayGenerationFn = func(context.Context, string, string) (int64, error) { return 5, nil }

	err := run.execute(context.Background(), res)

	require.ErrorIs(t, err, gateway.ErrGatewayChanged)
	assert.Contains(t, run.out.String(), changedCR(5, 6).Summary("offsets synced, route not switched"))
	assert.NotContains(t, run.out.String(), "keeping the fence")
}

func TestStoppedAt(t *testing.T) {
	tests := []struct {
		state         string
		fencedAtStart bool
		want          string
	}{
		{StateUninitialized, false, "no fence applied"},
		{StateInitialized, false, "no fence applied"},
		{StateUninitialized, true, "an earlier run's fence applied"},
		{StateInitialized, true, "an earlier run's fence applied"},
		{StateFenced, false, "fence applied, not verified"},
		{StateFenced, true, "fence applied, not verified"},
		{StateFenceVerified, false, "fence applied, offsets not synced"},
		{StateOffsetsSynced, true, "offsets synced, route not switched"},
		{StateSwitched, false, "route switched"},
		{"some_future_state", false, "run stopped at state some_future_state"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%v", tt.state, tt.fencedAtStart), func(t *testing.T) {
			assert.Equal(t, tt.want, stoppedAt(tt.state, tt.fencedAtStart))
		})
	}
}
