package migration

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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

// Initialize validates infrastructure and populates migration config
func (s *DefaultWorkflowService) Initialize(
	ctx context.Context,
	config *types.MigrationConfig,
	clusterApiKey, clusterApiSecret string,
) error {
	slog.Info("initializing migration", "migrationId", config.MigrationId)

	// Check Kubernetes permissions
	allowed, err := s.gatewayService.CheckPermissions(
		ctx,
		"update",
		gateway.GatewayResourcePlural,
		gateway.GatewayGroup,
		config.GatewayNamespace,
	)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !allowed {
		return fmt.Errorf("you don't have permission to update gateway resources")
	}
	slog.Info("permission check passed", "verb", "update")

	// Get and validate gateway
	gatewayYAML, err := s.gatewayService.GetGatewayYAML(ctx, config.GatewayNamespace, config.GatewayCrdName)
	if err != nil {
		return fmt.Errorf("failed to get gateway as YAML: %w", err)
	}

	gatewayConfig := gateway.GatewayConfig{
		Namespace:            config.GatewayNamespace,
		CRDName:              config.GatewayCrdName,
		SourceName:           config.SourceName,
		DestinationName:      config.DestinationName,
		SourceRouteName:      config.SourceRouteName,
		DestinationRouteName: config.DestinationRouteName,
		AuthMode:             config.AuthMode,
		KubeConfigPath:       config.KubeConfigPath,
	}

	if err := s.gatewayService.ValidateGateway(ctx, gatewayYAML, gatewayConfig); err != nil {
		return fmt.Errorf("gateway validation failed: %w", err)
	}

	slog.Info("gateway validation successful",
		"source", config.SourceName,
		"destination", config.DestinationName,
		"route", config.SourceRouteName,
	)

	// Validate cluster link and topics
	clusterLinkConfig := clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		APIKey:       clusterApiKey,
		APISecret:    clusterApiSecret,
		Topics:       config.Topics,
	}

	slog.Info("describing cluster link", "clusterId", config.ClusterId, "clusterLinkName", config.ClusterLinkName)

	mirrorTopics, err := s.clusterLinkService.ListMirrorTopics(ctx, clusterLinkConfig)
	if err != nil {
		return fmt.Errorf("failed to list mirror topics: %w", err)
	}

	clusterLinkTopics, inactiveTopics := clusterlink.ClassifyMirrorTopics(mirrorTopics)
	if len(inactiveTopics) > 0 {
		return fmt.Errorf("%d mirror topics are not active: %s", len(inactiveTopics), strings.Join(inactiveTopics, ", "))
	}

	// Validate topics
	if len(config.Topics) > 0 {
		slog.Info("validating topics in cluster link", "topic count", len(config.Topics))
		if err := s.clusterLinkService.ValidateTopics(config.Topics, clusterLinkTopics); err != nil {
			return fmt.Errorf("failed to validate topics in cluster link: %w", err)
		}
	} else {
		config.Topics = clusterLinkTopics
	}

	// Get cluster link configs
	configs, err := s.clusterLinkService.ListConfigs(ctx, clusterLinkConfig)
	if err != nil {
		return fmt.Errorf("failed to list cluster link configs: %w", err)
	}

	// Update config with discovered data
	config.GatewayOriginalYAML = gatewayYAML
	config.ClusterLinkTopics = clusterLinkTopics
	config.ClusterLinkConfigs = configs

	slog.Info("migration initialized successfully")
	return nil
}
