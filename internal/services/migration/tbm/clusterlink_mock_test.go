package tbm

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

// mockClusterLinkService implements clusterlink.Service using function fields
// for test control, mirroring migration's own (unexported, package-private)
// mockClusterLinkService — this is TBM's own copy, not shared, since the two
// packages intentionally have no cross-imports.
type mockClusterLinkService struct {
	getClusterLinkFn      func(ctx context.Context, config clusterlink.Config) (*clusterlink.ClusterLink, error)
	listMirrorTopicsFn    func(ctx context.Context, config clusterlink.Config) ([]clusterlink.MirrorTopic, error)
	listConfigsFn         func(ctx context.Context, config clusterlink.Config) (map[string]string, error)
	validateTopicsFn      func(topics []string, clusterLinkTopics []string) error
	promoteMirrorTopicsFn func(ctx context.Context, config clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error)
	alterConfigsFn        func(ctx context.Context, config clusterlink.Config, alterations []clusterlink.ConfigAlteration) error
	createMirrorTopicFn   func(ctx context.Context, config clusterlink.Config, sourceTopic, mirrorTopic string) error
	listTopicsFn          func(ctx context.Context, config clusterlink.Config) ([]string, error)
	createTopicFn         func(ctx context.Context, config clusterlink.Config, req clusterlink.CreateTopicRequest) error
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

func (m *mockClusterLinkService) CreateMirrorTopic(ctx context.Context, config clusterlink.Config, sourceTopic, mirrorTopic string) error {
	if m.createMirrorTopicFn != nil {
		return m.createMirrorTopicFn(ctx, config, sourceTopic, mirrorTopic)
	}
	return fmt.Errorf("mockClusterLinkService.CreateMirrorTopic not configured")
}

func (m *mockClusterLinkService) ListTopics(ctx context.Context, config clusterlink.Config) ([]string, error) {
	if m.listTopicsFn != nil {
		return m.listTopicsFn(ctx, config)
	}
	return nil, fmt.Errorf("mockClusterLinkService.ListTopics not configured")
}

func (m *mockClusterLinkService) CreateTopic(ctx context.Context, config clusterlink.Config, req clusterlink.CreateTopicRequest) error {
	if m.createTopicFn != nil {
		return m.createTopicFn(ctx, config, req)
	}
	return fmt.Errorf("mockClusterLinkService.CreateTopic not configured")
}
