package migration

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/types"
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
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if string(config.InitialCrYAML) != "initial-yaml" {
		t.Errorf("expected InitialCrYAML to be 'initial-yaml', got %q", config.InitialCrYAML)
	}
	if len(config.ClusterLinkTopics) != 3 {
		t.Errorf("expected 3 cluster link topics, got %d", len(config.ClusterLinkTopics))
	}
	if config.ClusterLinkConfigs["bootstrap.servers"] != "broker:9092" {
		t.Errorf("expected cluster link config 'bootstrap.servers'='broker:9092', got %q", config.ClusterLinkConfigs["bootstrap.servers"])
	}
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
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "failed to get initial CR YAML: k8s unreachable" {
		t.Errorf("unexpected error message: %s", got)
	}
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
	if err == nil {
		t.Fatal("expected error for inactive topics, got nil")
	}
	if got := err.Error(); got != "1 mirror topics are not active: topic-b (status: PAUSED)" {
		t.Errorf("unexpected error: %s", got)
	}
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
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if got := err.Error(); got != "failed to validate topics in cluster link: topic topic-x not found in cluster link" {
		t.Errorf("unexpected error: %s", got)
	}
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
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if len(config.Topics) != 3 {
		t.Fatalf("expected 3 discovered topics, got %d", len(config.Topics))
	}

	expected := map[string]bool{"orders": true, "payments": true, "users": true}
	for _, topic := range config.Topics {
		if !expected[topic] {
			t.Errorf("unexpected topic %q in discovered topics", topic)
		}
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
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestWorkflow_CheckLags_NoTopics(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	sourceOffset := &mockOffsetProvider{}
	destOffset := &mockOffsetProvider{}

	wf := NewMigrationWorkflowWithOffsets(gw, cl, sourceOffset, destOffset)
	config := &types.MigrationConfig{
		Topics: []string{},
	}

	err := wf.CheckLags(context.Background(), config, 10, "key", "secret")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestWorkflow_CheckLags_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationWorkflow(gw, cl)
	config := &types.MigrationConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.CheckLags(context.Background(), config, 10, "key", "secret")
	if err == nil {
		t.Fatal("expected error for nil offset services, got nil")
	}
	if got := err.Error(); got != "source and destination offset services are required" {
		t.Errorf("unexpected error: %s", got)
	}
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
	if err == nil {
		t.Fatal("expected context cancelled error, got nil")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
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
	config := &types.MigrationConfig{
		Topics:              []string{"topic-1", "topic-2"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !promoted["topic-1"] || !promoted["topic-2"] {
		t.Errorf("expected both topics promoted, got: %v", promoted)
	}
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
	config := &types.MigrationConfig{
		Topics:              []string{"topic-1", "topic-2"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	if err != nil {
		t.Fatalf("expected nil error (retry should succeed), got: %v", err)
	}

	finalCallCount := atomic.LoadInt64(&callCount)
	if finalCallCount < 2 {
		t.Errorf("expected at least 2 promote calls (initial + retry), got %d", finalCallCount)
	}
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
	config := &types.MigrationConfig{
		Topics:              []string{"topic-1"},
		ClusterRestEndpoint: "https://cluster",
		ClusterId:           "lkc-123",
		ClusterLinkName:     "link-1",
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	if err == nil {
		t.Fatal("expected error after max retries, got nil")
	}
	expected := "topic topic-1 failed promotion after 3 attempts: persistent error"
	if got := err.Error(); got != expected {
		t.Errorf("unexpected error:\n  got:  %s\n  want: %s", got, expected)
	}
}

func TestWorkflow_PromoteTopics_NilOffsetServices(t *testing.T) {
	gw := &mockGatewayService{}
	cl := &mockClusterLinkService{}

	wf := NewMigrationWorkflow(gw, cl)
	config := &types.MigrationConfig{
		Topics: []string{"topic-1"},
	}

	err := wf.PromoteTopics(context.Background(), config, "key", "secret")
	if err == nil {
		t.Fatal("expected error for nil offset services, got nil")
	}
	if got := err.Error(); got != "source and destination offset services are required" {
		t.Errorf("unexpected error: %s", got)
	}
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
			if got != tc.expected {
				t.Errorf("formatLag64(%d) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}
