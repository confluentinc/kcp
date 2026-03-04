package migration

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
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

// CheckLags polls mirror topics until lag is below threshold or timeout
func (s *DefaultWorkflowService) CheckLags(
	ctx context.Context,
	config *types.MigrationConfig,
	threshold, maxWaitTime int64,
	clusterApiKey, clusterApiSecret string,
) error {
	fmt.Printf("\n%s Checking mirror lag across %s (threshold: %s, timeout: %s)\n\n",
		color.CyanString("⏳"),
		color.CyanString("%d topics", len(config.Topics)),
		color.YellowString("%d", threshold),
		color.YellowString("%ds", maxWaitTime))

	// Early exit if no topics to check
	if len(config.Topics) == 0 {
		fmt.Printf("%s No topics to check\n", color.GreenString("✔"))
		return nil
	}

	// Build cluster link configuration for API calls
	clusterLinkConfig := clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		APIKey:       clusterApiKey,
		APISecret:    clusterApiSecret,
	}

	// Set up polling interval (check every 2 seconds)
	pollInterval := 2 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Record start time for timeout tracking
	startTime := time.Now()
	maxWaitDuration := time.Duration(maxWaitTime) * time.Second

	// Main polling loop
	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if maxWaitTime has been exceeded
		elapsed := time.Since(startTime)
		if elapsed >= maxWaitDuration {
			return fmt.Errorf("max wait time exceeded (%v): lag threshold (%d) not met within %v", elapsed, threshold, maxWaitDuration)
		}

		// Fetch current mirror topic status
		mirrorTopics, err := s.clusterLinkService.ListMirrorTopics(ctx, clusterLinkConfig)
		if err != nil {
			return fmt.Errorf("failed to list mirror topics: %w", err)
		}

		// Build a map of topics we care about
		topicMap := make(map[string]bool)
		for _, topic := range config.Topics {
			topicMap[topic] = true
		}

		// Check lag for all partitions of all relevant mirror topics
		allBelowThreshold := true
		topicLags := make(map[string][]string)
		topicTotalLags := make(map[string]int)

		for _, mirrorTopic := range mirrorTopics {
			// Skip topics not in our migration list
			if !topicMap[mirrorTopic.MirrorTopicName] {
				continue
			}

			// Calculate total lag across all partitions for this topic
			totalLag := 0
			partitionLags := make([]string, 0, len(mirrorTopic.MirrorLags))

			for _, lag := range mirrorTopic.MirrorLags {
				totalLag += lag.Lag
				partitionLags = append(partitionLags,
					fmt.Sprintf("p%d:%d", lag.Partition, lag.Lag))
			}

			// Store total lag for this topic
			topicTotalLags[mirrorTopic.MirrorTopicName] = totalLag

			// Check if topic's TOTAL lag exceeds threshold
			if totalLag >= int(threshold) {
				allBelowThreshold = false
				topicLags[mirrorTopic.MirrorTopicName] = partitionLags
			}
		}

		// Success: all partition lags are below threshold
		if allBelowThreshold {
			fmt.Printf("\n%s All mirror topic lags below threshold (%d)\n",
				color.GreenString("✔"),
				threshold)
			return nil
		}

		// Build sorted list of topic names with lag
		lagTopics := make([]string, 0, len(topicLags))
		for topic := range topicLags {
			lagTopics = append(lagTopics, topic)
		}
		sort.Strings(lagTopics)

		elapsed = time.Since(startTime)
		remaining := maxWaitDuration - elapsed

		fmt.Printf("%s Waiting for lag to clear  %s  %s  %s\n",
			color.YellowString("⏳"),
			color.YellowString("%d/%d topics behind", len(topicLags), len(config.Topics)),
			color.CyanString("elapsed %s", elapsed.Round(time.Second)),
			color.CyanString("remaining %s", remaining.Round(time.Second)))

		for _, topic := range lagTopics {
			parts := topicLags[topic]
			totalLag := topicTotalLags[topic]
			fmt.Printf("   %s %s  %s\n",
				color.YellowString("↳"),
				color.WhiteString(topic),
				color.HiBlackString("(total lag: %d, %d partitions: %s)",
					totalLag, len(parts), strings.Join(parts, ", ")))
		}
		fmt.Println()

		// Wait for next poll interval or context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Continue polling
		}
	}
}
