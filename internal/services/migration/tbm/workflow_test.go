package tbm

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockOffsetProvider implements offset.Provider using function fields for
// test control, mirroring migration's own (unexported, package-private)
// mockOffsetProvider — this is TBM's own copy: test
// doubles are kept per-package even where the underlying interface is
// shared.
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
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) { return "", nil },
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
	config := &migration.MigrationConfig{MigrationId: "tbm-1"}
	res := &migplan.Result{
		Route:          "migration-route",
		PromoteTopics:  []string{"t1.order"},
		AwaitStopped:   []string{"t1.order"},
		FenceYAML:      "rules:\n  fenced: true\n",
		SwitchoverYAML: "rules:\n  switched: true\n",
		GatewayYAML:    "apiVersion: v1\nkind: Gateway\n",
		Mode:           "dynamic",
	}

	require.NoError(t, actions.Initialize(context.Background(), config, res))

	assert.Equal(t, res.Route, config.Route)
	assert.Equal(t, res.PromoteTopics, config.Topics)
	assert.Equal(t, res.AwaitStopped, config.AwaitStopped)
	assert.Equal(t, res.FenceYAML, config.FenceYAML)
	assert.Equal(t, res.SwitchoverYAML, config.SwitchoverYAML)
	assert.Equal(t, res.GatewayYAML, config.GatewayYAML)
	// Mode must be persisted, mirroring AAO's Initialize: the unified `execute`
	// dispatcher reads config.Mode on resume, so an interrupted dynamic
	// migration that dropped Mode here would be re-dispatched to the static
	// (AAO) branch on its next run.
	assert.Equal(t, res.Mode, config.Mode)
}

func TestTBMActions_Initialize_RefusedPlanFailsWithReasonsAndDoesNotMutateConfig(t *testing.T) {
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	config := &migration.MigrationConfig{MigrationId: "tbm-1"}
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
	config := &migration.MigrationConfig{Topics: []string{"topic-1", "topic-2"}}

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
	config := &migration.MigrationConfig{Topics: []string{}}

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
	config := &migration.MigrationConfig{Topics: []string{"topic-1"}}

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
	config := &migration.MigrationConfig{Topics: []string{"topic-1"}}

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
	config := &migration.MigrationConfig{Topics: []string{"topic-1"}}

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
	config := &migration.MigrationConfig{Topics: []string{"topic-1"}}

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
	config := &migration.MigrationConfig{Topics: []string{"topic-1"}}

	err := actions.WaitForLags(context.Background(), config, 10)
	require.NoError(t, err, "four non-consecutive failures must not abort")
	assert.Equal(t, int32(6), calls.Load())
}

func promoteTestConfig(topics []string) *migration.MigrationConfig {
	return &migration.MigrationConfig{
		MigrationId:         "tbm-promote-1",
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

// Resume-from-PENDING_STOPPED (TBM): a topic reconcile classified AwaitStopped
// is already mid-promotion, so Promote must WAIT for it to reach STOPPED and
// never re-issue a promote on it. Mirrors the AAO guard.
func TestTBMActions_Promote_AwaitStoppedTopicsAreWaitedNotRepromoted(t *testing.T) {
	var promoted []string // Promote is synchronous — no locking needed
	var listCalls int64
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			promoted = append(promoted, topicNames...)
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
			status := "PENDING_STOPPED"
			if atomic.AddInt64(&listCalls, 1) >= 3 {
				status = clusterlink.MirrorStatusStopped
			}
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "await-me", MirrorStatus: status},
				{MirrorTopicName: "migrate-me", MirrorStatus: status},
			}, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, cl)
	actions.promotePollInterval = time.Millisecond
	config := promoteTestConfig([]string{"await-me", "migrate-me"})
	config.AwaitStopped = []string{"await-me"}

	err := actions.Promote(context.Background(), config, clusterlink.BasicAuth{})
	require.NoError(t, err)
	assert.NotContains(t, promoted, "await-me",
		"an AwaitStopped topic (already promoting on resume) must be waited on, never re-promoted")
	assert.Contains(t, promoted, "migrate-me",
		"the genuinely migratable topic must still be promoted")
}

// The intra-promote kill-point (killpoint.AfterPromoteAccepted) must interrupt
// Promote right after a promote request is accepted but before the mirror is
// confirmed STOPPED — leaving it PENDING_STOPPED — and exit with a non-zero
// error (no rollback, since it is not ErrUnroutedProducers). This is the seam
// the live suite drives to leave a genuine PENDING_STOPPED world.
func TestTBMActions_Promote_KillPointAfterAcceptExitsBeforeConfirm(t *testing.T) {
	t.Setenv(killpoint.EnvVar, killpoint.AfterPromoteAccepted)
	var promoted, listed int64
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, names []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			atomic.AddInt64(&promoted, 1)
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, n := range names {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: n})
			}
			return resp, nil
		},
		listMirrorTopicsFn: func(context.Context, clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			atomic.AddInt64(&listed, 1)
			return []clusterlink.MirrorTopic{{MirrorTopicName: "topic-1", MirrorStatus: "PENDING_STOPPED"}}, nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), &mockGatewayService{}, cl)
	actions.promotePollInterval = time.Millisecond
	config := promoteTestConfig([]string{"topic-1"})

	err := actions.Promote(context.Background(), config, clusterlink.BasicAuth{})
	require.Error(t, err, "the kill-point after accept must exit non-zero")
	require.Contains(t, err.Error(), "kill-point")
	assert.Equal(t, int64(1), atomic.LoadInt64(&promoted), "the promote request must have been issued (accepted) before the kill-point")
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
	config := &migration.MigrationConfig{Topics: []string{"t1.order"}}

	err := actions.VerifyFence(context.Background(), config, 0)
	require.NoError(t, err)
}

func TestTBMActions_VerifyFence_StableOffsets_Passes(t *testing.T) {
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{0: 1000}, nil },
	}
	actions := NewTBMActions(sourceOffset, zeroLagOffsetProvider(), &mockGatewayService{}, &mockClusterLinkService{})
	config := &migration.MigrationConfig{Topics: []string{"t1.order"}}

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
	config := &migration.MigrationConfig{Topics: []string{"t1.order"}}

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
	config := &migration.MigrationConfig{Topics: []string{"t1.order"}}

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
	config := &migration.MigrationConfig{Topics: []string{"t1.order"}}

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
	config := &migration.MigrationConfig{Topics: []string{"t1.order"}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := actions.VerifyFence(ctx, config, 20*time.Second)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 1*time.Second, "expected cancellation to exit well before the 20s monitoring window")
}

// ===========================================================================
// Fence/Switch no-op guard tests — prove the guard is keyed on the plan's
// own artifact (config.FenceYAML / config.SwitchoverYAML), never on
// config.Topics (the promote set). The case this covers: an all-promoted
// batch has an empty promote set (Topics) but still owes a fence/switch
// (FenceYAML/SwitchoverYAML non-empty) — gating on Topics would wrongly
// no-op it and report the batch complete without cutting over.
// ===========================================================================

// TestTBM_Fence_NoFenceYAMLIsNoop proves an empty FenceYAML artifact
// short-circuits Fence even when Topics is non-empty — the guard reads the
// plan's artifact, not the promote set.
func TestTBM_Fence_NoFenceYAMLIsNoop(t *testing.T) {
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			t.Fatal("DetectCapability must not be called when FenceYAML is empty")
			return gateway.Capability{}, nil
		},
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
			t.Fatal("PatchGatewayRoute must not be called when FenceYAML is empty")
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()
	config.FenceYAML = ""                // artifact empty
	config.Topics = []string{"t1.order"} // promote set still non-empty

	err := actions.Fence(context.Background(), config)
	require.NoError(t, err, "Fence with empty FenceYAML must no-op, not error")
}

// TestTBM_Fence_NonEmptyFenceYAMLButNoTopics_StillFences:
// Topics empty (nothing left to promote — an all-promoted batch) but
// FenceYAML still set (a fence is still owed ahead of switchover). Fence
// must still apply exactly one patch, not no-op.
func TestTBM_Fence_NonEmptyFenceYAMLButNoTopics_StillFences(t *testing.T) {
	var applyCalls int
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
			applyCalls++
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()
	config.Topics = nil // no topics left to promote

	err := actions.Fence(context.Background(), config)
	require.NoError(t, err, "Fence with FenceYAML set + no Topics must still fence")
	assert.Equal(t, 1, applyCalls, "expected exactly one gateway patch")
}

// TestTBM_Switch_NoSwitchoverYAMLIsNoop proves an empty SwitchoverYAML
// artifact short-circuits Switch even when Topics is non-empty.
func TestTBM_Switch_NoSwitchoverYAMLIsNoop(t *testing.T) {
	gw := &mockGatewayService{
		detectCapabilityFn: func(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
			t.Fatal("DetectCapability must not be called when SwitchoverYAML is empty")
			return gateway.Capability{}, nil
		},
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
			t.Fatal("PatchGatewayRoute must not be called when SwitchoverYAML is empty")
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()
	config.SwitchoverYAML = ""           // artifact empty
	config.Topics = []string{"t1.order"} // promote set still non-empty

	err := actions.Switch(context.Background(), config)
	require.NoError(t, err, "Switch with empty SwitchoverYAML must no-op, not error")
}

// TestTBM_Switch_NonEmptySwitchoverYAMLButNoTopics_StillSwitches: Topics
// empty (all-promoted batch) but SwitchoverYAML still set (the switch is
// still owed). Switch must still apply exactly one patch.
func TestTBM_Switch_NonEmptySwitchoverYAMLButNoTopics_StillSwitches(t *testing.T) {
	var applyCalls int
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
			applyCalls++
			return "", nil
		},
	}
	actions := NewTBMActions(zeroLagOffsetProvider(), zeroLagOffsetProvider(), gw, &mockClusterLinkService{})
	config := testTBMConfig()
	config.Topics = nil // no topics left to promote

	err := actions.Switch(context.Background(), config)
	require.NoError(t, err, "Switch with SwitchoverYAML set + no Topics must still switch")
	assert.Equal(t, 1, applyCalls, "expected exactly one gateway patch")
}
