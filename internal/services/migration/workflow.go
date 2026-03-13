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
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
)

type MigrationWorkflow struct {
	gatewayService     gateway.Service
	clusterLinkService clusterlink.Service
	sourceOffset       *offset.Service
	destinationOffset  *offset.Service
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

func NewMigrationWorkflowWithOffsets(
	gatewayService gateway.Service,
	clusterLinkService clusterlink.Service,
	sourceOffset *offset.Service,
	destinationOffset *offset.Service,
) *MigrationWorkflow {
	return &MigrationWorkflow{
		gatewayService:     gatewayService,
		clusterLinkService: clusterLinkService,
		sourceOffset:       sourceOffset,
		destinationOffset:  destinationOffset,
	}
}

func (s *MigrationWorkflow) Initialize(
	ctx context.Context,
	config *types.MigrationConfig,
	clusterApiKey, clusterApiSecret string,
) error {
	slog.Debug("initializing migration", "migrationId", config.MigrationId)

	// Fetch the initial CR YAML from k8s
	initialCrYAML, err := s.gatewayService.GetGatewayYAML(ctx, config.K8sNamespace, config.PassthroughCrName)
	if err != nil {
		return fmt.Errorf("failed to get initial CR YAML: %w", err)
	}
	config.InitialCrYAML = initialCrYAML

	// Validate all three gateway CRs are consistent
	if err := s.gatewayService.ValidateGatewayCRs(config.InitialCrYAML, config.FencedCrYAML, config.SwitchoverCrYAML); err != nil {
		return fmt.Errorf("gateway CR validation failed: %w", err)
	}
	slog.Debug("gateway CRs validated")
	fmt.Printf("   %s Gateway CRs validated\n", color.GreenString("✔"))

	// Validate cluster link and topics
	clusterLinkConfig := clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		APIKey:       clusterApiKey,
		APISecret:    clusterApiSecret,
		Topics:       config.Topics,
	}

	slog.Debug("describing cluster link", "clusterId", config.ClusterId, "clusterLinkName", config.ClusterLinkName)

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
		slog.Debug("validating topics in cluster link", "topicCount", len(config.Topics))
		if err := s.clusterLinkService.ValidateTopics(config.Topics, clusterLinkTopics); err != nil {
			return fmt.Errorf("failed to validate topics in cluster link: %w", err)
		}
	} else {
		config.Topics = clusterLinkTopics
	}
	slog.Debug("cluster link validated", "activeTopicCount", len(clusterLinkTopics))
	fmt.Printf("   %s Cluster link validated (%d mirror topics active)\n", color.GreenString("✔"), len(clusterLinkTopics))

	// Get cluster link configs
	configs, err := s.clusterLinkService.ListConfigs(ctx, clusterLinkConfig)
	if err != nil {
		return fmt.Errorf("failed to list cluster link configs: %w", err)
	}

	// Update config with discovered data
	config.InitialCrYAML = initialCrYAML
	config.ClusterLinkTopics = clusterLinkTopics
	config.ClusterLinkConfigs = configs

	slog.Debug("migration initialized successfully")
	return nil
}

// CheckLags polls source and destination offsets until lag is below threshold
func (s *MigrationWorkflow) CheckLags(
	ctx context.Context,
	config *types.MigrationConfig,
	lagThreshold int64,
	clusterApiKey, clusterApiSecret string,
) error {
	fmt.Printf("\n%s Checking replication lag across %s (threshold: %s)\n\n",
		color.CyanString("⏳"),
		color.CyanString("%d topics", len(config.Topics)),
		color.YellowString("%d", lagThreshold))

	if len(config.Topics) == 0 {
		fmt.Printf("%s No topics to check\n", color.GreenString("✔"))
		return nil
	}

	pollInterval := 2 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		allBelowThreshold := true
		topicTotalLags := make(map[string]int64)

		for _, topic := range config.Topics {
			sourceOffsets, err := s.sourceOffset.Get(topic)
			if err != nil {
				return fmt.Errorf("failed to get source offsets for %s: %w", topic, err)
			}
			destinationOffsets, err := s.destinationOffset.Get(topic)
			if err != nil {
				return fmt.Errorf("failed to get destination offsets for %s: %w", topic, err)
			}

			lag := offset.ComputeTotalLag(sourceOffsets, destinationOffsets)
			if lag > lagThreshold {
				allBelowThreshold = false
				topicTotalLags[topic] = lag
			}
		}

		if allBelowThreshold {
			fmt.Printf("\n%s All topic lags below threshold (%d)\n",
				color.GreenString("✔"),
				lagThreshold)
			return nil
		}

		lagTopics := make([]string, 0, len(topicTotalLags))
		for topic := range topicTotalLags {
			lagTopics = append(lagTopics, topic)
		}
		sort.Strings(lagTopics)

		elapsed := time.Since(startTime)

		fmt.Printf("   %s Waiting for lag to clear  %s  %s\n",
			color.YellowString("↳"),
			color.YellowString("%d/%d topics behind", len(topicTotalLags), len(config.Topics)),
			color.CyanString("elapsed %s", elapsed.Round(time.Second)))

		for _, topic := range lagTopics {
			fmt.Printf("   %s %s  %s %s\n",
				color.YellowString("↳"),
				color.WhiteString(topic),
				color.CyanString("lag:"),
				color.YellowString(formatLag64(topicTotalLags[topic])))
		}
		fmt.Printf("\n")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// formatLag64 formats an int64 with comma separators (e.g. 21655 -> "21,655")
func formatLag64(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// FenceGateway applies the fenced gateway CR YAML to block traffic
func (s *MigrationWorkflow) FenceGateway(ctx context.Context, config *types.MigrationConfig) error {
	slog.Debug("fencing gateway", "gateway", config.PassthroughCrName, "namespace", config.K8sNamespace)

	// Step 1: Capture initial pod state (BEFORE any gateway modifications)
	initialGatewayPodUIDs, err := s.gatewayService.GetGatewayPodUIDs(ctx, config.K8sNamespace, config.PassthroughCrName)
	if err != nil {
		return fmt.Errorf("failed to capture initial gateway pod state: %w", err)
	}

	// Step 2: Apply the fenced CR YAML
	if err := s.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.PassthroughCrName, config.FencedCrYAML); err != nil {
		return fmt.Errorf("failed to apply fenced gateway CR: %w", err)
	}
	slog.Debug("fenced gateway CR applied")
	fmt.Printf("   %s Fenced gateway CR applied\n", color.GreenString("✔"))

	// Step 3: Wait for gateway pods to be recycled with new configuration
	const (
		pollInterval = 5 * time.Second
		timeout      = 5 * time.Minute
	)

	initialPodCount := len(initialGatewayPodUIDs)
	fmt.Printf("   ↳ Waiting for pod rollout (0/%d pods replaced)...\n", initialPodCount)
	slog.Debug("waiting for gateway pod rollout", "timeout", timeout)

	if err := s.gatewayService.WaitForGatewayPods(ctx, config.K8sNamespace, config.PassthroughCrName, initialGatewayPodUIDs, pollInterval, timeout, printPodRolloutProgress); err != nil {
		return fmt.Errorf("failed waiting for gateway pods: %w", err)
	}

	slog.Debug("gateway fenced and ready")
	fmt.Printf("   %s All %d pods rolled out\n", color.GreenString("✔"), initialPodCount)
	return nil
}

// PromoteTopics polls offsets and promotes mirror topics that reach zero lag
func (s *MigrationWorkflow) PromoteTopics(ctx context.Context, config *types.MigrationConfig, clusterApiKey, clusterApiSecret string) error {
	slog.Debug("topic promotion process started")

	clusterLinkConfig := clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		APIKey:       clusterApiKey,
		APISecret:    clusterApiSecret,
		Topics:       config.Topics,
	}

	pollInterval := 5 * time.Second

	// Track which topics still need promotion
	remaining := make(map[string]bool)
	for _, topic := range config.Topics {
		remaining[topic] = true
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if len(remaining) == 0 {
			slog.Debug("all topics promoted")
			return nil
		}

		// Find topics at zero lag using direct offset comparison
		var topicsToPromote []string
		for topic := range remaining {
			sourceOffsets, err := s.sourceOffset.Get(topic)
			if err != nil {
				return fmt.Errorf("failed to get source offsets for %s: %w", topic, err)
			}
			destinationOffsets, err := s.destinationOffset.Get(topic)
			if err != nil {
				return fmt.Errorf("failed to get destination offsets for %s: %w", topic, err)
			}

			lag := offset.ComputeTotalLag(sourceOffsets, destinationOffsets)
			if lag == 0 {
				topicsToPromote = append(topicsToPromote, topic)
			}
		}
		sort.Strings(topicsToPromote)

		if len(topicsToPromote) == 0 {
			fmt.Printf("   ↳ Waiting for lag to reach zero (%d topics remaining)...\n",
				len(remaining))
			slog.Debug("no topics at zero lag yet, waiting",
				"remaining", len(remaining), "pollInterval", pollInterval)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pollInterval):
				continue
			}
		}

		// Promote topics confirmed at zero lag
		fmt.Printf("   %s %s confirmed at zero lag\n",
			color.GreenString("✔"),
			color.WhiteString("%d/%d topics", len(topicsToPromote), len(remaining)))
		for _, topic := range topicsToPromote {
			fmt.Printf("   %s %s  %s %s\n",
				color.GreenString("↳"),
				color.WhiteString(topic),
				color.CyanString("lag:"),
				color.GreenString("0"))
		}
		fmt.Printf("   ↳ Promoting %d mirror topics...\n", len(topicsToPromote))
		slog.Debug("promoting mirror topics", "topicCount", len(topicsToPromote), "topics", topicsToPromote)

		promoteResponse, err := s.clusterLinkService.PromoteMirrorTopics(ctx, clusterLinkConfig, topicsToPromote)
		if err != nil {
			return fmt.Errorf("failed to promote mirror topics: %w", err)
		}

		for _, topic := range promoteResponse.Data {
			if topic.ErrorCode != 0 {
				fmt.Printf("   %s Topic %s promotion error: %s\n",
					color.RedString("✗"), topic.MirrorTopicName, topic.ErrorMessage)
				slog.Warn("topic promotion error",
					"topic", topic.MirrorTopicName,
					"errorCode", topic.ErrorCode,
					"errorMessage", topic.ErrorMessage)
			} else {
				fmt.Printf("   %s %s promoted\n", color.GreenString("✔"), topic.MirrorTopicName)
				slog.Debug("topic promotion initiated", "topic", topic.MirrorTopicName)
				delete(remaining, topic.MirrorTopicName)
			}
		}

		slog.Debug("waiting for promotion to complete before next check", "pollInterval", pollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// SwitchGateway applies the switchover gateway CR YAML to point to Confluent Cloud
func (s *MigrationWorkflow) SwitchGateway(ctx context.Context, config *types.MigrationConfig) error {
	slog.Debug("switching gateway", "gateway", config.PassthroughCrName, "namespace", config.K8sNamespace)

	// Step 1: Capture initial pod state (BEFORE any gateway modifications)
	initialGatewayPodUIDs, err := s.gatewayService.GetGatewayPodUIDs(ctx, config.K8sNamespace, config.PassthroughCrName)
	if err != nil {
		return fmt.Errorf("failed to capture initial gateway pod state: %w", err)
	}

	// Step 2: Apply the switchover CR YAML
	if err := s.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.PassthroughCrName, config.SwitchoverCrYAML); err != nil {
		return fmt.Errorf("failed to apply switchover gateway CR: %w", err)
	}
	slog.Debug("switchover gateway CR applied")
	fmt.Printf("   %s Switchover gateway CR applied\n", color.GreenString("✔"))

	// Step 3: Wait for gateway pods to be recycled with new configuration
	const (
		pollInterval = 5 * time.Second
		timeout      = 5 * time.Minute
	)

	initialPodCount := len(initialGatewayPodUIDs)
	fmt.Printf("   ↳ Waiting for pod rollout (0/%d pods replaced)...\n", initialPodCount)
	slog.Debug("waiting for gateway pod rollout", "timeout", timeout)

	if err := s.gatewayService.WaitForGatewayPods(ctx, config.K8sNamespace, config.PassthroughCrName, initialGatewayPodUIDs, pollInterval, timeout, printPodRolloutProgress); err != nil {
		return fmt.Errorf("failed waiting for gateway pods: %w", err)
	}

	slog.Debug("gateway switchover complete")
	fmt.Printf("   %s All %d pods rolled out\n", color.GreenString("✔"), initialPodCount)
	return nil
}

func printPodRolloutProgress(p gateway.PodRolloutProgress) {
	if !p.RolloutDetected {
		fmt.Printf("   %s No pod restart required\n", color.GreenString("✔"))
		return
	}
	if p.NewPodsReady == p.InitialPodCount && p.OldPodsRemaining > 0 {
		fmt.Printf("   ↳ %d/%d new pods ready, waiting for existing pods to terminate...\n",
			p.NewPodsReady, p.InitialPodCount)
	} else {
		fmt.Printf("   ↳ %d/%d new pods ready...\n",
			p.NewPodsReady, p.InitialPodCount)
	}
}
