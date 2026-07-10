package migration

import (
	"context"
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// mockGatewayService implements gateway.Service using function fields for test control.
type mockGatewayService struct {
	getGatewayYAMLFn                   func(ctx context.Context, namespace, name string) ([]byte, error)
	validateGatewayCRsFn               func(initial, fenced, switchover []byte) error
	checkPermissionsFn                 func(ctx context.Context, verb, resource, group, namespace string) (bool, error)
	applyGatewayYAMLFn                 func(ctx context.Context, namespace, name string, yaml []byte) error
	waitForGatewayObservedGenerationFn func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration) error
	getGatewayPodUIDsFn                func(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error)
	waitForGatewayPodsFn               func(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error
	waitForGatewayReadyFn              func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error
}

func (m *mockGatewayService) GetGatewayYAML(ctx context.Context, namespace, name string) ([]byte, error) {
	if m.getGatewayYAMLFn != nil {
		return m.getGatewayYAMLFn(ctx, namespace, name)
	}
	return nil, fmt.Errorf("mockGatewayService.GetGatewayYAML not configured")
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
	return fmt.Errorf("mockGatewayService.ApplyGatewayYAML not configured")
}

func (m *mockGatewayService) WaitForGatewayObservedGeneration(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration) error {
	if m.waitForGatewayObservedGenerationFn != nil {
		return m.waitForGatewayObservedGenerationFn(ctx, namespace, name, pollInterval, timeout)
	}
	return nil
}

func (m *mockGatewayService) GetGatewayPodUIDs(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error) {
	if m.getGatewayPodUIDsFn != nil {
		return m.getGatewayPodUIDsFn(ctx, namespace, name)
	}
	return nil, fmt.Errorf("mockGatewayService.GetGatewayPodUIDs not configured")
}

func (m *mockGatewayService) WaitForGatewayPods(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error {
	if m.waitForGatewayPodsFn != nil {
		return m.waitForGatewayPodsFn(ctx, namespace, name, initialPodUIDs, pollInterval, timeout, onProgress)
	}
	return fmt.Errorf("mockGatewayService.WaitForGatewayPods not configured")
}

func (m *mockGatewayService) WaitForGatewayReady(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration, onProgress func(gateway.GatewayReadinessProgress)) error {
	if m.waitForGatewayReadyFn != nil {
		return m.waitForGatewayReadyFn(ctx, namespace, name, pollInterval, timeout, onProgress)
	}
	return nil
}

// mockClusterLinkService implements clusterlink.Service using function fields for test control.
type mockClusterLinkService struct {
	getClusterLinkFn      func(ctx context.Context, config clusterlink.Config) (*clusterlink.ClusterLink, error)
	listMirrorTopicsFn    func(ctx context.Context, config clusterlink.Config) ([]clusterlink.MirrorTopic, error)
	listConfigsFn         func(ctx context.Context, config clusterlink.Config) (map[string]string, error)
	validateTopicsFn      func(topics []string, clusterLinkTopics []string) error
	promoteMirrorTopicsFn func(ctx context.Context, config clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error)
	alterConfigsFn        func(ctx context.Context, config clusterlink.Config, alterations []clusterlink.ConfigAlteration) error
}

func (m *mockClusterLinkService) GetClusterLink(ctx context.Context, config clusterlink.Config) (*clusterlink.ClusterLink, error) {
	if m.getClusterLinkFn != nil {
		return m.getClusterLinkFn(ctx, config)
	}
	return nil, fmt.Errorf("mockClusterLinkService.GetClusterLink not configured")
}

func (m *mockClusterLinkService) ListMirrorTopics(ctx context.Context, config clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
	if m.listMirrorTopicsFn != nil {
		return m.listMirrorTopicsFn(ctx, config)
	}
	return nil, fmt.Errorf("mockClusterLinkService.ListMirrorTopics not configured")
}

func (m *mockClusterLinkService) ListConfigs(ctx context.Context, config clusterlink.Config) (map[string]string, error) {
	if m.listConfigsFn != nil {
		return m.listConfigsFn(ctx, config)
	}
	return nil, fmt.Errorf("mockClusterLinkService.ListConfigs not configured")
}

func (m *mockClusterLinkService) ValidateTopics(topics []string, clusterLinkTopics []string) error {
	if m.validateTopicsFn != nil {
		return m.validateTopicsFn(topics, clusterLinkTopics)
	}
	return fmt.Errorf("mockClusterLinkService.ValidateTopics not configured")
}

func (m *mockClusterLinkService) PromoteMirrorTopics(ctx context.Context, config clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
	if m.promoteMirrorTopicsFn != nil {
		return m.promoteMirrorTopicsFn(ctx, config, topicNames)
	}
	return nil, fmt.Errorf("mockClusterLinkService.PromoteMirrorTopics not configured")
}

func (m *mockClusterLinkService) AlterConfigs(ctx context.Context, config clusterlink.Config, alterations []clusterlink.ConfigAlteration) error {
	if m.alterConfigsFn != nil {
		return m.alterConfigsFn(ctx, config, alterations)
	}
	return fmt.Errorf("mockClusterLinkService.AlterConfigs not configured")
}

// mockOffsetProvider implements offset.Provider using function fields for
// test control. Tests may script the whole batch via getManyFn or, more
// commonly, per topic via getFn — the GetMany fallback assembles the batch
// result from per-topic calls, failing the whole sweep on the first error
// (matching the real GetMany's all-or-nothing contract).
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
