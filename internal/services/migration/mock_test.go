package migration

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/gateway/gatewaytest"
)

// mockGatewayService is the shared gateway.TransitionClient fake.
type mockGatewayService = gatewaytest.Fake

// mockClusterLinkService implements clusterlink.Service using function fields for test control.
type mockClusterLinkService struct {
	getClusterLinkFn      func(ctx context.Context, config clusterlink.Config) (*clusterlink.ClusterLink, error)
	listMirrorTopicsFn    func(ctx context.Context, config clusterlink.Config) ([]clusterlink.MirrorTopic, error)
	listConfigsFn         func(ctx context.Context, config clusterlink.Config) (map[string]string, error)
	validateTopicsFn      func(topics []string, clusterLinkTopics []string) error
	promoteMirrorTopicsFn func(ctx context.Context, config clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error)
	// promoteWithOptionsFn, if set, is called in place of promoteMirrorTopicsFn with the call's options.
	promoteWithOptionsFn func(ctx context.Context, config clusterlink.Config, topicNames []string, opts clusterlink.PromoteOptions) (*clusterlink.PromoteMirrorTopicsResponse, error)
	alterConfigsFn       func(ctx context.Context, config clusterlink.Config, alterations []clusterlink.ConfigAlteration) error
	createMirrorTopicFn  func(ctx context.Context, config clusterlink.Config, sourceTopic, mirrorTopic string) error
	listTopicsFn         func(ctx context.Context, config clusterlink.Config) ([]string, error)
	createTopicFn        func(ctx context.Context, config clusterlink.Config, req clusterlink.CreateTopicRequest) error
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

func (m *mockClusterLinkService) PromoteMirrorTopics(ctx context.Context, config clusterlink.Config, topicNames []string, opts ...clusterlink.PromoteOption) (*clusterlink.PromoteMirrorTopicsResponse, error) {
	if m.promoteWithOptionsFn != nil {
		return m.promoteWithOptionsFn(ctx, config, topicNames, clusterlink.ApplyPromoteOptions(opts...))
	}
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

// promoteVia adapts svc's PromoteMirrorTopics to a promoteMirrorTopicsFn.
func promoteVia(svc clusterlink.Service) func(context.Context, clusterlink.Config, []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
	return func(ctx context.Context, config clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
		return svc.PromoteMirrorTopics(ctx, config, topicNames)
	}
}

// fakeTransitioner is the transitioner factory over a test gateway client; a
// nil client yields a Transitioner whose HasClient is false.
func fakeTransitioner(gw gateway.TransitionClient) gateway.TransitionerFactory {
	return gateway.NewTransitionerFactory(gw)
}
