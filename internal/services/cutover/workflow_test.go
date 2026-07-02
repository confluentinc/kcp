package cutover

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// ===========================================================================
// Initialize tests
// ===========================================================================

func TestWorkflow_Initialize_Success(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("initial-yaml"), nil
		},
		validateGatewayCRsFn: func(_, _, _ []byte) error {
			return nil
		},
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"},
				{MirrorTopicName: "topic-b", MirrorStatus: "ACTIVE"},
				{MirrorTopicName: "topic-c", MirrorStatus: "ACTIVE"},
			}, nil
		},
		listConfigsFn: func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
			return map[string]string{"bootstrap.servers": "broker:9092"}, nil
		},
	}

	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{
		CutoverId:           "test-1",
		K8sNamespace:        "ns",
		InitialCrName:       "my-gw",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
		FencedCrYAML:        []byte("fenced"),
		SwitchoverCrYAML:    []byte("switchover"),
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.NoError(t, err)

	assert.Equal(t, "initial-yaml", string(config.InitialCrYAML))
	assert.Len(t, config.ClusterLinkTopics, 3)
	assert.Equal(t, "broker:9092", config.ClusterLinkConfigs["bootstrap.servers"])
}

func TestWorkflow_Initialize_GatewayFetchError(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return nil, fmt.Errorf("k8s unreachable")
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{
		K8sNamespace:  "ns",
		InitialCrName: "my-gw",
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.Error(t, err)
	assert.Equal(t, "failed to get initial CR YAML: k8s unreachable", err.Error())
}

func TestWorkflow_Initialize_InactiveMirrorTopics(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("yaml"), nil
		},
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"},
				{MirrorTopicName: "topic-b", MirrorStatus: "PAUSED"},
			}, nil
		},
	}

	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{
		K8sNamespace:        "ns",
		InitialCrName:       "my-gw",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
		FencedCrYAML:        []byte("fenced"),
		SwitchoverCrYAML:    []byte("switchover"),
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.Error(t, err)
	assert.Equal(t, "1 mirror topics are not active: topic-b (status: PAUSED)", err.Error())
}

func TestWorkflow_Initialize_TopicValidationError(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("yaml"), nil
		},
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"},
			}, nil
		},
		validateTopicsFn: func(topics []string, _ []string) error {
			return fmt.Errorf("topic topic-x not found in cluster link")
		},
	}

	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{
		K8sNamespace:        "ns",
		InitialCrName:       "my-gw",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
		Topics:              []string{"topic-x"},
		FencedCrYAML:        []byte("fenced"),
		SwitchoverCrYAML:    []byte("switchover"),
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.Error(t, err)
	assert.Equal(t, "failed to validate topics in cluster link: topic topic-x not found in cluster link", err.Error())
}

func TestWorkflow_Initialize_NoTopicsDiscoverAll(t *testing.T) {
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("yaml"), nil
		},
	}

	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{
				{MirrorTopicName: "orders", MirrorStatus: "ACTIVE"},
				{MirrorTopicName: "payments", MirrorStatus: "ACTIVE"},
				{MirrorTopicName: "users", MirrorStatus: "ACTIVE"},
			}, nil
		},
		listConfigsFn: func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
			return map[string]string{}, nil
		},
	}

	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{
		K8sNamespace:        "ns",
		InitialCrName:       "my-gw",
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
		Topics:              nil, // empty — should discover all
		FencedCrYAML:        []byte("fenced"),
		SwitchoverCrYAML:    []byte("switchover"),
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.NoError(t, err)

	require.Len(t, config.Topics, 3)

	expected := map[string]bool{"orders": true, "payments": true, "users": true}
	for _, topic := range config.Topics {
		assert.True(t, expected[topic], "unexpected topic %q in discovered topics", topic)
	}
}

// ===========================================================================
// PauseConsumerOffsetSync precondition tests (U2)
// ===========================================================================

// makeOffsetSyncWorkflow builds a workflow with mocks that satisfy Initialize
// up to the cluster-link config check. listConfigsFn is the seam under test.
func makeOffsetSyncWorkflow(t *testing.T, listConfigsFn func(_ context.Context, _ clusterlink.Config) (map[string]string, error)) *CutoverWorkflow {
	t.Helper()
	gw := &mockGatewayService{
		getGatewayYAMLFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("yaml"), nil
		},
	}
	cl := &mockClusterLinkService{
		listMirrorTopicsFn: func(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
			return []clusterlink.MirrorTopic{{MirrorTopicName: "topic-a", MirrorStatus: "ACTIVE"}}, nil
		},
		listConfigsFn: listConfigsFn,
	}
	return NewCutoverWorkflow(gw, cl)
}

func TestWorkflow_Initialize_PauseOffsetSync_Pass(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "true"}, nil
	})
	config := &CutoverConfig{
		ClusterLinkName:         "link-pause",
		PauseConsumerOffsetSync: true,
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.NoError(t, err)
	assert.True(t, config.PauseConsumerOffsetSync, "intent should be retained on config")
	assert.False(t, config.PauseConsumerOffsetSyncFlipped, "flipped marker must remain false at init time")
}

func TestWorkflow_Initialize_PauseOffsetSync_RefusesOnFalse(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "false"}, nil
	})
	config := &CutoverConfig{
		ClusterLinkName:         "link-falsey",
		PauseConsumerOffsetSync: true,
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "link-falsey")
	assert.Contains(t, err.Error(), "consumer.offset.sync.enable")
	assert.Contains(t, err.Error(), `"false"`)
}

func TestWorkflow_Initialize_PauseOffsetSync_RefusesOnAbsentKey(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"other.key": "value"}, nil
	})
	config := &CutoverConfig{
		ClusterLinkName:         "link-absent",
		PauseConsumerOffsetSync: true,
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "link-absent")
	assert.Contains(t, err.Error(), "no consumer.offset.sync.enable config key", "error must distinguish absent key from false value")
}

func TestWorkflow_Initialize_PauseOffsetSync_FlagOff_IgnoresConfigValue(t *testing.T) {
	// Cluster link reports enable=false. Without the flag, init must succeed
	// regardless — the precondition only applies when the operator opted in.
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "false"}, nil
	})
	config := &CutoverConfig{
		ClusterLinkName:         "link-offset-disabled",
		PauseConsumerOffsetSync: false,
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.NoError(t, err, "flag off must not assert offset-sync state")
}

// TestWorkflow_Initialize_PauseOffsetSync_AlreadyFlipped_SkipsPrecondition
// covers the --skip-validate + --pause-consumer-offset-sync flow where:
//  1. init runs with --skip-validate so the precondition was NOT checked at init time
//  2. first execute calls DisableOffsetSync which sets enable=false and marker=true
//  3. FSM transitions out of StateUninitialized, calling Initialize
//
// At step 3 the live config is "false" (kcp just set it) and the marker is
// true, meaning kcp is the reason the value drifted. Initialize must NOT
// refuse — that would wedge the cutover mid-flight.
func TestWorkflow_Initialize_PauseOffsetSync_AlreadyFlipped_SkipsPrecondition(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "false"}, nil
	})
	config := &CutoverConfig{
		ClusterLinkName:                "link-mid-flight",
		PauseConsumerOffsetSync:        true,
		PauseConsumerOffsetSyncFlipped: true,
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.NoError(t, err, "Initialize must not refuse when kcp already flipped the config (Flipped=true)")
}

// TestWorkflow_Initialize_PauseOffsetSync_AlreadyFlipped_PreservesSnapshot
// pins the defensive guard against the snapshot-clobbering bug. If Initialize
// is ever called after DisableOffsetSync has run (i.e. Flipped=true), the live
// configs reflect the post-disable state. Writing them to ClusterLinkConfigs
// would clobber the pre-disable snapshot that RestoreOffsetSync needs to diff
// against. The guard must keep the existing snapshot in that case.
//
// Today the CLI blocks the ordering hazard via mutual exclusion of
// --skip-validate and --pause-consumer-offset-sync, but this test pins the
// in-code defense in case a future caller reintroduces the ordering.
func TestWorkflow_Initialize_PauseOffsetSync_AlreadyFlipped_PreservesSnapshot(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		// Post-disable live state — toggle false, filters cleared.
		return map[string]string{"consumer.offset.sync.enable": "false"}, nil
	})
	preDisableSnapshot := map[string]string{
		"consumer.offset.sync.enable":   "true",
		"consumer.offset.group.filters": `{"groups":["app-*"]}`,
	}
	config := &CutoverConfig{
		ClusterLinkName:                "link-mid-flight",
		PauseConsumerOffsetSync:        true,
		PauseConsumerOffsetSyncFlipped: true,
		ClusterLinkConfigs:             preDisableSnapshot,
	}

	err := wf.Initialize(context.Background(), config, "key", "secret")
	require.NoError(t, err)

	assert.Equal(t, "true", config.ClusterLinkConfigs["consumer.offset.sync.enable"],
		"pre-disable toggle value must survive Initialize when Flipped=true")
	assert.Equal(t, `{"groups":["app-*"]}`, config.ClusterLinkConfigs["consumer.offset.group.filters"],
		"pre-disable filters value must survive Initialize when Flipped=true")
}

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

	wf := NewCutoverWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &CutoverConfig{
		Topics: []string{"topic-1", "topic-2"},
	}

	err := wf.CheckLags(context.Background(), config, 10, "key", "secret")
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

	wf := NewCutoverWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &CutoverConfig{
		Topics: []string{},
	}

	err := wf.CheckLags(context.Background(), config, 10, "key", "secret")
	require.NoError(t, err)
}

func TestWorkflow_CheckLags_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.CheckLags(context.Background(), config, 10, "key", "secret")
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

	wf := NewCutoverWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &CutoverConfig{
		Topics: []string{"topic-1"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	err := wf.CheckLags(ctx, config, 10, "key", "secret")
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

	wf := NewCutoverWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &CutoverConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.CheckLags(context.Background(), config, 10, "key", "secret")
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

	wf := NewCutoverWorkflowWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &CutoverConfig{
		Topics:              []string{"topic-1", "topic-2"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
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

	wf := NewCutoverWorkflowWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &CutoverConfig{
		Topics:              []string{"topic-1", "topic-2"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
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

	wf := NewCutoverWorkflowWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &CutoverConfig{
		Topics:              []string{"topic-1"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := wf.PromoteTopics(ctx, config, "key", "secret")
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

	wf := NewCutoverWorkflowWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &CutoverConfig{
		Topics:              []string{"topic-1"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, atomic.LoadInt64(&listCalls), int64(3),
		"expected PromoteTopics to poll mirror status until STOPPED was observed")
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

	wf := NewCutoverWorkflowWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	wf.promoteBatchSize = batchSize
	config := &CutoverConfig{
		Topics:              topics,
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
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

	wf := NewCutoverWorkflowWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &CutoverConfig{
		Topics:              []string{"topic-1"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	require.Error(t, err)
	assert.Equal(t, "topic topic-1 failed promotion after 3 attempts: persistent error", err.Error())
}

func TestWorkflow_PromoteTopics_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	require.Error(t, err)
	assert.Equal(t, "source and destination offset services are required", err.Error())
}

// ===========================================================================
// FenceGateway / SwitchGateway tests
// ===========================================================================

func TestWorkflow_FenceGateway_HappyPath(t *testing.T) {
	var callOrder []string
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error {
			callOrder = append(callOrder, "apply")
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _ time.Duration, _ time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
			callOrder = append(callOrder, "wait")
			if onProgress != nil {
				onProgress(gateway.GatewayReadinessProgress{InitialPodCount: 3, PodsReady: 3, Elapsed: 2 * time.Second, RolloutDetected: true, Ready: true})
			}
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, []string{"apply", "wait"}, callOrder, "apply must precede wait")
}

func TestWorkflow_FenceGateway_DoesNotCallUIDDiffingMethods(t *testing.T) {
	var unwantedCall string
	gw := &mockGatewayService{
		getGatewayPodUIDsFn: func(_ context.Context, _, _ string) (map[k8stypes.UID]struct{}, error) {
			unwantedCall = "GetGatewayPodUIDs"
			return nil, nil
		},
		waitForGatewayPodsFn: func(_ context.Context, _, _ string, _ map[k8stypes.UID]struct{}, _, _ time.Duration, _ func(gateway.PodRolloutProgress)) error {
			unwantedCall = "WaitForGatewayPods"
			return nil
		},
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error {
			return nil
		},
		// waitForGatewayReadyFn defaults to nil → returns nil success
	}
	cl := &mockClusterLinkService{}
	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Empty(t, unwantedCall, "FenceGateway must not use UID-diffing methods, but called: %s", unwantedCall)
}

func TestWorkflow_FenceGateway_ApplyFailsReturnsWrappedError(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error {
			return fmt.Errorf("k8s 403")
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

	err := wf.FenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to apply fenced gateway CR")
	assert.Contains(t, err.Error(), "k8s 403")
}

func TestWorkflow_FenceGateway_WaitTimeoutPropagatesDeadlineExceeded(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error {
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return fmt.Errorf("rollout-timeout exceeded: %w", context.DeadlineExceeded)
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewCutoverWorkflow(gw, cl)
	wf.SetRolloutTimeout(100 * time.Millisecond)
	config := &CutoverConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

	err := wf.FenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "DeadlineExceeded must propagate: %v", err)
}

func TestWorkflow_FenceGateway_WaitContextCancelledPropagates(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error {
			return nil
		},
		waitForGatewayReadyFn: func(ctx context.Context, _, _ string, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

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
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error { return nil },
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _, timeout time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			observedTimeout = timeout
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewCutoverWorkflow(gw, cl)
	wf.SetRolloutTimeout(15 * time.Minute)
	config := &CutoverConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, observedTimeout)
}

func TestWorkflow_FenceGateway_DefaultRolloutTimeoutIsZero(t *testing.T) {
	var observedTimeout time.Duration
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error { return nil },
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _, timeout time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			observedTimeout = timeout
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

	err := wf.FenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), observedTimeout, "default rolloutTimeout should be 0 (no deadline)")
}

func TestWorkflow_SwitchGateway_HappyPath(t *testing.T) {
	var callOrder []string
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, yaml []byte) error {
			callOrder = append(callOrder, fmt.Sprintf("apply:%s", string(yaml)))
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			callOrder = append(callOrder, "wait")
			return nil
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{K8sNamespace: "ns", InitialCrName: "gw-1", SwitchoverCrYAML: []byte("switchover")}

	err := wf.SwitchGateway(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, []string{"apply:switchover", "wait"}, callOrder, "apply (switchover YAML) must precede wait")
}

func TestWorkflow_SwitchGateway_WaitErrorIsWrapped(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error { return nil },
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return fmt.Errorf("kube unreachable")
		},
	}
	cl := &mockClusterLinkService{}
	wf := NewCutoverWorkflow(gw, cl)
	config := &CutoverConfig{K8sNamespace: "ns", InitialCrName: "gw-1", SwitchoverCrYAML: []byte("switchover")}

	err := wf.SwitchGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway readiness")
	assert.Contains(t, err.Error(), "kube unreachable")
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
