package tbm

import (
	"context"
	"fmt"
	"sync/atomic"
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

// mockOffsetProvider implements offset.Provider using function fields for
// test control, mirroring migration's own (unexported, package-private)
// mockOffsetProvider — this is TBM's own copy, not shared, since the two
// packages intentionally have no cross-imports.
type mockOffsetProvider struct {
	getFn     func(topic string) (map[int32]int64, error)
	getManyFn func(topics []string) (map[string]map[int32]int64, error)
}

func (m *mockOffsetProvider) GetMany(_ context.Context, topics []string) (map[string]map[int32]int64, error) {
	if m.getManyFn != nil {
		return m.getManyFn(topics)
	}
	if m.getFn == nil {
		return nil, fmt.Errorf("mockOffsetProvider not configured")
	}
	out := make(map[string]map[int32]int64, len(topics))
	for _, topic := range topics {
		offsets, err := m.getFn(topic)
		if err != nil {
			return nil, err
		}
		out[topic] = offsets
	}
	return out, nil
}

// zeroLagOffsetProvider returns a mockOffsetProvider reporting the same fixed
// offset for every topic. Calling it twice (once for source, once for
// destination) and passing both to NewTBMActions gives every topic zero lag,
// for tests where wait_for_lags (or a full orchestrator walk through it)
// should pass through immediately.
func zeroLagOffsetProvider() *mockOffsetProvider {
	return &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 1000}, nil
		},
	}
}

// zeroLagBatch mirrors migration's own test helper of the same name.
func zeroLagBatch(topics []string, off int64) map[string]map[int32]int64 {
	out := make(map[string]map[int32]int64, len(topics))
	for _, topic := range topics {
		out[topic] = map[int32]int64{0: off}
	}
	return out
}

func TestTBMActions_EachMethodSucceeds(t *testing.T) {
	setFastTransitions(t)
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider())
	config := &TBMConfig{MigrationId: "tbm-1", CurrentState: StateUninitialized, Topics: []string{"topic-1"}}
	ctx := context.Background()

	require.NoError(t, actions.Initialize(ctx, config, &migplan.Result{}))
	require.NoError(t, actions.WaitForLags(ctx, config, 10))
	require.NoError(t, actions.Fence(ctx, config))
	require.NoError(t, actions.VerifyFence(ctx, config))
	require.NoError(t, actions.Promote(ctx, config))
	require.NoError(t, actions.Switch(ctx, config))
}

func TestTBMActions_CtxCancellationExitsPromptly(t *testing.T) {
	// Deliberately NOT setFastTransitions: this proves cancellation wins the
	// race against the real 7s default, not against an already-short delay.
	// Fence (not WaitForLags) exercises this now: WaitForLags is real and has
	// its own dedicated cancellation test below (pre-cancelled ctx, no ticker
	// wait needed), while Fence is still a noop with something to cancel.
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider())
	config := &TBMConfig{MigrationId: "tbm-1", CurrentState: StateUninitialized}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := actions.Fence(ctx, config)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 1*time.Second, "expected cancellation to exit well before the 7s simulated delay")
}

func TestTBMActions_Initialize_CopiesReconcileArtifactsOntoConfig(t *testing.T) {
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider())
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
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider())
	config := &TBMConfig{MigrationId: "tbm-1", CurrentState: StateUninitialized}
	res := &migplan.Result{Refused: true, Reasons: []string{"topic t1.order has replication lag"}}

	err := actions.Initialize(context.Background(), config, res)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "topic t1.order has replication lag")
	assert.Empty(t, config.Topics)
	assert.Empty(t, config.FenceYAML)
}

// ===========================================================================
// WaitForLags tests — mirror migration's TestWorkflow_CheckLags_* suite.
// ===========================================================================

func TestTBMActions_WaitForLags_ImmediatelyBelowThreshold(t *testing.T) {
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{0: 1000}, nil },
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{0: 999}, nil },
	}

	actions := NewTBMActions(sourceOffset, destOffset)
	config := &TBMConfig{Topics: []string{"topic-1", "topic-2"}}

	err := actions.WaitForLags(context.Background(), config, 10)
	require.NoError(t, err)
}

func TestTBMActions_WaitForLags_NoTopics(t *testing.T) {
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{}, nil },
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{}, nil },
	}

	actions := NewTBMActions(sourceOffset, destOffset)
	config := &TBMConfig{Topics: []string{}}

	err := actions.WaitForLags(context.Background(), config, 10)
	require.NoError(t, err)
}

func TestTBMActions_WaitForLags_ContextCancelled(t *testing.T) {
	// Return high lag so the loop does not exit early on threshold.
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{0: 10000}, nil },
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{0: 0}, nil },
	}

	actions := NewTBMActions(sourceOffset, destOffset)
	config := &TBMConfig{Topics: []string{"topic-1"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	err := actions.WaitForLags(ctx, config, 10)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestTBMActions_WaitForLags_DestinationAhead(t *testing.T) {
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{0: 100}, nil },
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{0: 200}, nil }, // ahead of source
	}

	actions := NewTBMActions(sourceOffset, destOffset)
	config := &TBMConfig{Topics: []string{"topic-1"}}

	err := actions.WaitForLags(context.Background(), config, 10)
	require.NoError(t, err, "negative lag (destination ahead) should be treated as 0 and pass threshold")
}

func TestTBMActions_WaitForLags_ToleratesTransientSweepFailures(t *testing.T) {
	// The source sweep fails twice (fewer than maxConsecutiveSweepFailures),
	// then succeeds at zero lag.
	var calls atomic.Int32
	sourceOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			if calls.Add(1) <= 2 {
				return nil, fmt.Errorf("leader election in progress")
			}
			return zeroLagBatch(topics, 1000), nil
		},
	}
	destOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			return zeroLagBatch(topics, 1000), nil
		},
	}

	actions := NewTBMActions(sourceOffset, destOffset)
	actions.lagPollInterval = time.Millisecond
	config := &TBMConfig{Topics: []string{"topic-1"}}

	err := actions.WaitForLags(context.Background(), config, 10)
	require.NoError(t, err, "two transient sweep failures must be ridden out")
	assert.GreaterOrEqual(t, calls.Load(), int32(3), "expected the sweep to be retried on later ticks")
}

func TestTBMActions_WaitForLags_AbortsAfterMaxConsecutiveSweepFailures(t *testing.T) {
	var calls atomic.Int32
	sourceOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			calls.Add(1)
			return nil, fmt.Errorf("broker unreachable")
		},
	}
	destOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			return zeroLagBatch(topics, 1000), nil
		},
	}

	actions := NewTBMActions(sourceOffset, destOffset)
	actions.lagPollInterval = time.Millisecond
	config := &TBMConfig{Topics: []string{"topic-1"}}

	err := actions.WaitForLags(context.Background(), config, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d consecutive", maxConsecutiveSweepFailures))
	assert.Contains(t, err.Error(), "broker unreachable", "the underlying cause must be preserved")
	assert.Equal(t, int32(maxConsecutiveSweepFailures), calls.Load(),
		"the sweep must not be attempted again after the abort threshold")
}

func TestTBMActions_WaitForLags_SweepFailureCounterResetsOnSuccess(t *testing.T) {
	// Scripted sequence: fail, fail, succeed-above-threshold (loop continues),
	// fail, fail, succeed-at-zero-lag. Four total failures but never three in
	// a row — only a counter that resets on success lets this pass.
	var calls atomic.Int32
	sourceOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			switch calls.Add(1) {
			case 1, 2, 4, 5:
				return nil, fmt.Errorf("transient sweep failure")
			case 3:
				return zeroLagBatch(topics, 5000), nil // lag 4000 → above threshold
			default:
				return zeroLagBatch(topics, 1000), nil // lag 0 → done
			}
		},
	}
	destOffset := &mockOffsetProvider{
		getManyFn: func(topics []string) (map[string]map[int32]int64, error) {
			return zeroLagBatch(topics, 1000), nil
		},
	}

	actions := NewTBMActions(sourceOffset, destOffset)
	actions.lagPollInterval = time.Millisecond
	config := &TBMConfig{Topics: []string{"topic-1"}}

	err := actions.WaitForLags(context.Background(), config, 10)
	require.NoError(t, err, "four non-consecutive failures must not abort")
	assert.Equal(t, int32(6), calls.Load())
}
