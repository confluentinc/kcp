package tbm

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestOrchestrator(t *testing.T, initialState string) (*TBMOrchestrator, *TBMConfig, string) {
	t.Helper()
	setFastTransitions(t)

	config := &TBMConfig{
		MigrationId:   "test-tbm-1",
		CurrentState:  initialState,
		ManifestHash:  "deadbeef",
		K8sNamespace:  "confluent",
		InitialCrName: "gateway-initial",
	}
	state := NewTBMState()
	stateFile := filepath.Join(t.TempDir(), "tbm-state.json")
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(context.Context, string, string, []byte, string) (string, error) { return "", nil },
	}
	// Configured (not the bare zero-value mock) because realisticReconcileResult
	// (used by several tests below) sets Topics: []string{"t1.order"}, which a
	// full uninitialized->switched walk carries all the way into Promote —
	// an unconfigured mock would fail there with "not configured".
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: name})
			}
			return resp, nil
		},
		listMirrorTopicsFn: func(context.Context, clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{{MirrorTopicName: "t1.order", MirrorStatus: clusterlink.MirrorStatusStopped}}, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, cl)
	actions.promotePollInterval = time.Millisecond
	orchestrator := NewTBMOrchestrator(config, actions, state, stateFile)
	return orchestrator, config, stateFile
}

func TestTBMOrchestrator_Execute_WalksEveryStepFromUninitialized(t *testing.T) {
	orchestrator, config, stateFile := newTestOrchestrator(t, StateUninitialized)

	require.NoError(t, orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{}))

	assert.Equal(t, StateSwitched, config.CurrentState)
	assert.False(t, orchestrator.HasPendingWork())

	loaded, err := NewTBMStateFromFile(stateFile)
	require.NoError(t, err)
	persisted, err := loaded.GetMigrationById("test-tbm-1")
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, persisted.CurrentState)
}

func TestTBMOrchestrator_Execute_ResumesFromPartialState(t *testing.T) {
	// Resuming at fenced is bootstrap-demoted to lags_ok (see
	// TestTBMOrchestrator_Bootstrap_ExpiresFencePostureOnResume), so this walk
	// re-runs Fence for real — harmlessly, since config.Topics is empty here
	// and Fence's own no-topics guard makes it a no-op success.
	orchestrator, config, _ := newTestOrchestrator(t, StateFenced)

	require.NoError(t, orchestrator.Execute(context.Background(), &migplan.Result{}, 10, 0, clusterlink.BasicAuth{}))

	assert.Equal(t, StateSwitched, config.CurrentState)
}

func TestTBMOrchestrator_HasPendingWork(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  bool
	}{
		{"uninitialized has work", StateUninitialized, true},
		{"switched has no work", StateSwitched, false},
		{"unknown state reports pending", "some-future-state", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator, _, _ := newTestOrchestrator(t, tt.state)
			assert.Equal(t, tt.want, orchestrator.HasPendingWork())
		})
	}
}

func TestTBMOrchestrator_Execute_RefusesUnknownState(t *testing.T) {
	orchestrator, _, _ := newTestOrchestrator(t, "some-future-state")

	err := orchestrator.Execute(context.Background(), &migplan.Result{}, 10, 0, clusterlink.BasicAuth{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unrecognized")
}

func TestTBMOrchestrator_Execute_CtxCancellationStopsAtLastCompletedStep(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateUninitialized)
	// Force the fence step to block on ctx: a real gateway wait that never
	// resolves on its own, cancellable only by ctx, is what proves the walk
	// stops mid-step rather than after the whole Execute call completes.
	orchestrator.actions.gatewayService.(*mockGatewayService).waitForGatewayAcceptedFn = func(ctx context.Context, _, _ string, _, _ time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := orchestrator.Execute(ctx, realisticReconcileResult(), 10, 0, clusterlink.BasicAuth{})

	require.Error(t, err)
	assert.NotEqual(t, StateSwitched, config.CurrentState)
}

func TestTBMOrchestrator_Execute_InitializeCapturesReconcileArtifacts(t *testing.T) {
	orchestrator, config, stateFile := newTestOrchestrator(t, StateUninitialized)

	res := realisticReconcileResult()

	require.NoError(t, orchestrator.Execute(context.Background(), res, 10, 0, clusterlink.BasicAuth{}))

	assert.Equal(t, res.Topics, config.Topics)
	assert.Equal(t, res.FenceYAML, config.FenceYAML)
	assert.Equal(t, res.SwitchoverYAML, config.SwitchoverYAML)
	assert.Equal(t, res.GatewayYAML, config.GatewayYAML)
	assert.Equal(t, res.Route, config.Route)

	loaded, err := NewTBMStateFromFile(stateFile)
	require.NoError(t, err)
	persisted, err := loaded.GetMigrationById("test-tbm-1")
	require.NoError(t, err)
	assert.Equal(t, res.Topics, persisted.Topics)
	assert.Equal(t, res.FenceYAML, persisted.FenceYAML)
	assert.Equal(t, res.SwitchoverYAML, persisted.SwitchoverYAML)
	assert.Equal(t, res.GatewayYAML, persisted.GatewayYAML)
	assert.Equal(t, res.Route, persisted.Route)
}

func TestTBMOrchestrator_Execute_RefusedReconcilePlanFailsAndConfigNotAdvanced(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateUninitialized)

	res := &migplan.Result{Refused: true, Reasons: []string{"topic t1.order has replication lag", "gateway rejected the fence spec"}}

	err := orchestrator.Execute(context.Background(), res, 10, 0, clusterlink.BasicAuth{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "topic t1.order has replication lag")
	assert.Contains(t, err.Error(), "gateway rejected the fence spec")
	assert.Equal(t, StateUninitialized, config.CurrentState)
	assert.Empty(t, config.Topics)
}

func TestTBMOrchestrator_Execute_UnroutedProducersDetected_UnfencesAndRollsBackToLagsOk(t *testing.T) {
	var applyCount int
	var lastAppliedYAML []byte
	var call int32
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, yamlData []byte, configID string) (string, error) {
			applyCount++
			lastAppliedYAML = yamlData
			return "", nil
		},
	}
	cl := &mockClusterLinkService{}
	sourceOffset := &mockOffsetProvider{
		// a.sourceOffset is shared between wait_for_lags's own offset sweep and
		// verify_fence's two snapshots (both read the same live source
		// cluster), so the first call here is wait_for_lags's sweep, not
		// verify_fence's baseline — the first two calls hold steady at 1000
		// (wait_for_lags's sweep, then verify_fence's baseline snapshot) and
		// only the third (verify_fence's post-window snapshot) shows the rise.
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt32(&call, 1)
			if n <= 2 {
				return map[int32]int64{0: 1000}, nil
			}
			return map[int32]int64{0: 1500}, nil // rogue producer during the window
		},
	}
	config := &TBMConfig{
		MigrationId:   "test-tbm-rollback",
		CurrentState:  StateUninitialized,
		K8sNamespace:  "confluent",
		InitialCrName: "gateway-initial",
	}
	state := NewTBMState()
	stateFile := filepath.Join(t.TempDir(), "tbm-state.json")
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), gw, cl)
	orchestrator := NewTBMOrchestrator(config, actions, state, stateFile)

	err := orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 5*time.Millisecond, clusterlink.BasicAuth{})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)
	assert.Equal(t, StateLagsOk, config.CurrentState, "a detected rollback must leave the batch at lags_ok, not fenced")
	assert.Equal(t, 2, applyCount, "fence applies once, the abort_fence rollback's unfence applies once more")

	// testGatewayYAML's migration-route already carries a rules.routing block
	// before any fence — only FenceYAML's "fencing" key is grafted on top of
	// it (see realisticReconcileResult's FenceYAML). So the unfenced route's
	// rules must still have routing (never removed) and must NOT have
	// fencing (the thing the rollback undoes) — not "no rules at all".
	var applied map[string]interface{}
	require.NoError(t, yamlUnmarshalForTest(t, lastAppliedYAML, &applied))
	spec, ok := applied["spec"].(map[string]interface{})
	require.True(t, ok)
	routes, ok := spec["routes"].([]interface{})
	require.True(t, ok)
	route, ok := routes[0].(map[string]interface{})
	require.True(t, ok)
	rules, ok := route["rules"].(map[string]interface{})
	require.True(t, ok, "the unfenced route must still carry its original rules.routing block")
	_, hasFencing := rules["fencing"]
	assert.False(t, hasFencing, "the unfenced route must not carry the fencing block the rollback is undoing")
	_, hasRouting := rules["routing"]
	assert.True(t, hasRouting, "the unfenced route must still have the routing block testGatewayYAML always had")

	loaded, err := NewTBMStateFromFile(stateFile)
	require.NoError(t, err)
	persisted, err := loaded.GetMigrationById("test-tbm-rollback")
	require.NoError(t, err)
	assert.Equal(t, StateLagsOk, persisted.CurrentState, "the rolled-back state must be persisted")
}

func TestTBMOrchestrator_Execute_StableOffsets_NoRollback(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateUninitialized)

	err := orchestrator.Execute(context.Background(), realisticReconcileResult(), 10, 5*time.Millisecond, clusterlink.BasicAuth{})

	require.NoError(t, err)
	assert.Equal(t, StateSwitched, config.CurrentState)
}

func TestTBMOrchestrator_Bootstrap_ExpiresFenceVerificationOnResume(t *testing.T) {
	config := &TBMConfig{MigrationId: "t1", CurrentState: StateFenceVerified, K8sNamespace: "confluent", InitialCrName: "gw"}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	state := NewTBMState()
	NewTBMOrchestrator(config, actions, state, filepath.Join(t.TempDir(), "s.json"))

	assert.Equal(t, StateFenced, config.CurrentState, "fence_verified is a point-in-time attestation and must not survive a restart")
}

func TestTBMOrchestrator_Bootstrap_ExpiresFencePostureOnResume(t *testing.T) {
	config := &TBMConfig{MigrationId: "t1", CurrentState: StateFenced, K8sNamespace: "confluent", InitialCrName: "gw"}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	state := NewTBMState()
	NewTBMOrchestrator(config, actions, state, filepath.Join(t.TempDir(), "s.json"))

	assert.Equal(t, StateLagsOk, config.CurrentState, "a resume at fenced must demote to lags_ok so it re-asserts the fence rather than trusting a posture that may not still hold")
}
