package tbm

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(context.Context, string, string, []byte, string) (string, error) { return "", nil },
	}
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
	config := testTBMConfig()
	config.CurrentState = StateUninitialized
	ctx := context.Background()

	// Initialize captures the reconcile plan's artifacts (FenceYAML,
	// GatewayYAML, Route, ...) onto config, overwriting whatever testTBMConfig
	// set — so a self-consistent plan must be fed here for the real Fence
	// below to have a valid CR/route/rules to work with.
	require.NoError(t, actions.Initialize(ctx, config, realisticReconcileResult()))
	require.NoError(t, actions.WaitForLags(ctx, config, 10))
	require.NoError(t, actions.Fence(ctx, config))
	require.NoError(t, actions.VerifyFence(ctx, config, 0))
	require.NoError(t, actions.Promote(ctx, config, clusterlink.BasicAuth{}))
	require.NoError(t, actions.Switch(ctx, config))
}

func TestTBMActions_Initialize_CopiesReconcileArtifactsOntoConfig(t *testing.T) {
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	config := &TBMConfig{MigrationId: "tbm-1", CurrentState: StateUninitialized}
	res := &migplan.Result{
		Route:          "migration-route",
		Topics:         []string{"t1.order"},
		FenceYAML:      "rules:\n  fenced: true\n",
		SwitchoverYAML: "rules:\n  switched: true\n",
		GatewayYAML:    "apiVersion: v1\nkind: Gateway\n",
	}

	require.NoError(t, actions.Initialize(context.Background(), config, res))

	assert.Equal(t, res.Route, config.Route)
	assert.Equal(t, res.Topics, config.Topics)
	assert.Equal(t, res.FenceYAML, config.FenceYAML)
	assert.Equal(t, res.SwitchoverYAML, config.SwitchoverYAML)
	assert.Equal(t, res.GatewayYAML, config.GatewayYAML)
}

func TestTBMActions_Initialize_RefusedPlanFailsWithReasonsAndDoesNotMutateConfig(t *testing.T) {
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
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

	actions := NewTBMActions(sourceOffset, destOffset, &mockGatewayService{}, &mockClusterLinkService{})
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

	actions := NewTBMActions(sourceOffset, destOffset, &mockGatewayService{}, &mockClusterLinkService{})
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

	actions := NewTBMActions(sourceOffset, destOffset, &mockGatewayService{}, &mockClusterLinkService{})
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

	actions := NewTBMActions(sourceOffset, destOffset, &mockGatewayService{}, &mockClusterLinkService{})
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

	actions := NewTBMActions(sourceOffset, destOffset, &mockGatewayService{}, &mockClusterLinkService{})
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

	actions := NewTBMActions(sourceOffset, destOffset, &mockGatewayService{}, &mockClusterLinkService{})
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

	actions := NewTBMActions(sourceOffset, destOffset, &mockGatewayService{}, &mockClusterLinkService{})
	actions.lagPollInterval = time.Millisecond
	config := &TBMConfig{Topics: []string{"topic-1"}}

	err := actions.WaitForLags(context.Background(), config, 10)
	require.NoError(t, err, "four non-consecutive failures must not abort")
	assert.Equal(t, int32(6), calls.Load())
}

func promoteTestConfig(topics []string) *TBMConfig {
	return &TBMConfig{
		MigrationId:         "tbm-promote-1",
		CurrentState:        StateFenceVerified,
		Topics:              topics,
		ClusterId:           "lkc-123",
		ClusterRestEndpoint: "https://cluster.example.com",
		ClusterLinkName:     "link-1",
	}
}

func TestTBMActions_Promote_NoTopics_ReturnsImmediately(t *testing.T) {
	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(context.Context, clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			t.Fatal("ListMirrorTopics must not be called when there are no topics to promote")
			return nil, nil
		},
		promoteMirrorTopicsFn: func(context.Context, clusterlink.Config, []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			t.Fatal("PromoteMirrorTopics must not be called when there are no topics to promote")
			return nil, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, cl)
	config := promoteTestConfig(nil)

	err := actions.Promote(context.Background(), config, clusterlink.BasicAuth{})
	require.NoError(t, err, "an empty topic list is a legitimate no-op, not an error")
}

func TestTBMActions_Promote_AllAtZeroLag_PromotesAndConfirmsStopped(t *testing.T) {
	promoted := make(map[string]bool)
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				promoted[name] = true
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: name})
			}
			return resp, nil
		},
		listMirrorTopicsFn: func(context.Context, clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: clusterlink.MirrorStatusStopped},
				{MirrorTopicName: "topic-2", MirrorStatus: clusterlink.MirrorStatusStopped},
			}, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, cl)
	actions.promotePollInterval = time.Millisecond
	config := promoteTestConfig([]string{"topic-1", "topic-2"})

	err := actions.Promote(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
	assert.True(t, promoted["topic-1"])
	assert.True(t, promoted["topic-2"])
}

func TestTBMActions_Promote_WaitsForPendingStoppedUntilStopped(t *testing.T) {
	var listCalls int64
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
			n := atomic.AddInt64(&listCalls, 1)
			status := "PENDING_STOPPED"
			if n >= 3 {
				status = clusterlink.MirrorStatusStopped
			}
			return []clusterlink.MirrorTopic{{MirrorTopicName: "topic-1", MirrorStatus: status}}, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, cl)
	actions.promotePollInterval = time.Millisecond
	config := promoteTestConfig([]string{"topic-1"})

	err := actions.Promote(context.Background(), config, clusterlink.BasicAuth{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, atomic.LoadInt64(&listCalls), int64(3),
		"expected Promote to poll mirror status until STOPPED was observed")
}

func TestTBMActions_Promote_BatchSize_ProcessesSequentially(t *testing.T) {
	const batchSize = 5
	topics := make([]string, 12)
	for i := range topics {
		topics[i] = fmt.Sprintf("topic-%02d", i)
	}

	var mu sync.Mutex
	inFlight := make(map[string]bool)
	pollsSince := make(map[string]int)
	var promoteCallSizes []int

	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			if len(inFlight) != 0 {
				t.Errorf("promoted a new batch of %d while %d topics still in flight", len(topicNames), len(inFlight))
			}
			promoteCallSizes = append(promoteCallSizes, len(topicNames))
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				inFlight[name] = true
				pollsSince[name] = 0
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: name})
			}
			return resp, nil
		},
		listMirrorTopicsFn: func(context.Context, clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			mu.Lock()
			defer mu.Unlock()
			out := make([]clusterlink.MirrorTopic, 0, len(topics))
			for _, name := range topics {
				status := clusterlink.MirrorStatusActive
				if _, promoted := pollsSince[name]; promoted {
					pollsSince[name]++
					if pollsSince[name] >= 2 {
						status = clusterlink.MirrorStatusStopped
						delete(inFlight, name)
					} else {
						status = "PENDING_STOPPED"
					}
				}
				out = append(out, clusterlink.MirrorTopic{MirrorTopicName: name, MirrorStatus: status})
			}
			return out, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, cl)
	actions.promotePollInterval = time.Millisecond
	actions.SetPromoteBatchSize(batchSize)
	config := promoteTestConfig(topics)

	err := actions.Promote(context.Background(), config, clusterlink.BasicAuth{})
	require.NoError(t, err)
	for _, size := range promoteCallSizes {
		assert.LessOrEqualf(t, size, batchSize, "no promote call may exceed the configured batch size")
	}
}

func TestTBMActions_Promote_MaxRetriesExceeded_FailsAfterThreeAttempts(t *testing.T) {
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: name, ErrorCode: 1, ErrorMessage: "persistent error"})
			}
			return resp, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, cl)
	actions.promotePollInterval = time.Millisecond
	config := promoteTestConfig([]string{"topic-1"})

	err := actions.Promote(context.Background(), config, clusterlink.BasicAuth{})
	require.Error(t, err)
	assert.Equal(t, "topic topic-1 failed promotion after 3 attempts: persistent error", err.Error())
}

func TestTBMActions_Promote_ToleratesTransientSweepFailures(t *testing.T) {
	var sweepAttempts int64
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&sweepAttempts, 1)
			if n <= 2 {
				return nil, fmt.Errorf("transient network error")
			}
			return map[int32]int64{0: 1000}, nil
		},
	}
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
			return []clusterlink.MirrorTopic{{MirrorTopicName: "topic-1", MirrorStatus: clusterlink.MirrorStatusStopped}}, nil
		},
	}
	actions := NewTBMActions(offsetProvider, offsetProvider, &mockGatewayService{}, cl)
	actions.promotePollInterval = time.Millisecond
	config := promoteTestConfig([]string{"topic-1"})

	err := actions.Promote(context.Background(), config, clusterlink.BasicAuth{})
	require.NoError(t, err, "must tolerate up to maxConsecutiveSweepFailures-1 transient sweep failures")
}

func TestTBMActions_Promote_AbortsAfterMaxConsecutiveSweepFailures(t *testing.T) {
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return nil, fmt.Errorf("persistent network error")
		},
	}
	actions := NewTBMActions(offsetProvider, offsetProvider, &mockGatewayService{}, &mockClusterLinkService{})
	actions.promotePollInterval = time.Millisecond
	config := promoteTestConfig([]string{"topic-1"})

	err := actions.Promote(context.Background(), config, clusterlink.BasicAuth{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "offset sweep failed")
}

// ===========================================================================
// VerifyFence tests — mirror migration's TestOrchestrator_Execute_Unrouted*
// suite at the action level (detection logic only; rollback is exercised at
// the orchestrator level in orchestrator_test.go).
// ===========================================================================

func TestTBMActions_VerifyFence_DetectionDisabled_SkipsCheck(t *testing.T) {
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			t.Fatal("GetMany must not be called when detection is disabled")
			return nil, nil
		},
	}
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	config := &TBMConfig{Topics: []string{"t1.order"}}

	err := actions.VerifyFence(context.Background(), config, 0)
	require.NoError(t, err)
}

func TestTBMActions_VerifyFence_StableOffsets_Passes(t *testing.T) {
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{0: 1000}, nil },
	}
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	config := &TBMConfig{Topics: []string{"t1.order"}}

	err := actions.VerifyFence(context.Background(), config, 5*time.Millisecond)
	require.NoError(t, err)
}

func TestTBMActions_VerifyFence_RisingOffset_ReturnsErrUnroutedProducers(t *testing.T) {
	var call int32
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt32(&call, 1)
			if n == 1 {
				return map[int32]int64{0: 1000}, nil
			}
			return map[int32]int64{0: 1500}, nil // rose during the window
		},
	}
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	config := &TBMConfig{Topics: []string{"t1.order"}}

	err := actions.VerifyFence(context.Background(), config, 5*time.Millisecond)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)
	assert.Contains(t, err.Error(), "t1.order partition 0")
	assert.Contains(t, err.Error(), "1000 → 1500")
}

func TestTBMActions_VerifyFence_PartitionAbsentFromFirstSnapshot_TreatedAsZeroBaseline(t *testing.T) {
	var call int32
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt32(&call, 1)
			if n == 1 {
				return map[int32]int64{}, nil // partition 0 doesn't exist yet
			}
			return map[int32]int64{0: 5}, nil // created and written to during the window
		},
	}
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	config := &TBMConfig{Topics: []string{"t1.order"}}

	err := actions.VerifyFence(context.Background(), config, 5*time.Millisecond)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)
	assert.Contains(t, err.Error(), "0 → 5")
}

func TestTBMActions_VerifyFence_FirstSnapshotFetchError_PropagatesWithoutErrUnroutedProducers(t *testing.T) {
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return nil, fmt.Errorf("kafka: connection refused") },
	}
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	config := &TBMConfig{Topics: []string{"t1.order"}}

	err := actions.VerifyFence(context.Background(), config, 5*time.Millisecond)

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrUnroutedProducers)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestTBMActions_VerifyFence_ContextCancelledDuringWindow_ReturnsCtxErr(t *testing.T) {
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{0: 1000}, nil },
	}
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	config := &TBMConfig{Topics: []string{"t1.order"}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := actions.VerifyFence(ctx, config, 20*time.Second)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 1*time.Second, "expected cancellation to exit well before the 20s monitoring window")
}
