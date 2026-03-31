package migration

import (
	"context"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// mockGatewayService implements gateway.Service using function fields for test control.
type mockGatewayService struct {
	getGatewayYAMLFn     func(ctx context.Context, namespace, name string) ([]byte, error)
	validateGatewayCRsFn func(initial, fenced, switchover []byte) error
	checkPermissionsFn   func(ctx context.Context, verb, resource, group, namespace string) (bool, error)
	applyGatewayYAMLFn   func(ctx context.Context, namespace, name string, yaml []byte) error
	getGatewayPodUIDsFn  func(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error)
	waitForGatewayPodsFn func(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error
}

func (m *mockGatewayService) GetGatewayYAML(ctx context.Context, namespace, name string) ([]byte, error) {
	if m.getGatewayYAMLFn != nil {
		return m.getGatewayYAMLFn(ctx, namespace, name)
	}
	return nil, nil
}

func (m *mockGatewayService) ValidateGatewayCRs(initial, fenced, switchover []byte) error {
	if m.validateGatewayCRsFn != nil {
		return m.validateGatewayCRsFn(initial, fenced, switchover)
	}
	return nil
}

func (m *mockGatewayService) CheckPermissions(ctx context.Context, verb, resource, group, namespace string) (bool, error) {
	if m.checkPermissionsFn != nil {
		return m.checkPermissionsFn(ctx, verb, resource, group, namespace)
	}
	return true, nil
}

func (m *mockGatewayService) ApplyGatewayYAML(ctx context.Context, namespace, name string, yaml []byte) error {
	if m.applyGatewayYAMLFn != nil {
		return m.applyGatewayYAMLFn(ctx, namespace, name, yaml)
	}
	return nil
}

func (m *mockGatewayService) GetGatewayPodUIDs(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error) {
	if m.getGatewayPodUIDsFn != nil {
		return m.getGatewayPodUIDsFn(ctx, namespace, name)
	}
	return map[k8stypes.UID]struct{}{}, nil
}

func (m *mockGatewayService) WaitForGatewayPods(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error {
	if m.waitForGatewayPodsFn != nil {
		return m.waitForGatewayPodsFn(ctx, namespace, name, initialPodUIDs, pollInterval, timeout, onProgress)
	}
	return nil
}

// mockClusterLinkService implements clusterlink.Service using function fields for test control.
type mockClusterLinkService struct {
	listMirrorTopicsFn    func(ctx context.Context, config clusterlink.Config) ([]clusterlink.MirrorTopic, error)
	listConfigsFn         func(ctx context.Context, config clusterlink.Config) (map[string]string, error)
	validateTopicsFn      func(topics []string, clusterLinkTopics []string) error
	promoteMirrorTopicsFn func(ctx context.Context, config clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error)
}

func (m *mockClusterLinkService) ListMirrorTopics(ctx context.Context, config clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
	if m.listMirrorTopicsFn != nil {
		return m.listMirrorTopicsFn(ctx, config)
	}
	return nil, nil
}

func (m *mockClusterLinkService) ListConfigs(ctx context.Context, config clusterlink.Config) (map[string]string, error) {
	if m.listConfigsFn != nil {
		return m.listConfigsFn(ctx, config)
	}
	return map[string]string{}, nil
}

func (m *mockClusterLinkService) ValidateTopics(topics []string, clusterLinkTopics []string) error {
	if m.validateTopicsFn != nil {
		return m.validateTopicsFn(topics, clusterLinkTopics)
	}
	return nil
}

func (m *mockClusterLinkService) PromoteMirrorTopics(ctx context.Context, config clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
	if m.promoteMirrorTopicsFn != nil {
		return m.promoteMirrorTopicsFn(ctx, config, topicNames)
	}
	return &clusterlink.PromoteMirrorTopicsResponse{}, nil
}

// mockOffsetProvider implements offset.Provider using a function field for test control.
type mockOffsetProvider struct {
	getFn func(topic string) (map[int32]int64, error)
}

func (m *mockOffsetProvider) Get(topic string) (map[int32]int64, error) {
	if m.getFn != nil {
		return m.getFn(topic)
	}
	return map[int32]int64{}, nil
}
