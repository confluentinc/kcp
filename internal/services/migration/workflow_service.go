package migration

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/goccy/go-yaml"
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

// FenceGateway adds fence configuration to the source route to block traffic
func (s *DefaultWorkflowService) FenceGateway(ctx context.Context, config *types.MigrationConfig) error {
	slog.Info("🚧 Fencing gateway route", "route", config.SourceRouteName, "gateway", config.GatewayCrdName)

	// Step 1: Capture initial pod state (BEFORE any gateway modifications)
	// This must be first to avoid race conditions where rollout completes before we capture UIDs
	initialGatewayPodUIDs, err := s.gatewayService.GetGatewayPodUIDs(ctx, config.GatewayNamespace, config.GatewayCrdName)
	if err != nil {
		return fmt.Errorf("failed to capture initial gateway pod state: %w", err)
	}

	// Step 2: Get the current gateway YAML to find the source route index
	gatewayYAML, err := s.gatewayService.GetGatewayYAML(ctx, config.GatewayNamespace, config.GatewayCrdName)
	if err != nil {
		return fmt.Errorf("failed to get gateway: %w", err)
	}

	var gw types.GatewayResource
	if err := yaml.Unmarshal(gatewayYAML, &gw); err != nil {
		return fmt.Errorf("failed to parse gateway YAML: %w", err)
	}

	// Step 3: Find the source route index
	routeIndex := -1
	for i, route := range gw.Spec.Routes {
		if route.Name == config.SourceRouteName {
			routeIndex = i
			break
		}
	}
	if routeIndex == -1 {
		return fmt.Errorf("source route '%s' not found in gateway routes", config.SourceRouteName)
	}

	if gw.Spec.Routes[routeIndex].Fence != nil {
		slog.Warn("⚠️  Route already fenced, replacing configuration", "route", config.SourceRouteName)
	}

	// Step 4: Build JSON patch to add fence configuration
	const (
		FenceScope        = "ALL"
		FenceErrorCode    = "BROKER_NOT_AVAILABLE"
		FenceErrorMessage = "This route is currently unavailable - all requests are blocked by me!?"
	)
	patchOps := []map[string]any{
		{
			"op":   "add",
			"path": fmt.Sprintf("/spec/routes/%d/fence", routeIndex),
			"value": map[string]any{
				"scope":        FenceScope,
				"errorCode":    FenceErrorCode,
				"errorMessage": FenceErrorMessage,
			},
		},
	}

	// Step 5: Apply the patch to fence the route
	if err := s.gatewayService.PatchGateway(ctx, config.GatewayNamespace, config.GatewayCrdName, patchOps); err != nil {
		return fmt.Errorf("failed to fence gateway route '%s': %w", config.SourceRouteName, err)
	}

	slog.Info("✅ Route fenced", "scope", FenceScope)

	// Step 6: Wait for gateway pods to be recycled with new configuration
	const (
		pollInterval = 5 * time.Second
		timeout      = 5 * time.Minute
	)

	slog.Info("⏳ Waiting for gateway pod rollout", "timeout", timeout)

	if err := s.gatewayService.WaitForGatewayPods(ctx, config.GatewayNamespace, config.GatewayCrdName, initialGatewayPodUIDs, pollInterval, timeout); err != nil {
		return fmt.Errorf("failed waiting for gateway pods: %w", err)
	}

	slog.Info("✅ Gateway fenced and ready")

	return nil
}

// PromoteTopics polls mirror topics and promotes those with zero lag
func (s *DefaultWorkflowService) PromoteTopics(ctx context.Context, config *types.MigrationConfig, clusterApiKey, clusterApiSecret string) error {
	slog.Info("topic promotion process started")

	clusterLinkConfig := clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		APIKey:       clusterApiKey,
		APISecret:    clusterApiSecret,
		Topics:       config.Topics,
	}

	pollInterval := 5 * time.Second

	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Step 1: List all mirror topics
		mirrorTopics, err := s.clusterLinkService.ListMirrorTopics(ctx, clusterLinkConfig)
		if err != nil {
			return fmt.Errorf("failed to list mirror topics: %w", err)
		}

		// no mirror topics found, promotion is complete
		if len(mirrorTopics) == 0 {
			slog.Info("no mirror topics found, promotion complete")
			return nil
		}

		// Step 2: Get active mirror topics with zero lag
		topicsToPromote := clusterlink.GetActiveTopicsWithZeroLag(mirrorTopics)

		// Check completion condition: no active topics found
		activeCount := clusterlink.CountActiveMirrorTopics(mirrorTopics)
		if activeCount == 0 {
			slog.Info("no active mirror topics remaining, promotion complete")
			return nil
		}

		// If no topics ready to promote (all have non-zero lag), wait and retry
		if len(topicsToPromote) == 0 {
			if clusterlink.HasActiveTopicsWithNonZeroLag(mirrorTopics) {
				slog.Info("active topics found but lag is not zero, waiting before retry",
					"activeTopics", activeCount,
					"pollInterval", pollInterval)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(pollInterval):
					continue
				}
			}
			// No active topics with any lag status - we're done
			slog.Info("promotion complete, no more topics to promote")
			return nil
		}

		// Step 3: Promote topics that are active and have zero lag
		slog.Info("promoting mirror topics", "topicCount", len(topicsToPromote), "topics", topicsToPromote)

		promoteResponse, err := s.clusterLinkService.PromoteMirrorTopics(ctx, clusterLinkConfig, topicsToPromote)
		if err != nil {
			return fmt.Errorf("failed to promote mirror topics: %w", err)
		}

		// Log any promotion errors
		for _, topic := range promoteResponse.Data {
			if topic.ErrorCode != 0 {
				slog.Warn("topic promotion error",
					"topic", topic.MirrorTopicName,
					"errorCode", topic.ErrorCode,
					"errorMessage", topic.ErrorMessage)
			} else {
				slog.Info("topic promotion initiated", "topic", topic.MirrorTopicName)
			}
		}

		// Wait before checking again
		slog.Info("waiting for promotion to complete before next check", "pollInterval", pollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
			// Continue to next iteration
		}
	}
}

// CheckPromotionCompletion is a placeholder for promotion completion verification
func (s *DefaultWorkflowService) CheckPromotionCompletion(ctx context.Context, config *types.MigrationConfig) error {
	slog.Info("waiting for topic promotion to complete.....")
	slog.Info("this can be removed")
	slog.Info("waiting for topic promotion to complete...done")
	return nil
}

// SwitchGateway switches the gateway to point to Confluent Cloud
func (s *DefaultWorkflowService) SwitchGateway(ctx context.Context, config *types.MigrationConfig) error {
	slog.Info("🔄 Switching gateway to Confluent Cloud", "destination", config.DestinationName)

	// Step 1: Capture initial pod state (BEFORE any gateway modifications)
	// This must be first to avoid race conditions where rollout completes before we capture UIDs
	initialGatewayPodUIDs, err := s.gatewayService.GetGatewayPodUIDs(ctx, config.GatewayNamespace, config.GatewayCrdName)
	if err != nil {
		return fmt.Errorf("failed to capture initial gateway pod state: %w", err)
	}

	// Step 2: Get the current gateway to find the source route index
	gatewayYAML, err := s.gatewayService.GetGatewayYAML(ctx, config.GatewayNamespace, config.GatewayCrdName)
	if err != nil {
		return fmt.Errorf("failed to get gateway: %w", err)
	}

	var gw types.GatewayResource
	if err := yaml.Unmarshal(gatewayYAML, &gw); err != nil {
		return fmt.Errorf("failed to parse gateway YAML: %w", err)
	}

	// Step 3: Find the source route index
	routeIndex := -1
	for i, route := range gw.Spec.Routes {
		if route.Name == config.SourceRouteName {
			routeIndex = i
			break
		}
	}
	if routeIndex == -1 {
		return fmt.Errorf("source route '%s' not found in gateway routes", config.SourceRouteName)
	}

	// Step 4: Build JSON patch to add streaming domain and replace route
	patchOps := []map[string]any{
		// Add the new streaming domain
		{
			"op":   "add",
			"path": "/spec/streamingDomains/-",
			"value": map[string]any{
				"name": "confluent-cloud-plain",
				"type": "kafka",
				"kafkaCluster": map[string]any{
					"bootstrapServers": []map[string]any{
						{
							"id":       "SASL_PLAIN",
							"endpoint": "SASL_SSL://" + config.CCBootstrapEndpoint,
							"tls": map[string]any{
								"secretRef": "confluent-tls",
							},
						},
					},
				},
			},
		},
		// Replace the source route to point to the new streaming domain
		{
			"op":   "replace",
			"path": fmt.Sprintf("/spec/routes/%d", routeIndex),
			"value": map[string]any{
				"name":     "swap-msk-unauth-to-cc-route",
				"endpoint": config.LoadBalancerEndpoint,
				"brokerIdentificationStrategy": map[string]any{
					"type":    "host",
					"pattern": fmt.Sprintf("broker$(nodeId).%s", config.LoadBalancerEndpoint),
				},
				"streamingDomain": map[string]any{
					"name":              "confluent-cloud-plain",
					"bootstrapServerId": "SASL_PLAIN",
				},
				"security": map[string]any{
					"auth":        "swap",
					"secretStore": "file-store",
					"client": map[string]any{
						"authentication": map[string]any{
							"type": "none",
						},
						"tls": map[string]any{
							"secretRef": "gateway-tls",
						},
					},
					"cluster": map[string]any{
						"authentication": map[string]any{
							"type": "plain",
							"jaasConfigPassThrough": map[string]any{
								"secretRef": "plain-jaas",
							},
						},
					},
				},
			},
		},
	}

	// Step 5: Apply the patch atomically (both operations together)
	if err := s.gatewayService.PatchGateway(ctx, config.GatewayNamespace, config.GatewayCrdName, patchOps); err != nil {
		return fmt.Errorf("failed to patch gateway: %w", err)
	}

	slog.Info("✅ Gateway config updated")

	// Step 6: Wait for gateway pods to be recycled with new configuration
	const (
		pollInterval = 5 * time.Second
		timeout      = 5 * time.Minute
	)

	slog.Info("⏳ Waiting for gateway pod rollout", "timeout", timeout)

	if err := s.gatewayService.WaitForGatewayPods(ctx, config.GatewayNamespace, config.GatewayCrdName, initialGatewayPodUIDs, pollInterval, timeout); err != nil {
		return fmt.Errorf("failed waiting for gateway pods: %w", err)
	}

	slog.Info("✅ Gateway switchover complete")

	return nil
}
