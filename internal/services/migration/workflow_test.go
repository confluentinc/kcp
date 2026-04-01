package migration

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
		Topics: []string{},
	}

	err := wf.CheckLags(context.Background(), config, 10, "key", "secret")
	require.NoError(t, err)
}

func TestWorkflow_CheckLags_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationWorkflow(gw, cl)
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
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
	config := &types.MigrationConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	require.Error(t, err)
	assert.Equal(t, "source and destination offset services are required", err.Error())
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
