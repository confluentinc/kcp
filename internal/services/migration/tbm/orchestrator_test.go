package tbm

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw)
	orchestrator := NewTBMOrchestrator(config, actions, state, stateFile)
	return orchestrator, config, stateFile
}

func TestTBMOrchestrator_Execute_WalksEveryStepFromUninitialized(t *testing.T) {
	orchestrator, config, stateFile := newTestOrchestrator(t, StateUninitialized)

	require.NoError(t, orchestrator.Execute(context.Background(), realisticReconcileResult(), 10))

	assert.Equal(t, StateSwitched, config.CurrentState)
	assert.False(t, orchestrator.HasPendingWork())

	loaded, err := NewTBMStateFromFile(stateFile)
	require.NoError(t, err)
	persisted, err := loaded.GetMigrationById("test-tbm-1")
	require.NoError(t, err)
	assert.Equal(t, StateSwitched, persisted.CurrentState)
}

func TestTBMOrchestrator_Execute_ResumesFromPartialState(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateFenced)

	require.NoError(t, orchestrator.Execute(context.Background(), &migplan.Result{}, 10))

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

	err := orchestrator.Execute(context.Background(), &migplan.Result{}, 10)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unrecognized")
}

func TestTBMOrchestrator_Execute_CtxCancellationStopsAtLastCompletedStep(t *testing.T) {
	orchestrator, config, _ := newTestOrchestrator(t, StateUninitialized)
	// Long enough that a 10ms ctx timeout reliably wins the race, short enough
	// to keep the test fast. Initialize/wait_for_lags/fence are all real now
	// but fast (mocked gateway calls return instantly, zero lag needs no
	// polling), so the 10ms budget is still spent waiting on one of the
	// remaining noop steps' simulated delay, same as before.
	TransitionSimulatedDelay = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := orchestrator.Execute(ctx, realisticReconcileResult(), 10)

	require.Error(t, err)
	assert.NotEqual(t, StateSwitched, config.CurrentState)
}

func TestTBMOrchestrator_Execute_InitializeCapturesReconcileArtifacts(t *testing.T) {
	orchestrator, config, stateFile := newTestOrchestrator(t, StateUninitialized)

	res := realisticReconcileResult()

	require.NoError(t, orchestrator.Execute(context.Background(), res, 10))

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

	err := orchestrator.Execute(context.Background(), res, 10)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "topic t1.order has replication lag")
	assert.Contains(t, err.Error(), "gateway rejected the fence spec")
	assert.Equal(t, StateUninitialized, config.CurrentState)
	assert.Empty(t, config.Topics)
}
