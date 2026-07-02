package migration

import (
	"context"
	"errors"
	"fmt"
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

	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{
		MigrationId:         "test-1",
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

	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{
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

	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{
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

	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{
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

	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{
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
func makeOffsetSyncWorkflow(t *testing.T, listConfigsFn func(_ context.Context, _ clusterlink.Config) (map[string]string, error)) *MigrationWorkflow {
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
	return NewMigrationWorkflow(gw, cl)
}

func TestWorkflow_Initialize_PauseOffsetSync_Pass(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "true"}, nil
	})
	config := &MigrationConfig{
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
	config := &MigrationConfig{
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
	config := &MigrationConfig{
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
	config := &MigrationConfig{
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
// refuse — that would wedge the migration mid-flight.
func TestWorkflow_Initialize_PauseOffsetSync_AlreadyFlipped_SkipsPrecondition(t *testing.T) {
	wf := makeOffsetSyncWorkflow(t, func(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
		return map[string]string{"consumer.offset.sync.enable": "false"}, nil
	})
	config := &MigrationConfig{
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
	config := &MigrationConfig{
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

	wf := NewMigrationWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
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

	wf := NewMigrationWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
		Topics: []string{},
	}

	err := wf.CheckLags(context.Background(), config, 10, "key", "secret")
	require.NoError(t, err)
}

func TestWorkflow_CheckLags_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{
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

	wf := NewMigrationWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
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

	wf := NewMigrationWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &MigrationConfig{
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
	}

	// Both source and dest return identical offsets (zero lag)
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 500, 1: 600}, nil
		},
	}

	wf := NewMigrationWorkflowWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
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
	}

	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 100}, nil
		},
	}

	wf := NewMigrationWorkflowWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
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

	wf := NewMigrationWorkflowWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
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

	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{
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
	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

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
	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

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
	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

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
	wf := NewMigrationWorkflow(gw, cl)
	wf.SetRolloutTimeout(100 * time.Millisecond)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

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
	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

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
	wf := NewMigrationWorkflow(gw, cl)
	wf.SetRolloutTimeout(15 * time.Minute)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

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
	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", FencedCrYAML: []byte("fenced")}

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
	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", SwitchoverCrYAML: []byte("switchover")}

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
	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{K8sNamespace: "ns", InitialCrName: "gw-1", SwitchoverCrYAML: []byte("switchover")}

	err := wf.SwitchGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway readiness")
	assert.Contains(t, err.Error(), "kube unreachable")
}

// ===========================================================================
// DetectUnroutedProducers tests
// ===========================================================================

func TestWorkflow_PromoteTopics_DetectUnroutedProducers_StableOffsets(t *testing.T) {
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
	}

	// Source offsets are stable (same value on every call)
	offsetProvider := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			return map[int32]int64{0: 500, 1: 600}, nil
		},
	}

	wf := NewMigrationWorkflowWithOffsets(gw, cl, offsetProvider, offsetProvider)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:                          []string{"topic-1", "topic-2"},
		ClusterRestEndpoint:             "https://cluster",
		ClusterId:                       "lkc-123",
		ClusterLinkName:                 "link-1",
		DetectUnroutedProducersDuration: time.Millisecond,
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	require.NoError(t, err)
	assert.True(t, promoted["topic-1"], "topic-1 should have been promoted")
	assert.True(t, promoted["topic-2"], "topic-2 should have been promoted")
}

func TestWorkflow_PromoteTopics_DetectUnroutedProducers_IncreasingOffsets_ReturnsError(t *testing.T) {
	// PromoteTopics only detects — it must NOT unfence the gateway itself.
	// Restoring traffic is the orchestrator's job on the abort_fence rollback
	// (see TestOrchestrator_Execute_UnroutedProducers_AbortsFenceAndRollsBack).
	var applyCalled bool
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error {
			applyCalled = true
			return nil
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

	wf := NewMigrationWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		ClusterRestEndpoint:             "https://cluster",
		ClusterId:                       "lkc-123",
		ClusterLinkName:                 "link-1",
		DetectUnroutedProducersDuration: time.Millisecond,
		InitialCrYAML:                   []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gw\n  namespace: ns\n  managedFields: []\n  resourceVersion: \"123\"\n"),
		InitialCrName:                   "my-gw",
		K8sNamespace:                    "ns",
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnroutedProducers)
	assert.Contains(t, err.Error(), "topic-1 partition 0")
	assert.False(t, applyCalled, "PromoteTopics must not unfence the gateway; that is the orchestrator's responsibility")
}

func TestWorkflow_PromoteTopics_DetectUnroutedProducers_FlagDisabled(t *testing.T) {
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
	}

	// Source offsets increase — but flag is off, so it shouldn't matter
	var callCount int64
	sourceOffset := &mockOffsetProvider{
		getFn: func(topic string) (map[int32]int64, error) {
			n := atomic.AddInt64(&callCount, 1)
			return map[int32]int64{0: 100 + n*10}, nil
		},
	}

	wf := NewMigrationWorkflowWithOffsets(gw, cl, sourceOffset, sourceOffset)
	wf.promotePollInterval = time.Millisecond
	config := &MigrationConfig{
		Topics:                          []string{"topic-1"},
		ClusterRestEndpoint:             "https://cluster",
		ClusterId:                       "lkc-123",
		ClusterLinkName:                 "link-1",
		DetectUnroutedProducersDuration: 0, // check disabled
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	require.NoError(t, err, "with flag disabled, increasing offsets should not block promotion")
	assert.True(t, promoted["topic-1"])
}

func TestWorkflow_UnfenceGateway_StripsServerMetadata(t *testing.T) {
	var appliedYAML []byte
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, yaml []byte) error {
			appliedYAML = yaml
			return nil
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{
		InitialCrName: "my-gw",
		K8sNamespace:  "confluent",
		InitialCrYAML: []byte(`apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: my-gw
  namespace: confluent
  managedFields:
  - manager: confluent-operator
    operation: Apply
  resourceVersion: "12345"
  uid: abc-def-123
  creationTimestamp: "2026-01-01T00:00:00Z"
  generation: 3
spec:
  streamingDomains:
  - name: source-kafka-cluster
status:
  phase: RUNNING
`),
	}

	err := wf.unfenceGateway(context.Background(), config)
	require.NoError(t, err)
	require.NotNil(t, appliedYAML, "ApplyGatewayYAML should have been called")

	yamlStr := string(appliedYAML)
	assert.NotContains(t, yamlStr, "managedFields", "managedFields should be stripped")
	assert.NotContains(t, yamlStr, "resourceVersion", "resourceVersion should be stripped")
	assert.NotContains(t, yamlStr, "uid", "uid should be stripped")
	assert.NotContains(t, yamlStr, "creationTimestamp", "creationTimestamp should be stripped")
	assert.NotContains(t, yamlStr, "generation", "generation should be stripped")
	assert.NotContains(t, yamlStr, "status", "status should be stripped")
	assert.Contains(t, yamlStr, "streamingDomains", "spec should be preserved")
	assert.Contains(t, yamlStr, "source-kafka-cluster", "spec values should be preserved")
}

func TestWorkflow_UnfenceGateway_WaitsForGatewayReadiness(t *testing.T) {
	var applyCalled, waitCalled bool
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error {
			applyCalled = true
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, namespace, name string, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			waitCalled = true
			assert.True(t, applyCalled, "readiness wait must happen after the CR is applied")
			assert.Equal(t, "confluent", namespace)
			assert.Equal(t, "my-gw", name)
			return nil
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{
		InitialCrName: "my-gw",
		K8sNamespace:  "confluent",
		InitialCrYAML: []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gw\n  namespace: confluent\n"),
	}

	err := wf.unfenceGateway(context.Background(), config)
	require.NoError(t, err)
	assert.True(t, waitCalled, "unfenceGateway should wait for gateway readiness after applying the initial CR")
}

func TestWorkflow_UnfenceGateway_ReadinessFailure_ReturnsError(t *testing.T) {
	gw := &mockGatewayService{
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, _ []byte) error {
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			return fmt.Errorf("gateway pods did not converge")
		},
	}
	cl := &mockClusterLinkService{}

	wf := NewMigrationWorkflow(gw, cl)
	config := &MigrationConfig{
		InitialCrName: "my-gw",
		K8sNamespace:  "confluent",
		InitialCrYAML: []byte("apiVersion: platform.confluent.io/v1beta1\nkind: Gateway\nmetadata:\n  name: my-gw\n  namespace: confluent\n"),
	}

	err := wf.unfenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway readiness after unfence")
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

	wf := NewMigrationWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)

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
