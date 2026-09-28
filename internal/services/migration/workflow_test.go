package migration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// testInitialCR is a minimal live-read gateway CR with a route named
// migration-route, so both capability detection's fence/switch CR splice
// (deriveFencedCRYAML/deriveSwitchedCRYAML) and the write path's RoutePatch
// derivation (deriveFenceRoutePatch/deriveSwitchRoutePatch) can derive a
// fenced/switched result from it in unit tests.
const testInitialCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gw-1
spec:
  routes:
    - name: migration-route
      endpoint: gateway:9595
`

// testFenceYAML and testSwitchoverYAML are the small, route-agnostic fragments
// migplan.Reconcile returns for a static route (see MigrationConfig.FenceYAML/
// SwitchoverYAML's doc comment) — paired with testInitialCR's one route named
// migration-route.
const (
	testFenceYAML      = "fence:\n  scope: ALL\n  errorCode: BROKER_NOT_AVAILABLE\n"
	testSwitchoverYAML = "streamingDomain:\n  name: confluent-cloud\n  bootstrapServerId: SASL_PLAIN\n"
)

// testReconcileResult returns the migplan.Result Initialize expects to
// receive, carrying testInitialCR's route and fragments so
// ResolveGatewayCapability's fence/switch derivation succeeds against it.
func testReconcileResult() *migplan.Result {
	return &migplan.Result{
		Route:          "migration-route",
		PromoteTopics:  []string{"topic-a", "topic-b", "topic-c"},
		FenceYAML:      testFenceYAML,
		SwitchoverYAML: testSwitchoverYAML,
		GatewayYAML:    testInitialCR,
		Mode:           "static",
	}
}

// ===========================================================================
// Initialize tests
// ===========================================================================

func TestWorkflow_Initialize_Success(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		MigrationId:         "test-1",
		K8sNamespace:        "ns",
		InitialCrName:       "my-gw",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"}, testReconcileResult())
	require.NoError(t, err)

	assert.Equal(t, "migration-route", config.Route)
	assert.Equal(t, "static", config.Mode)
	assert.Equal(t, testInitialCR, config.GatewayYAML)
	assert.Equal(t, testFenceYAML, config.FenceYAML)
	assert.Equal(t, testSwitchoverYAML, config.SwitchoverYAML)
	assert.Len(t, config.Topics, 3)
}

// TestWorkflow_Initialize_RefusedResult proves Initialize refuses outright
// when the migplan.Result it is handed already carries a refusal — mirroring
// TBMActions.Initialize's own res.Refused check. None of the AAO-specific
// preconditions below (gateway capability resolution, cluster-link config
// listing) should run in this case.
func TestWorkflow_Initialize_RefusedResult(t *testing.T) {
	wf := NewMigrationActions(&mockGatewayService{}, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "my-gw"}

	res := &migplan.Result{Refused: true, Reasons: []string{"topic t1: blocked by X", "precondition Y: failed"}}
	err := wf.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"}, res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reconcile plan refused")
	assert.Contains(t, err.Error(), "topic t1: blocked by X")
	assert.Contains(t, err.Error(), "precondition Y: failed")
}

// TestActions_Initialize_ThreadsReconcileResult proves the migplan.Result the
// command layer computed live lands on config exactly as onInitialize used
// to delegate — tested directly against MigrationActions.Initialize, with no
// FSM involved, since Execute()'s canonicalWorkflow loop already exercises
// the same call end-to-end (see TestOrchestrator_Execute_FullWorkflow).
func TestActions_Initialize_ThreadsReconcileResult(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(ctx context.Context, namespace, name string) ([]byte, error) {
			return []byte(testInitialCR), nil
		},
	}
	cl := &mockClusterLinkService{
		listConfigsFn: func(ctx context.Context, cfg clusterlink.Config) (map[string]string, error) {
			return map[string]string{"consumer.offset.sync.enable": "true"}, nil
		},
	}
	actions := NewMigrationActions(gw, cl)

	config := &MigrationConfig{MigrationId: "test-migration-1"}
	res := testReconcileResult()
	res.AwaitStopped = []string{"await-me"}

	err := actions.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, res)
	require.NoError(t, err)

	assert.Equal(t, res.Route, config.Route)
	assert.Equal(t, res.GatewayYAML, config.GatewayYAML)
	assert.Equal(t, res.FenceYAML, config.FenceYAML)
	assert.Equal(t, res.SwitchoverYAML, config.SwitchoverYAML)
	assert.Equal(t, res.PromoteTopics, config.Topics)
	assert.Equal(t, res.AwaitStopped, config.AwaitStopped)
	assert.Equal(t, res.Mode, config.Mode)
}

// TestActions_Initialize_RefusedPlanFailsWithReasons proves a refused
// reconcile plan is turned into a failed call, never silently accepted.
func TestActions_Initialize_RefusedPlanFailsWithReasons(t *testing.T) {
	actions := NewMigrationActions(&mockGatewayService{}, &mockClusterLinkService{})
	config := &MigrationConfig{MigrationId: "test-migration-1"}
	res := &migplan.Result{Refused: true, Reasons: []string{"topic t1.order has replication lag"}}

	err := actions.Initialize(context.Background(), config, clusterlink.BasicAuth{Username: "api-key", Password: "api-secret"}, res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "topic t1.order has replication lag")
	assert.Empty(t, config.Route, "a refused plan must not mutate config")
}

// Note: the former "PauseConsumerOffsetSync precondition tests (U2)" suite
// pinned Initialize's live-cluster-link precondition (refuse-if-not-enabled)
// and its ClusterLinkConfigs pre-disable snapshot capture. Both are removed —
// Initialize no longer calls ListConfigs at all, and PauseOffsetSync/
// restoreOffsetSync are now manifest+plan-driven idempotent applies that never
// read the live cluster link to decide anything (see offset_sync_bookend_test.go
// for their coverage).

// ===========================================================================
// CheckLags tests
// ===========================================================================

func TestWorkflow_CheckLags_ImmediatelyBelowThreshold(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 1000}, nil
		},
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 999}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics: []string{"topic-1", "topic-2"},
	}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
}

func TestWorkflow_CheckLags_NoTopics(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{}, nil
		},
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics: []string{},
	}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
}

func TestWorkflow_CheckLags_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Equal(t, "source and destination offset services are required", err.Error())
}

func TestWorkflow_CheckLags_ContextCancelled(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	// Return high lag so the loop does not exit early on threshold
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 10000}, nil
		},
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 0}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics: []string{"topic-1"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	err := wf.CheckLags(ctx, config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestWorkflow_CheckLags_DestinationAhead(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 200}, nil // ahead of source
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "negative lag (destination ahead) should be treated as 0 and pass threshold")
}

// ===========================================================================
// PromoteTopics tests
// ===========================================================================

func TestWorkflow_PromoteTopics_AllAtZeroLag(t *testing.T) {
	gw := &mockGatewayService{}

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
				}{
					MirrorTopicName: name,
					ErrorCode:       0,
				})
			}
			return resp, nil
		},
		// After promotion is accepted, the backend reports STOPPED.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: clusterlink.MirrorStatusStopped},
				{MirrorTopicName: "topic-2", MirrorStatus: clusterlink.MirrorStatusStopped},
			}, nil
		},
	}

	// Both source and dest return identical offsets (zero lag)
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 500, 1: 600}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"topic-1", "topic-2"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
	assert.True(t, promoted["topic-1"], "topic-1 should have been promoted")
	assert.True(t, promoted["topic-2"], "topic-2 should have been promoted")
}

func TestWorkflow_PromoteTopics_PartialPromotionError(t *testing.T) {
	gw := &mockGatewayService{}

	var callCount int64

	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			n := atomic.AddInt64(&callCount, 1)
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				entry := struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{
					MirrorTopicName: name,
				}
				// First call: topic-2 fails
				if n == 1 && name == "topic-2" {
					entry.ErrorCode = 1
					entry.ErrorMessage = "temporary error"
				}
				resp.Data = append(resp.Data, entry)
			}
			return resp, nil
		},
		// Once accepted, both topics report STOPPED.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: clusterlink.MirrorStatusStopped},
				{MirrorTopicName: "topic-2", MirrorStatus: clusterlink.MirrorStatusStopped},
			}, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"topic-1", "topic-2"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "retry should succeed")

	finalCallCount := atomic.LoadInt64(&callCount)
	assert.GreaterOrEqual(t, finalCallCount, int64(2), "expected at least 2 promote calls (initial + retry)")
}

// TestWorkflow_PromoteTopics_StuckPendingStoppedDoesNotSucceed reproduces the
// customer-reported false-positive: promote returns error_code 0 (accepted),
// but the mirror topic never leaves PENDING_STOPPED. PromoteTopics must NOT
// report success on the enqueue acknowledgement alone — it must wait for the
// terminal STOPPED status, so a topic stuck in PENDING_STOPPED keeps it polling
// until the caller cancels.
func TestWorkflow_PromoteTopics_StuckPendingStoppedDoesNotSucceed(t *testing.T) {
	gw := &mockGatewayService{}

	var promoteCalls int64
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			atomic.AddInt64(&promoteCalls, 1)
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: name, ErrorCode: 0})
			}
			return resp, nil
		},
		// Backend never finishes the async promotion: always PENDING_STOPPED.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: "PENDING_STOPPED"},
			}, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"topic-1"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := wf.PromoteTopics(ctx, config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err, "must not report success while topic is stuck in PENDING_STOPPED")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestWorkflow_PromoteTopics_WaitsForStoppedStatus verifies the happy path of
// the async promotion: the topic is PENDING_STOPPED immediately after promote
// and only later transitions to STOPPED. PromoteTopics must poll
// ListMirrorTopics and only return once the terminal STOPPED status is observed.
func TestWorkflow_PromoteTopics_WaitsForStoppedStatus(t *testing.T) {
	gw := &mockGatewayService{}

	var listCalls int64
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{MirrorTopicName: name, ErrorCode: 0})
			}
			return resp, nil
		},
		// PENDING_STOPPED for the first two polls, then STOPPED.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			n := atomic.AddInt64(&listCalls, 1)
			status := "PENDING_STOPPED"
			if n >= 3 {
				status = "STOPPED"
			}
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: status},
			}, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"topic-1"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, atomic.LoadInt64(&listCalls), int64(3),
		"expected PromoteTopics to poll mirror status until STOPPED was observed")
}

// Resume-from-PENDING_STOPPED: a topic reconcile classified AwaitStopped is
// already mid-promotion, so the promote stage must WAIT for it to reach STOPPED
// and never re-issue a promote on it (a re-promote of an already-promoting
// mirror is rejected by CC → the loop would retry 3x then fail). Only the
// genuinely migratable topic gets a promote request.
func TestWorkflow_PromoteTopics_AwaitStoppedTopicsAreWaitedNotRepromoted(t *testing.T) {
	gw := &mockGatewayService{}

	var promoted []string // PromoteTopics is synchronous — no locking needed
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
				}{MirrorTopicName: name, ErrorCode: 0})
			}
			return resp, nil
		},
		// Both mirrors PENDING_STOPPED for the first two polls, then STOPPED.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			status := "PENDING_STOPPED"
			if atomic.AddInt64(&listCalls, 1) >= 3 {
				status = "STOPPED"
			}
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "await-me", MirrorStatus: status},
				{MirrorTopicName: "migrate-me", MirrorStatus: status},
			}, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) { return map[int32]int64{0: 100}, nil },
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"await-me", "migrate-me"},
		AwaitStopped:        []string{"await-me"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
	assert.NotContains(t, promoted, "await-me",
		"an AwaitStopped topic (already promoting on resume) must be waited on, never re-promoted")
	assert.Contains(t, promoted, "migrate-me",
		"the genuinely migratable topic must still be promoted")
}

// TestWorkflow_PromoteTopics_BatchSizeProcessesSequentially verifies that when
// promoteBatchSize is set, PromoteTopics (1) never submits more than the cap in
// a single promote call, and (2) does not start the next batch until every
// topic in the current batch has reached STOPPED — i.e. synchronous batches.
func TestWorkflow_PromoteTopics_BatchSizeProcessesSequentially(t *testing.T) {
	gw := &mockGatewayService{}

	const batchSize = 10
	topics := make([]string, 25)
	for i := range topics {
		topics[i] = fmt.Sprintf("topic-%02d", i)
	}

	var mu sync.Mutex
	var promoteCallSizes []int
	inFlight := make(map[string]bool)  // promoted but not yet confirmed STOPPED
	pollsSince := make(map[string]int) // polls observed since a topic was promoted

	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			mu.Lock()
			// A new batch must not begin while the previous batch is still
			// draining to STOPPED.
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
				}{MirrorTopicName: name, ErrorCode: 0})
			}
			mu.Unlock()
			return resp, nil
		},
		// Each promoted topic reports PENDING_STOPPED on its first poll and
		// STOPPED thereafter, so the workflow must poll at least twice per batch.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			mu.Lock()
			defer mu.Unlock()
			out := make([]clusterlink.MirrorTopic, len(topics))
			for i, name := range topics {
				status := clusterlink.MirrorStatusActive
				if _, promoted := pollsSince[name]; promoted {
					pollsSince[name]++
					if pollsSince[name] >= 2 {
						status = clusterlink.MirrorStatusStopped
						delete(inFlight, name)
					} else {
						// Transient wire value the backend reports before STOPPED;
						// the workflow treats any non-STOPPED status as "not done".
						status = "PENDING_STOPPED"
					}
				}
				out[i] = clusterlink.MirrorTopic{MirrorTopicName: name, MirrorStatus: status}
			}
			return out, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	wf.promoteBatchSize = batchSize
	config := &MigrationConfig{
		Topics:              topics,
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []int{10, 10, 5}, promoteCallSizes,
		"expected 25 topics promoted in sequential batches of at most 10")
}

func TestWorkflow_PromoteTopics_MaxRetriesExceeded(t *testing.T) {
	gw := &mockGatewayService{}

	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			resp := &clusterlink.PromoteMirrorTopicsResponse{}
			for _, name := range topicNames {
				resp.Data = append(resp.Data, struct {
					MirrorTopicName string `json:"mirror_topic_name"`
					ErrorMessage    string `json:"error_message,omitempty"`
					ErrorCode       int    `json:"error_code,omitempty"`
				}{
					MirrorTopicName: name,
					ErrorCode:       1,
					ErrorMessage:    "persistent error",
				})
			}
			return resp, nil
		},
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 200}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:              []string{"topic-1"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Equal(t, "topic topic-1 failed promotion after 3 attempts: persistent error", err.Error())
}

func TestWorkflow_PromoteTopics_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Equal(t, "source and destination offset services are required", err.Error())
}

// TestWorkflow_PromoteTopics_NoTopicsIsNoop asserts the plan-driven no-op
// guard: when reconcile emitted no migratable topics, PromoteTopics returns
// nil immediately, before even the nil-offset-service check, and never
// touches the cluster link. Offset services are deliberately left nil (via
// NewMigrationActions) — if the guard were missing or misplaced after the
// nil check, this would fail with "source and destination offset services
// are required" instead of nil.
func TestWorkflow_PromoteTopics_NoTopicsIsNoop(t *testing.T) {
	var promoteCalls int64
	cl := &mockClusterLinkService{
		promoteMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config, _ []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
			atomic.AddInt64(&promoteCalls, 1)
			return nil, nil
		},
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			atomic.AddInt64(&promoteCalls, 1)
			return nil, nil
		},
	}
	wf := NewMigrationActions(&mockGatewayService{}, cl)
	config := &MigrationConfig{Topics: nil}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), atomic.LoadInt64(&promoteCalls), "no topics to promote: cluster link must not be touched")
}

// ===========================================================================
// FenceGateway / SwitchGateway tests
// ===========================================================================

// TestWorkflow_FenceGateway_AppliesFenceInjectedIntoInitialCR is the behavioural
// heart of the inline-fence change: FenceGateway no longer applies a
// snapshotted fenced CR file — it derives a RoutePatch at cutover from
// config.FenceYAML for config.Route. The patch must carry that fence.
func TestWorkflow_FenceGateway_AppliesFenceInjectedIntoInitialCR(t *testing.T) {
	var gotRP gateway.RoutePatch
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, configID string) (string, error) {
			gotRP = rp
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	require.NoError(t, wf.FenceGateway(context.Background(), config))

	assert.Equal(t, "migration-route", gotRP.RouteName)
	assert.Equal(t, "fence", gotRP.Field)
	fence, ok := gotRP.Value.(map[string]any)
	require.True(t, ok, "fence patch value must be a map")
	assert.Equal(t, "ALL", fence["scope"])
	assert.Equal(t, "BROKER_NOT_AVAILABLE", fence["errorCode"])
}

func TestWorkflow_FenceGateway_HappyPath(t *testing.T) {
	var callOrder []string
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			callOrder = append(callOrder, "apply")
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _ time.Duration, _ time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			callOrder = append(callOrder, "wait")
			if onProgress != nil {
				onProgress(gateway.GatewayReadinessProgress{InitialPodCount: 3, PodsReady: 3, Elapsed: 2 * time.Second, RolloutDetected: true, Ready: true})
			}
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, []string{"apply", "wait"}, callOrder, "apply must precede wait")
}

func TestWorkflow_FenceGateway_DetectionDisabled_UsesReadyWaitNotUIDDiffing(t *testing.T) {
	var unwantedCall string
	waitReadyCalled := false
	acceptedCalled := false
	gw := &mockGatewayService{
		getGatewayPodUIDsFn: func(_ context.Context, _, _ string) (map[k8stypes.UID]struct{}, error) {
			unwantedCall = "GetGatewayPodUIDs"
			return nil, nil
		},
		waitForGatewayPodsFn: func(_ context.Context, _, _ string, _ map[k8stypes.UID]struct{}, _ int64, _, _ time.Duration, _ func(gateway.PodRolloutProgress)) error {
			unwantedCall = "WaitForGatewayPods"
			return nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			acceptedCalled = true
			return nil
		},
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitReadyCalled = true
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	// DetectUnroutedProducersDuration unset (0) → detection disabled: the fence
	// keeps the lightweight readiness-only wait and never touches pod UIDs. The
	// operator-acceptance wait, by contrast, now runs on every path — the
	// Deployment-only wait cannot tell a no-op apply from a rejected one.
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Empty(t, unwantedCall, "with detection disabled, FenceGateway must not use UID-diffing methods, but called: %s", unwantedCall)
	assert.True(t, waitReadyCalled, "with detection disabled, FenceGateway must wait via WaitForGatewayReady")
	assert.True(t, acceptedCalled, "the acceptance wait must run even with detection disabled")
}

// TestWorkflow_FenceGateway_OperatorRejection_DoesNotProceed asserts a rejected
// fence CR aborts before the readiness wait. Previously the fence-without-
// detection path relied solely on the Deployment wait, which would report "No
// pod restart required" and let the migration continue to promote topics with
// the gateway never actually fenced.
func TestWorkflow_FenceGateway_OperatorRejection_DoesNotProceed(t *testing.T) {
	waitReadyCalled := false
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			return rejectionError("gw-1")
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitReadyCalled = true
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.FenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.False(t, waitReadyCalled, "a rejected fence must not fall through to the Deployment readiness wait")
	assert.Contains(t, err.Error(), "secretRef kcp-perf-plain-jaas not found")
}

func TestWorkflow_FenceGateway_DetectionEnabled_WaitsForOldPodsGone(t *testing.T) {
	var callOrder []string
	waitReadyCalled := false
	oldUIDs := map[k8stypes.UID]struct{}{"old-pod": {}}
	var passedUIDs map[k8stypes.UID]struct{}
	gw := &mockGatewayService{
		getGatewayPodUIDsFn: func(_ context.Context, _, _ string) (map[k8stypes.UID]struct{}, error) {
			callOrder = append(callOrder, "getUIDs")
			return oldUIDs, nil
		},
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			callOrder = append(callOrder, "apply")
			return configID, nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			callOrder = append(callOrder, "reconcile")
			return nil
		},
		waitForGatewayPodsFn: func(_ context.Context, _, _ string, initialPodUIDs map[k8stypes.UID]struct{}, _ int64, _, _ time.Duration, onProgress func(gateway.PodRolloutProgress)) error {
			callOrder = append(callOrder, "waitPods")
			passedUIDs = initialPodUIDs
			if onProgress != nil {
				onProgress(gateway.PodRolloutProgress{InitialPodCount: 1, NewPodsReady: 1, OldPodsRemaining: 0, RolloutDetected: true})
			}
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitReadyCalled = true
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	// Detection enabled: capture the pre-fence pod set, wait for the operator to
	// observe the fenced CR (so "no rollout detected" downstream is trustworthy),
	// then wait for those old pods to actually terminate so no unfenced pod is
	// still serving traffic when detection's first offset snapshot is taken.
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}, DetectUnroutedProducersDuration: 10 * time.Second}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, []string{"getUIDs", "apply", "reconcile", "waitPods"}, callOrder,
		"with detection enabled, FenceGateway must capture pod UIDs before apply, wait for operator reconcile, then wait for pod rollout")
	assert.False(t, waitReadyCalled, "with detection enabled, FenceGateway must not use the readiness-only wait")
	assert.Equal(t, oldUIDs, passedUIDs, "the pre-apply pod UIDs must be passed to WaitForGatewayPods")
}

func TestWorkflow_FenceGateway_ApplyFailsReturnsWrappedError(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, _ string) (string, error) {
			return "", fmt.Errorf("k8s 403")
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.FenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to apply fenced gateway CR")
	assert.Contains(t, err.Error(), "k8s 403")
}

func TestWorkflow_FenceGateway_WaitTimeoutPropagatesDeadlineExceeded(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return fmt.Errorf("rollout-timeout exceeded: %w", context.DeadlineExceeded)
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	wf.SetRolloutTimeout(100 * time.Millisecond)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.FenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "DeadlineExceeded must propagate: %v", err)
}

func TestWorkflow_FenceGateway_WaitContextCancelledPropagates(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := wf.FenceGateway(ctx, config)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestWorkflow_FenceGateway_PassesRolloutTimeoutToService(t *testing.T) {
	var observedTimeout time.Duration
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, timeout time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			observedTimeout = timeout
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	wf.SetRolloutTimeout(15 * time.Minute)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, observedTimeout)
}

func TestWorkflow_FenceGateway_DefaultRolloutTimeoutIsZero(t *testing.T) {
	var observedTimeout time.Duration
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, timeout time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			observedTimeout = timeout
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), observedTimeout, "default rolloutTimeout should be 0 (no deadline)")
}

// TestWorkflow_FenceGateway_NoFenceYAMLIsNoop asserts the plan-driven no-op
// guard: when reconcile emitted no fence artifact, FenceGateway returns nil
// immediately and never touches the gateway (no capability resolution, no
// patch, no wait). config carries no GatewayYAML/SwitchoverYAML either —
// proving the guard fires before anything that would need them. Fence's
// no-op signal is FenceYAML, deliberately NOT config.Topics — see
// TestWorkflow_FenceGateway_NonEmptyFenceYAMLButNoTopics_StillFences below
// for why a shared Topics-based guard would be wrong.
func TestWorkflow_FenceGateway_NoFenceYAMLIsNoop(t *testing.T) {
	var applyCalls int64
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			atomic.AddInt64(&applyCalls, 1)
			return configID, nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{Topics: []string{"topic-a"}, FenceYAML: ""}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, int64(0), atomic.LoadInt64(&applyCalls), "no fence artifact in plan: gateway must not be touched")
}

// TestWorkflow_FenceGateway_NonEmptyFenceYAMLButNoTopics_StillFences pins the
// promoted-not-switched shape at the unit level: reconcile can return an empty
// promote set (config.Topics == nil — every topic already promoted to
// STOPPED) while a fence artifact is still present, because fence is a
// whole-route action independent of per-topic promote status. Gating on
// Topics here would silently skip a still-owed fence (and, on a real
// cluster, the switch right after it) — this is the production bug this
// guard rework fixes.
func TestWorkflow_FenceGateway_NonEmptyFenceYAMLButNoTopics_StillFences(t *testing.T) {
	var applyCalls int64
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			atomic.AddInt64(&applyCalls, 1)
			return configID, nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{
		K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR,
		Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML,
		Topics: nil,
	}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&applyCalls),
		"a non-empty fence artifact must still be applied even when there is nothing left to promote")
}

func TestWorkflow_SwitchGateway_HappyPath(t *testing.T) {
	var callOrder []string
	var gotRP gateway.RoutePatch
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, configID string) (string, error) {
			gotRP = rp
			callOrder = append(callOrder, "apply")
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			callOrder = append(callOrder, "wait")
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.SwitchGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, []string{"apply", "wait"}, callOrder, "apply (derived switch route patch) must precede wait")
	assert.Equal(t, "migration-route", gotRP.RouteName)
	assert.Equal(t, "", gotRP.Field, "switch must whole-route replace so the fence is dropped, not overwrite a single field")
	route, ok := gotRP.Value.(map[string]any)
	require.True(t, ok, "switch patch value must be the route object")
	domain, ok := route["streamingDomain"].(map[string]any)
	require.True(t, ok, "the switched route must carry a streamingDomain")
	assert.Equal(t, "confluent-cloud", domain["name"], "the patch must carry the target streaming domain")
}

// TestDeriveSwitchRoutePatch_WholeRouteReplaceDropsFence proves the switch is a
// whole-route replace onto the captured (unfenced) route with streamingDomain
// flipped to the target — not a single-field write. A field-only streamingDomain
// patch would flip the domain but leave the fence FenceGateway added earlier in
// place, completing a migration whose gateway is still fenced (the regression the
// e2e "Gateway CR must not have fence config" assertions caught). Mirrors
// deriveUnfenceRoutePatch, which replaces the whole route for the same reason.
func TestDeriveSwitchRoutePatch_WholeRouteReplaceDropsFence(t *testing.T) {
	// A captured route carrying the source domain, so the test proves the domain
	// is overwritten as well as that no fence survives.
	const capturedCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gw-1
spec:
  routes:
    - name: migration-route
      endpoint: gateway:9595
      streamingDomain:
        name: source-kafka-cluster
`
	config := &MigrationConfig{GatewayYAML: capturedCR, Route: "migration-route", SwitchoverYAML: testSwitchoverYAML}

	rp, err := deriveSwitchRoutePatch(config)
	require.NoError(t, err)

	assert.Equal(t, "migration-route", rp.RouteName)
	assert.Equal(t, "", rp.Field, "switch must whole-route replace so the fence key is dropped")
	route, ok := rp.Value.(map[string]any)
	require.True(t, ok, "switch patch value must be the route object")
	_, hasFence := route["fence"]
	assert.False(t, hasFence, "the switched route must not carry a fence key")
	domain, ok := route["streamingDomain"].(map[string]any)
	require.True(t, ok, "the switched route must carry a streamingDomain")
	assert.Equal(t, "confluent-cloud", domain["name"], "the source domain must be overwritten with the target")
}

// On resume the captured CR is already fenced (the prior interrupted run fenced
// it and reconcile re-pulls it live), so the switch must EXPLICITLY drop the
// fence key — it cannot rely on the captured route being unfenced.
func TestDeriveSwitchRoutePatch_DropsFenceFromAlreadyFencedCapture(t *testing.T) {
	const fencedCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gw-1
spec:
  routes:
    - name: migration-route
      endpoint: gateway:9595
      fence:
        topics:
          - topic-a
          - topic-b
      streamingDomain:
        name: source-kafka-cluster
`
	config := &MigrationConfig{GatewayYAML: fencedCR, Route: "migration-route", SwitchoverYAML: testSwitchoverYAML}

	rp, err := deriveSwitchRoutePatch(config)
	require.NoError(t, err)

	route, ok := rp.Value.(map[string]any)
	require.True(t, ok, "switch patch value must be the route object")
	_, hasFence := route["fence"]
	assert.False(t, hasFence, "switch must drop the fence even when the captured route was already fenced (resume)")
	domain, ok := route["streamingDomain"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "confluent-cloud", domain["name"], "the source domain must still be overwritten with the target")
}

func TestWorkflow_SwitchGateway_WaitErrorIsWrapped(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return fmt.Errorf("kube unreachable")
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.SwitchGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway readiness")
	assert.Contains(t, err.Error(), "kube unreachable")
}

// ===========================================================================
// Operator-acceptance guard — the switchover-verification gap
// ===========================================================================

// rejectionError builds the error the gateway service returns when the CFK
// operator refuses a spec, matching the rejection observed on 2026-07-27 while
// setting up the live-cluster e2e test infrastructure.
func rejectionError(gatewayName string) *gateway.GatewayRejectedError {
	return &gateway.GatewayRejectedError{
		Gateway:            gatewayName,
		ConditionType:      "platform.confluent.io/cluster-ready",
		Reason:             "ApplyFailed",
		Message:            "secretRef kcp-perf-plain-jaas not found",
		Generation:         4,
		ObservedGeneration: 3,
	}
}

// TestWorkflow_SwitchGateway_OperatorRejection_FailsWithOperatorMessage is the
// regression test for the reported bug: kcp printed "No pod restart required"
// then "✅ Migration complete!" while the gateway was still fenced, because the
// Deployment-based readiness wait cannot see that the operator refused the
// switchover CR. SwitchGateway must now fail, and fail with the operator's own
// diagnosis.
func TestWorkflow_SwitchGateway_OperatorRejection_FailsWithOperatorMessage(t *testing.T) {
	waitReadyCalled := false
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			return rejectionError("gw-1")
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitReadyCalled = true
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.SwitchGateway(context.Background(), config)
	require.Error(t, err, "a switchover the operator rejected must not be reported as a success")
	assert.False(t, waitReadyCalled, "must abort before the Deployment wait that would report 'No pod restart required'")

	var rejected *gateway.GatewayRejectedError
	require.ErrorAs(t, err, &rejected, "the typed rejection must survive to the caller")
	assert.Contains(t, err.Error(), "secretRef kcp-perf-plain-jaas not found")
	assert.Contains(t, err.Error(), "ApplyFailed")
}

// TestWorkflow_SwitchGateway_WaitsForAcceptanceBeforeReadiness pins the
// ordering: acceptance must be confirmed between the apply and the readiness
// wait, otherwise the readiness wait observes the pre-switchover Deployment.
func TestWorkflow_SwitchGateway_WaitsForAcceptanceBeforeReadiness(t *testing.T) {
	var callOrder []string
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			callOrder = append(callOrder, "apply")
			return configID, nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			callOrder = append(callOrder, "accepted")
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			callOrder = append(callOrder, "ready")
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	require.NoError(t, wf.SwitchGateway(context.Background(), config))
	assert.Equal(t, []string{"apply", "accepted", "ready"}, callOrder)
}

// TestWorkflow_SwitchGateway_NonRejectionWaitError_IsWrapped keeps transport
// failures distinguishable from operator rejections.
func TestWorkflow_SwitchGateway_NonRejectionWaitError_IsWrapped(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			return fmt.Errorf("kube unreachable")
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	err := wf.SwitchGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway reconcile during switchover")
	assert.Contains(t, err.Error(), "kube unreachable")
	var rejected *gateway.GatewayRejectedError
	assert.NotErrorAs(t, err, &rejected, "a transport failure is not an operator rejection")
}

// TestWorkflow_SwitchGateway_PassesRolloutTimeoutToAcceptanceWait ensures
// --rollout-timeout bounds the acceptance wait too, not just the readiness wait.
func TestWorkflow_SwitchGateway_PassesRolloutTimeoutToAcceptanceWait(t *testing.T) {
	var observedTimeout time.Duration
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _ time.Duration, timeout time.Duration) error {
			observedTimeout = timeout
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	wf.SetRolloutTimeout(15 * time.Minute)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML, Topics: []string{"topic-a", "topic-b", "topic-c"}}

	require.NoError(t, wf.SwitchGateway(context.Background(), config))
	assert.Equal(t, 15*time.Minute, observedTimeout)
}

// TestWorkflow_SwitchGateway_NoSwitchoverYAMLIsNoop asserts the plan-driven
// no-op guard: when reconcile emitted no switchover artifact, SwitchGateway
// returns nil immediately and never touches the gateway. Switch's no-op
// signal is SwitchoverYAML, deliberately NOT config.Topics — see
// TestWorkflow_SwitchGateway_NonEmptySwitchoverYAMLButNoTopics_StillSwitches
// below for why a shared Topics-based guard would be wrong.
func TestWorkflow_SwitchGateway_NoSwitchoverYAMLIsNoop(t *testing.T) {
	var applyCalls int64
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			atomic.AddInt64(&applyCalls, 1)
			return configID, nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{Topics: []string{"topic-a"}, SwitchoverYAML: ""}

	err := wf.SwitchGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, int64(0), atomic.LoadInt64(&applyCalls), "no switchover artifact in plan: gateway must not be touched")
}

// TestWorkflow_SwitchGateway_NonEmptySwitchoverYAMLButNoTopics_StillSwitches
// pins the promoted-not-switched shape at the unit level: reconcile can
// return an empty promote set (config.Topics == nil — every topic already
// promoted to STOPPED) while a switchover artifact is still present and owed,
// because switch is a whole-route action independent of per-topic promote
// status.
// Gating on Topics here is the exact production bug this guard rework
// fixes: a kill right after the last topic's promote completes would
// otherwise report the migration complete without ever switching the
// gateway.
func TestWorkflow_SwitchGateway_NonEmptySwitchoverYAMLButNoTopics_StillSwitches(t *testing.T) {
	var applyCalls int64
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			atomic.AddInt64(&applyCalls, 1)
			return configID, nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{
		K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR,
		Route: "migration-route", Mode: "static", FenceYAML: testFenceYAML, SwitchoverYAML: testSwitchoverYAML,
		Topics: nil,
	}

	err := wf.SwitchGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&applyCalls),
		"a non-empty switchover artifact must still be applied even when there is nothing left to promote")
}

// TestWorkflow_UnfenceGateway_OperatorRejection_Fails covers the rollback path:
// reporting restored traffic while the gateway is still fenced is the worst
// place to be blind to a rejected apply.
func TestWorkflow_UnfenceGateway_OperatorRejection_Fails(t *testing.T) {
	waitReadyCalled := false
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			return rejectionError("gw-1")
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitReadyCalled = true
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route"}

	err := wf.unfenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.False(t, waitReadyCalled, "a rejected unfence must not report traffic restored")
	assert.Contains(t, err.Error(), "secretRef kcp-perf-plain-jaas not found")
}

// TestWorkflow_UnfenceGateway_WaitsForAcceptanceBeforeReadiness pins the
// ordering on the rollback path.
func TestWorkflow_UnfenceGateway_WaitsForAcceptanceBeforeReadiness(t *testing.T) {
	var callOrder []string
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			callOrder = append(callOrder, "apply")
			return configID, nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _, _ time.Duration) error {
			callOrder = append(callOrder, "accepted")
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			callOrder = append(callOrder, "ready")
			return nil
		},
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", GatewayYAML: testInitialCR, Route: "migration-route"}

	require.NoError(t, wf.unfenceGateway(context.Background(), config))
	assert.Equal(t, []string{"apply", "accepted", "ready"}, callOrder)
}

// ===========================================================================
// VerifyFence / DetectUnroutedProducers tests
// ===========================================================================

func TestWorkflow_VerifyFence_StableOffsets(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	// Source offsets are stable (same value on every call)
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 500, 1: 600}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	config := &MigrationConfig{
		Topics:                          []string{"topic-1", "topic-2"},
		DetectUnroutedProducersDuration: time.Millisecond,
	}

	err := wf.VerifyFence(context.Background(), config)
	require.NoError(t, err)
}

func TestWorkflow_VerifyFence_IncreasingOffsets_ReturnsError(t *testing.T) {
	// VerifyFence only detects — it must NOT unfence the gateway itself.
	// Restoring traffic is the orchestrator's job on the abort_fence rollback
	// (see TestOrchestrator_Execute_UnroutedProducers_AbortsFenceAndRollsBack).
	var applyCalled bool
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			applyCalled = true
			return configID, nil
		},
	}
	cl := &mockClusterLinkService{}

	// Simulates an unrouted producer: offsets keep increasing on every call.
	var callCount int64
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&callCount, 1)
			if topic == "topic-1" {
				return map[int32]int64{0: 100 + n*10, 1: 200}, nil
			}
			return map[int32]int64{0: 300}, nil
		},
	}

	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100, 1: 200}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		DetectUnroutedProducersDuration: time.Millisecond,
		GatewayYAML:                     "apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gw\n  namespace: ns\n  managedFields: []\n  resourceVersion: \"123\"\n",
		InitialCrName:                   "my-gw",
		K8sNamespace:                    "ns",
	}

	err := wf.VerifyFence(context.Background(), config)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)
	assert.Contains(t, err.Error(), "topic-1 partition 0")
	assert.False(t, applyCalled, "VerifyFence must not unfence the gateway; that is the orchestrator's responsibility")
}

func TestWorkflow_VerifyFence_NewPartitionBetweenSnapshots_IsViolation(t *testing.T) {
	// A partition that appears only in the second snapshot (created during
	// the monitoring window, or missing from the first fetch's metadata)
	// starts at offset 0 — any data on it was written after fencing and must
	// be flagged, not silently skipped.
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	var call int64
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			if atomic.AddInt64(&call, 1) == 1 {
				return map[int32]int64{0: 100}, nil // partition 1 not yet visible
			}
			return map[int32]int64{0: 100, 1: 50}, nil // partition 0 stable, 1 has data
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, sourceOffset)
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		DetectUnroutedProducersDuration: time.Millisecond,
	}

	err := wf.VerifyFence(context.Background(), config)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)
	assert.Contains(t, err.Error(), "topic-1 partition 1: offset 0 → 50")
}

func TestWorkflow_VerifyFence_Disabled_SkipsOffsetChecks(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	// Detection is disabled, so the offset providers must never be consulted.
	var getCalls int64
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			atomic.AddInt64(&getCalls, 1)
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		DetectUnroutedProducersDuration: 0, // check disabled
	}

	err := wf.VerifyFence(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, int64(0), atomic.LoadInt64(&getCalls),
		"offset providers should not be consulted when detection is disabled")
}

func TestWorkflow_VerifyFence_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl) // no offset providers
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		DetectUnroutedProducersDuration: time.Millisecond,
	}

	err := wf.VerifyFence(context.Background(), config)
	require.Error(t, err)
	assert.Equal(t, "source offset service is required for unrouted producer detection", err.Error())
}

func TestWorkflow_PromoteTopics_IgnoresDetectionConfig(t *testing.T) {
	// Unrouted-producer detection belongs to the verify_fence step; PromoteTopics
	// must not re-run it. If it did, this test would block on the 30s detection
	// window and trip the 2s context deadline.
	gw := &mockGatewayService{}

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
				}{
					MirrorTopicName: name,
					ErrorCode:       0,
				})
			}
			return resp, nil
		},
		// Accepted promotions are confirmed STOPPED via ListMirrorTopics
		// before PromoteTopics returns.
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: clusterlink.MirrorStatusStopped},
			}, nil
		},
	}

	// Zero lag so promotion completes immediately
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 500}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		ClusterRestEndpoint:             "https://cluster",
		ClusterId:                       "lkc-123",
		ClusterLinkName:                 "link-1",
		DetectUnroutedProducersDuration: 30 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := wf.PromoteTopics(ctx, config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "PromoteTopics should not run unrouted-producer detection")
	assert.True(t, promoted["topic-1"])
}

// TestWorkflow_UnfenceGateway_PatchesRouteVerbatim proves unfenceGateway
// restores config.Route to exactly the state captured in config.GatewayYAML —
// a whole-route replace (Field == "") straight off the migplan-captured CR,
// with no re-cleaning of its own (migplan already cleaned server-managed
// metadata once, centrally — see migplan/gatewayfile.go's cleanGatewayDoc).
func TestWorkflow_UnfenceGateway_PatchesRouteVerbatim(t *testing.T) {
	var gotRP gateway.RoutePatch
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp gateway.RoutePatch, configID string) (string, error) {
			gotRP = rp
			return configID, nil
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	const cleanedGatewayYAML = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: my-gw
  namespace: confluent
spec:
  routes:
    - name: migration-route
      endpoint: gateway:9595
`
	config := &MigrationConfig{
		InitialCrName: "my-gw",
		K8sNamespace:  "confluent",
		GatewayYAML:   cleanedGatewayYAML,
		Route:         "migration-route",
	}

	err := wf.unfenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, "migration-route", gotRP.RouteName)
	assert.Equal(t, "", gotRP.Field, "unfence must whole-route replace, not set a single field")
	route, ok := gotRP.Value.(map[string]any)
	require.True(t, ok, "unfence patch value must be the route object")
	assert.Equal(t, "migration-route", route["name"])
	assert.Equal(t, "gateway:9595", route["endpoint"])
}

func TestWorkflow_UnfenceGateway_WaitsForGatewayReadiness(t *testing.T) {
	var applyCalled, waitCalled bool
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			applyCalled = true
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, namespace, name string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitCalled = true
			assert.True(t, applyCalled, "readiness wait must happen after the CR is applied")
			assert.Equal(t, "confluent", namespace)
			assert.Equal(t, "my-gw", name)
			return nil
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		InitialCrName: "my-gw",
		K8sNamespace:  "confluent",
		GatewayYAML:   testInitialCR,
		Route:         "migration-route",
	}

	err := wf.unfenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.True(t, waitCalled, "unfenceGateway should wait for gateway readiness after applying the initial CR")
}

func TestWorkflow_UnfenceGateway_ReadinessFailure_ReturnsError(t *testing.T) {
	gw := &mockGatewayService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ gateway.RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ int64, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return fmt.Errorf("gateway pods did not converge")
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewMigrationActions(gw, cl)
	config := &MigrationConfig{
		InitialCrName: "my-gw",
		K8sNamespace:  "confluent",
		GatewayYAML:   testInitialCR,
		Route:         "migration-route",
	}

	err := wf.unfenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway readiness during unfence")
	assert.Contains(t, err.Error(), "gateway pods did not converge")
}

func TestWorkflow_DetectUnroutedProducers_ContextCancelled(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}
	destOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	err := wf.detectUnroutedProducers(ctx, []string{"topic-1"}, 5*time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ===========================================================================
// Helper tests
// ===========================================================================

func TestFormatLag64(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{21655, "21,655"},
		{1000000, "1,000,000"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d", tc.input), func(t *testing.T) {
			got := formatLag64(tc.input)
			assert.Equal(t, tc.expected, got, "formatLag64(%d)", tc.input)
		})
	}
}

// ===========================================================================
// Sweep-failure tolerance tests (maxConsecutiveSweepFailures)
// ===========================================================================

// zeroLagBatch builds a GetMany-shaped result with the same offset for every
// topic, so source and destination compare at zero lag.
func zeroLagBatch(topics []string, off int64) map[string]map[int32]int64 {
	out := make(map[string]map[int32]int64, len(topics))
	for _, topic := range topics {
		out[topic] = map[int32]int64{0: off}
	}
	return out
}

func TestWorkflow_CheckLags_ToleratesTransientSweepFailures(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

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

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.lagPollInterval = time.Millisecond
	config := &MigrationConfig{Topics: []string{"topic-1"}}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "two transient sweep failures must be ridden out")
	assert.GreaterOrEqual(t, calls.Load(), int32(3), "expected the sweep to be retried on later ticks")
}

func TestWorkflow_CheckLags_AbortsAfterMaxConsecutiveSweepFailures(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

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

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.lagPollInterval = time.Millisecond
	config := &MigrationConfig{Topics: []string{"topic-1"}}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d consecutive", maxConsecutiveSweepFailures))
	assert.Contains(t, err.Error(), "broker unreachable", "the underlying cause must be preserved")
	assert.Equal(t, int32(maxConsecutiveSweepFailures), calls.Load(),
		"the sweep must not be attempted again after the abort threshold")
}

func TestWorkflow_CheckLags_SweepFailureCounterResetsOnSuccess(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

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

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.lagPollInterval = time.Millisecond
	config := &MigrationConfig{Topics: []string{"topic-1"}}

	err := wf.CheckLags(context.Background(), config, 10, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "four non-consecutive failures must not abort")
	assert.Equal(t, int32(6), calls.Load())
}

func TestWorkflow_PromoteTopics_ToleratesTransientSweepFailures(t *testing.T) {
	gw := &mockGatewayService{}

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
				}{
					MirrorTopicName: name,
					ErrorCode:       0,
				})
			}
			return resp, nil
		},
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-1", MirrorStatus: clusterlink.MirrorStatusStopped},
			}, nil
		},
	}

	// The source sweep fails twice, then reports zero lag so promotion runs.
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

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{Topics: []string{"topic-1"}}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.NoError(t, err, "two transient sweep failures must be ridden out")
	assert.True(t, promoted["topic-1"], "topic must still be promoted after tolerated failures")
}

func TestWorkflow_PromoteTopics_AbortsAfterMaxConsecutiveSweepFailures(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

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

	wf := NewMigrationActionsWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{Topics: []string{"topic-1"}}

	err := wf.PromoteTopics(context.Background(), config, clusterlink.BasicAuth{Username: "key", Password: "secret"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d consecutive", maxConsecutiveSweepFailures))
	assert.Contains(t, err.Error(), "broker unreachable", "the underlying cause must be preserved")
	assert.Equal(t, int32(maxConsecutiveSweepFailures), calls.Load(),
		"the sweep must not be attempted again after the abort threshold")
}
