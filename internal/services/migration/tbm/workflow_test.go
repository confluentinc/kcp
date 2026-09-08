package tbm

import (
	"context"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setFastTransitions shrinks TransitionSimulatedDelay to 1ms for the duration
// of a test, restoring the original value on cleanup. Used by every test in
// this package that calls an action or drives the orchestrator through a real
// transition, so the suite runs in milliseconds rather than minutes.
func setFastTransitions(t *testing.T) {
	t.Helper()
	original := TransitionSimulatedDelay
	TransitionSimulatedDelay = time.Millisecond
	t.Cleanup(func() { TransitionSimulatedDelay = original })
}

func TestTBMActions_EachMethodSucceeds(t *testing.T) {
	setFastTransitions(t)
	actions := NewTBMActions()
	config := &TBMConfig{MigrationId: "tbm-1", CurrentState: StateUninitialized}
	ctx := context.Background()

	require.NoError(t, actions.Initialize(ctx, config, &migplan.Result{}))
	require.NoError(t, actions.WaitForLags(ctx, config))
	require.NoError(t, actions.Fence(ctx, config))
	require.NoError(t, actions.VerifyFence(ctx, config))
	require.NoError(t, actions.Promote(ctx, config))
	require.NoError(t, actions.Switch(ctx, config))
}

func TestTBMActions_CtxCancellationExitsPromptly(t *testing.T) {
	// Deliberately NOT setFastTransitions: this proves cancellation wins the
	// race against the real 7s default, not against an already-short delay.
	// WaitForLags (not Initialize) exercises this: Initialize no longer sleeps
	// — its work (validating an already-computed reconcile plan) is pure and
	// instant, so it has nothing left to cancel.
	actions := NewTBMActions()
	config := &TBMConfig{MigrationId: "tbm-1", CurrentState: StateUninitialized}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := actions.WaitForLags(ctx, config)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 1*time.Second, "expected cancellation to exit well before the 7s simulated delay")
}

func TestTBMActions_Initialize_CopiesReconcileArtifactsOntoConfig(t *testing.T) {
	actions := NewTBMActions()
	config := &TBMConfig{MigrationId: "tbm-1", CurrentState: StateUninitialized}
	res := &migplan.Result{
		Topics:         []string{"t1.order"},
		FenceYAML:      "rules:\n  fenced: true\n",
		SwitchoverYAML: "rules:\n  switched: true\n",
		GatewayYAML:    "apiVersion: v1\nkind: Gateway\n",
	}

	require.NoError(t, actions.Initialize(context.Background(), config, res))

	assert.Equal(t, res.Topics, config.Topics)
	assert.Equal(t, res.FenceYAML, config.FenceYAML)
	assert.Equal(t, res.SwitchoverYAML, config.SwitchoverYAML)
	assert.Equal(t, res.GatewayYAML, config.GatewayYAML)
}

func TestTBMActions_Initialize_RefusedPlanFailsWithReasonsAndDoesNotMutateConfig(t *testing.T) {
	actions := NewTBMActions()
	config := &TBMConfig{MigrationId: "tbm-1", CurrentState: StateUninitialized}
	res := &migplan.Result{Refused: true, Reasons: []string{"topic t1.order has replication lag"}}

	err := actions.Initialize(context.Background(), config, res)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "topic t1.order has replication lag")
	assert.Empty(t, config.Topics)
	assert.Empty(t, config.FenceYAML)
}
