package migration

import (
	"context"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/types"
)

// WorkflowService defines the business operations for migration workflow
type WorkflowService interface {
	Initialize(ctx context.Context, config *types.MigrationConfig, clusterApiKey, clusterApiSecret string) error
	CheckLags(ctx context.Context, config *types.MigrationConfig, threshold, maxWaitTime int64, clusterApiKey, clusterApiSecret string) error
	FenceGateway(ctx context.Context, config *types.MigrationConfig) error
	PromoteTopics(ctx context.Context, config *types.MigrationConfig, clusterApiKey, clusterApiSecret string) error
	CheckPromotionCompletion(ctx context.Context, config *types.MigrationConfig) error
	SwitchGateway(ctx context.Context, config *types.MigrationConfig) error
}

// DefaultWorkflowService implements WorkflowService using injected services
type DefaultWorkflowService struct {
	gatewayService     gateway.Service
	clusterLinkService clusterlink.Service
}

// NewDefaultWorkflowService creates a new workflow service with injected dependencies
func NewDefaultWorkflowService(
	gatewayService gateway.Service,
	clusterLinkService clusterlink.Service,
) *DefaultWorkflowService {
	return &DefaultWorkflowService{
		gatewayService:     gatewayService,
		clusterLinkService: clusterLinkService,
	}
}
