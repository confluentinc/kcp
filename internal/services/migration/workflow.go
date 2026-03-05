package migration

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
	"github.com/goccy/go-yaml"
)

type MigrationWorkflow struct {
	gatewayService     gateway.Service
	clusterLinkService clusterlink.Service
}

func NewMigrationWorkflow(
	gatewayService gateway.Service,
	clusterLinkService clusterlink.Service,
) *MigrationWorkflow {
	return &MigrationWorkflow{
		gatewayService:     gatewayService,
		clusterLinkService: clusterLinkService,
	}
}

func (s *MigrationWorkflow) Initialize(
	ctx context.Context,
	config *types.MigrationConfig,
	clusterApiKey, clusterApiSecret string,
) error {
	slog.Info("initializing migration", "migrationId", config.MigrationId)

	// Validate YAML files are parseable
	if err := validateYAML(config.FencedCrYAML, "fenced CR"); err != nil {
		return err
	}
	if err := validateYAML(config.SwitchoverCrYAML, "switchover CR"); err != nil {
		return err
	}

	// Fetch and store the initial CR YAML from k8s
	initialCrYAML, err := s.gatewayService.GetGatewayYAML(ctx, config.K8sNamespace, config.PassthroughCrName)
	if err != nil {
		return fmt.Errorf("failed to get initial CR YAML: %w", err)
	}
	config.InitialCrYAML = initialCrYAML

	// TODO: now we have all 3 yamls - we can do some additional validation before proceeding


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
	config.InitialCrYAML = initialCrYAML
	config.ClusterLinkTopics = clusterLinkTopics
	config.ClusterLinkConfigs = configs

	slog.Info("migration initialized successfully")
	return nil
}

func validateYAML(data []byte, name string) error {
	var out any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("%s YAML is not valid: %w", name, err)
	}
	return nil
}

// CheckLags polls mirror topics until lag is below threshold
func (s *MigrationWorkflow) CheckLags(
	ctx context.Context,
	config *types.MigrationConfig,
	lagThreshold int64,
	clusterApiKey, clusterApiSecret string,
) error {
	fmt.Printf("\n%s Checking mirror lag across %s (threshold: %s)\n\n",
		color.CyanString("⏳"),
		color.CyanString("%d topics", len(config.Topics)),
		color.YellowString("%d", lagThreshold))

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

	startTime := time.Now()

	// Main polling loop
	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
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
			if totalLag >= int(lagThreshold) {
				allBelowThreshold = false
				topicLags[mirrorTopic.MirrorTopicName] = partitionLags
			}
		}

		// Success: all partition lags are below threshold
		if allBelowThreshold {
			fmt.Printf("\n%s All mirror topic lags below threshold (%d)\n",
				color.GreenString("✔"),
				lagThreshold)
			return nil
		}

		// Build sorted list of topic names with lag
		lagTopics := make([]string, 0, len(topicLags))
		for topic := range topicLags {
			lagTopics = append(lagTopics, topic)
		}
		sort.Strings(lagTopics)

		elapsed := time.Since(startTime)

		fmt.Printf("%s Waiting for lag to clear  %s  %s\n",
			color.YellowString("⏳"),
			color.YellowString("%d/%d topics behind", len(topicLags), len(config.Topics)),
			color.CyanString("elapsed %s", elapsed.Round(time.Second)))

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

// FenceGateway applies the fenced gateway CR YAML to block traffic
func (s *MigrationWorkflow) FenceGateway(ctx context.Context, config *types.MigrationConfig) error {
	slog.Info("🚧 Fencing gateway", "gateway", config.PassthroughCrName, "namespace", config.K8sNamespace)

	// Step 1: Capture initial pod state (BEFORE any gateway modifications)
	initialGatewayPodUIDs, err := s.gatewayService.GetGatewayPodUIDs(ctx, config.K8sNamespace, config.PassthroughCrName)
	if err != nil {
		return fmt.Errorf("failed to capture initial gateway pod state: %w", err)
	}

	// Step 2: Apply the fenced CR YAML
	if err := s.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.PassthroughCrName, config.FencedCrYAML); err != nil {
		return fmt.Errorf("failed to apply fenced gateway CR: %w", err)
	}

	slog.Info("✅ Fenced gateway CR applied")

	// Step 3: Wait for gateway pods to be recycled with new configuration
	const (
		pollInterval = 5 * time.Second
		timeout      = 5 * time.Minute
	)

	slog.Info("⏳ Waiting for gateway pod rollout", "timeout", timeout)

	if err := s.gatewayService.WaitForGatewayPods(ctx, config.K8sNamespace, config.PassthroughCrName, initialGatewayPodUIDs, pollInterval, timeout); err != nil {
		return fmt.Errorf("failed waiting for gateway pods: %w", err)
	}

	slog.Info("✅ Gateway fenced and ready")

	return nil
}

// PromoteTopics polls mirror topics and promotes those with zero lag
func (s *MigrationWorkflow) PromoteTopics(ctx context.Context, config *types.MigrationConfig, clusterApiKey, clusterApiSecret string) error {
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
func (s *MigrationWorkflow) CheckPromotionCompletion(ctx context.Context, config *types.MigrationConfig) error {
	slog.Info("waiting for topic promotion to complete.....")
	slog.Info("this can be removed")
	slog.Info("waiting for topic promotion to complete...done")
	return nil
}

// SwitchGateway applies the switchover gateway CR YAML to point to Confluent Cloud
func (s *MigrationWorkflow) SwitchGateway(ctx context.Context, config *types.MigrationConfig) error {
	slog.Info("🔄 Switching gateway", "gateway", config.PassthroughCrName, "namespace", config.K8sNamespace)

	// Step 1: Capture initial pod state (BEFORE any gateway modifications)
	initialGatewayPodUIDs, err := s.gatewayService.GetGatewayPodUIDs(ctx, config.K8sNamespace, config.PassthroughCrName)
	if err != nil {
		return fmt.Errorf("failed to capture initial gateway pod state: %w", err)
	}

	// Step 2: Apply the switchover CR YAML
	if err := s.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.PassthroughCrName, config.SwitchoverCrYAML); err != nil {
		return fmt.Errorf("failed to apply switchover gateway CR: %w", err)
	}

	slog.Info("✅ Switchover gateway CR applied")

	// Step 3: Wait for gateway pods to be recycled with new configuration
	const (
		pollInterval = 5 * time.Second
		timeout      = 5 * time.Minute
	)

	slog.Info("⏳ Waiting for gateway pod rollout", "timeout", timeout)

	if err := s.gatewayService.WaitForGatewayPods(ctx, config.K8sNamespace, config.PassthroughCrName, initialGatewayPodUIDs, pollInterval, timeout); err != nil {
		return fmt.Errorf("failed waiting for gateway pods: %w", err)
	}

	slog.Info("✅ Gateway switchover complete")

	return nil
}
