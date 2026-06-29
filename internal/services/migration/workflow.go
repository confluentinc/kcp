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
	"github.com/fatih/color"
	"github.com/goccy/go-yaml"
)

type MigrationWorkflow struct {
	gatewayService      gateway.Service
	clusterLinkService  clusterlink.Service
	sourceOffset        offset.Provider
	destinationOffset   offset.Provider
	lagPollInterval     time.Duration
	promotePollInterval time.Duration
	// rolloutTimeout is the deadline applied to gateway-readiness waits in
	// FenceGateway and SwitchGateway. A value of 0 means no deadline — the
	// wait runs until the operator reports ready or the user cancels.
	rolloutTimeout time.Duration
	// quiescenceInterval is the wait between two source offset snapshots when
	// detecting unrouted producers. Defaults to 10 seconds.
	quiescenceInterval time.Duration
}

func NewMigrationWorkflow(
	gatewayService gateway.Service,
	clusterLinkService clusterlink.Service,
) *MigrationWorkflow {
	return &MigrationWorkflow{
		gatewayService:      gatewayService,
		clusterLinkService:  clusterLinkService,
		lagPollInterval:     2 * time.Second,
		promotePollInterval: 5 * time.Second,
		quiescenceInterval:  10 * time.Second,
	}
}

func NewMigrationWorkflowWithOffsets(
	gatewayService gateway.Service,
	clusterLinkService clusterlink.Service,
	sourceOffset offset.Provider,
	destinationOffset offset.Provider,
) *MigrationWorkflow {
	return &MigrationWorkflow{
		gatewayService:      gatewayService,
		clusterLinkService:  clusterLinkService,
		sourceOffset:        sourceOffset,
		destinationOffset:   destinationOffset,
		lagPollInterval:     2 * time.Second,
		promotePollInterval: 5 * time.Second,
		quiescenceInterval:  10 * time.Second,
	}
}

// SetRolloutTimeout sets the deadline applied to gateway-readiness waits.
// A value of 0 means no deadline.
func (s *MigrationWorkflow) SetRolloutTimeout(d time.Duration) {
	s.rolloutTimeout = d
}

func (s *MigrationWorkflow) Initialize(
	ctx context.Context,
	config *MigrationConfig,
	clusterApiKey, clusterApiSecret string,
) error {
	slog.Debug("initializing migration", "migrationId", config.MigrationId)

	// Fetch the initial CR YAML from k8s
	initialCrYAML, err := s.gatewayService.GetGatewayYAML(ctx, config.K8sNamespace, config.InitialCrName)
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

	// If the operator opted into pausing consumer offset sync during execute,
	// validate the precondition: the cluster link must currently have
	// consumer.offset.sync.enable=true. Refuse fail-fast if the key is missing
	// or set to anything other than "true".
	//
	// Skip the check when PauseConsumerOffsetSyncFlipped is already true: kcp
	// itself set the value to "false" via DisableOffsetSync, so seeing "false"
	// here is the expected mid-flight state, not drift. This matters when init
	// ran with --skip-validate (no init-time precondition) and the first
	// execute reaches Initialize via the FSM after the bookend has already run.
	if config.PauseConsumerOffsetSync && !config.PauseConsumerOffsetSyncFlipped {
		observed, present := configs[offsetSyncEnableKey]
		switch {
		case !present:
			return fmt.Errorf("--pause-consumer-offset-sync refused: cluster link %q has no %s config key (expected %q)", config.ClusterLinkName, offsetSyncEnableKey, "true")
		case observed != "true":
			return fmt.Errorf("--pause-consumer-offset-sync refused: cluster link %q has %s=%q (expected %q)", config.ClusterLinkName, offsetSyncEnableKey, observed, "true")
		}
		fmt.Printf("   %s Cluster link %s=true (pause-on-execute intent recorded)\n", color.GreenString("✔"), offsetSyncEnableKey)
	}

	// Update config with discovered data
	config.ClusterLinkTopics = clusterLinkTopics

	// Defensive guard: never overwrite the pre-disable snapshot once the
	// bookend has flipped consumer.offset.sync.enable=false. If Initialize
	// were ever called after DisableOffsetSync ran (today blocked at the CLI
	// by --skip-validate / --pause-consumer-offset-sync mutual exclusion in
	// cmd/migration/init), `configs` would reflect the post-disable live
	// state and clobber the snapshot RestoreOffsetSync needs to diff against
	// — silently leaving the cluster link disabled. Keep the existing
	// snapshot in that case.
	if !config.PauseConsumerOffsetSyncFlipped {
		config.ClusterLinkConfigs = configs
	}

	slog.Debug("migration initialized successfully")
	return nil
}

// CheckLags polls source and destination offsets until lag is below threshold
func (s *MigrationWorkflow) CheckLags(
	ctx context.Context,
	config *MigrationConfig,
	lagThreshold int64,
	clusterApiKey, clusterApiSecret string,
) error {
	if s.sourceOffset == nil || s.destinationOffset == nil {
		return fmt.Errorf("source and destination offset services are required")
	}

	fmt.Printf("\n%s Checking replication lag across %s (threshold: %s)\n\n",
		color.CyanString("⏳"),
		color.CyanString("%d topics", len(config.Topics)),
		color.YellowString("%d", lagThreshold))

	if len(config.Topics) == 0 {
		fmt.Printf("%s No topics to check\n", color.GreenString("✔"))
		return nil
	}

	ticker := time.NewTicker(s.lagPollInterval)
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

// FenceGateway applies the fenced gateway CR YAML to block traffic and waits
// for the Confluent operator to report the gateway as Ready at the new spec
// generation. The wait runs without a deadline by default — the operator
// drives convergence and the user can Ctrl-C if a rollout wedges. An optional
// per-workflow rolloutTimeout caps the wait when set (via SetRolloutTimeout).
func (s *MigrationWorkflow) FenceGateway(ctx context.Context, config *MigrationConfig) error {
	slog.Debug("fencing gateway", "gateway", config.InitialCrName, "namespace", config.K8sNamespace)

	if err := s.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.InitialCrName, config.FencedCrYAML); err != nil {
		return fmt.Errorf("failed to apply fenced gateway CR: %w", err)
	}
	slog.Debug("fenced gateway CR applied")
	fmt.Printf("   %s Fenced gateway CR applied\n", color.GreenString("✔"))

	fmt.Printf("   ↳ Waiting for gateway readiness...\n")
	slog.Debug("waiting for gateway readiness", "rolloutTimeout", s.rolloutTimeout)

	if err := s.gatewayService.WaitForGatewayReady(ctx, config.K8sNamespace, config.InitialCrName, 5*time.Second, s.rolloutTimeout, printGatewayReadinessProgress); err != nil {
		return fmt.Errorf("failed waiting for gateway readiness: %w", err)
	}

	slog.Debug("gateway fenced and ready")
	fmt.Printf("   %s Gateway fenced and ready\n", color.GreenString("✔"))
	return nil
}

// unfenceGateway reapplies the initial gateway CR to restore normal traffic.
// The initial CR YAML fetched from k8s contains server-managed metadata
// (managedFields, resourceVersion, status) that breaks server-side apply,
// so we strip it before applying.
func (s *MigrationWorkflow) unfenceGateway(ctx context.Context, config *MigrationConfig) error {
	// Parse the initial CR, strip server metadata, re-marshal
	var obj map[string]interface{}
	if err := yaml.Unmarshal(config.InitialCrYAML, &obj); err != nil {
		return fmt.Errorf("failed to parse initial CR YAML: %w", err)
	}

	// Remove server-managed fields that break re-apply
	if metadata, ok := obj["metadata"].(map[string]interface{}); ok {
		delete(metadata, "managedFields")
		delete(metadata, "resourceVersion")
		delete(metadata, "uid")
		delete(metadata, "creationTimestamp")
		delete(metadata, "generation")
	}
	delete(obj, "status")

	cleanYAML, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal cleaned initial CR YAML: %w", err)
	}

	return s.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.InitialCrName, cleanYAML)
}

// detectUnroutedProducers takes two source offset snapshots separated by the
// quiescence interval. If any partition's offset increases between snapshots,
// it means a producer is writing directly to the source cluster (bypassing the
// fenced gateway) and the migration should not proceed.
func (s *MigrationWorkflow) detectUnroutedProducers(ctx context.Context, topics []string) error {
	// Snapshot 1
	slog.Debug("taking first source offset snapshot", "topicCount", len(topics))
	snapshot1 := make(map[string]map[int32]int64, len(topics))
	for _, topic := range topics {
		offsets, err := s.sourceOffset.Get(topic)
		if err != nil {
			return fmt.Errorf("failed to get source offsets for %s: %w", topic, err)
		}
		snapshot1[topic] = offsets
	}

	// Wait, then snapshot 2
	fmt.Printf("   ↳ Monitoring source offsets for %s...\n", s.quiescenceInterval)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.quiescenceInterval):
	}

	slog.Debug("taking second source offset snapshot")
	var violations []string
	for _, topic := range topics {
		offsets, err := s.sourceOffset.Get(topic)
		if err != nil {
			return fmt.Errorf("failed to get source offsets for %s: %w", topic, err)
		}
		for p, o2 := range offsets {
			if o1, ok := snapshot1[topic][p]; ok && o2 > o1 {
				delta := o2 - o1
				rate := float64(delta) / s.quiescenceInterval.Seconds()
				violations = append(violations, fmt.Sprintf(
					"topic %s partition %d: offset %d → %d (+%d, ~%.0f msg/s)",
					topic, p, o1, o2, delta, rate))
			}
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		return fmt.Errorf("source offsets are still increasing after fencing — unrouted producer(s) detected:\n  %s\n\nThese producers are bypassing the gateway and writing directly to the source cluster.\nReconfigure them to produce through the migration gateway, then re-run 'kcp migration execute' to resume",
			strings.Join(violations, "\n  "))
	}

	return nil
}

// PromoteTopics polls offsets and promotes mirror topics that reach zero lag
func (s *MigrationWorkflow) PromoteTopics(ctx context.Context, config *MigrationConfig, clusterApiKey, clusterApiSecret string) error {
	if s.sourceOffset == nil || s.destinationOffset == nil {
		return fmt.Errorf("source and destination offset services are required")
	}

	// If enabled, verify source offsets are stable before promoting.
	// An increasing offset after fencing indicates a producer bypassing the gateway.
	if config.DetectUnroutedProducers {
		fmt.Printf("\n%s\n", color.CyanString("🔍 Checking for unrouted producers..."))
		if err := s.detectUnroutedProducers(ctx, config.Topics); err != nil {
			fmt.Printf("   %s Unrouted producers detected — removing fence to restore traffic\n",
				color.YellowString("⚠️"))
			if unfenceErr := s.unfenceGateway(ctx, config); unfenceErr != nil {
				slog.Error("❌ failed to unfence gateway after detecting unrouted producers", "error", unfenceErr)
			} else {
				fmt.Printf("   %s Gateway unfenced — traffic restored to pre-migration state\n",
					color.GreenString("✔"))
			}
			return err
		}
		fmt.Printf("   %s Source offsets stable — no unrouted producers detected\n",
			color.GreenString("✔"))
		fmt.Printf("%s\n", color.GreenString("✅ Done"))
	}

	fmt.Printf("\n%s\n", color.CyanString("📤 Promoting mirror topics..."))
	slog.Debug("topic promotion process started")

	const maxPromoteRetries = 3

	clusterLinkConfig := clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		APIKey:       clusterApiKey,
		APISecret:    clusterApiSecret,
		Topics:       config.Topics,
	}

	// Track which topics still need promotion
	remaining := make(map[string]bool)
	retryCount := make(map[string]int)
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
				"remaining", len(remaining), "pollInterval", s.promotePollInterval)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.promotePollInterval):
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
				retryCount[topic.MirrorTopicName]++
				fmt.Printf("   %s Topic %s promotion error (attempt %d/%d): %s\n",
					color.RedString("✗"), topic.MirrorTopicName, retryCount[topic.MirrorTopicName], maxPromoteRetries, topic.ErrorMessage)
				slog.Warn("topic promotion error",
					"topic", topic.MirrorTopicName,
					"errorCode", topic.ErrorCode,
					"errorMessage", topic.ErrorMessage,
					"attempt", retryCount[topic.MirrorTopicName])
				if retryCount[topic.MirrorTopicName] >= maxPromoteRetries {
					return fmt.Errorf("topic %s failed promotion after %d attempts: %s",
						topic.MirrorTopicName, maxPromoteRetries, topic.ErrorMessage)
				}
			} else {
				fmt.Printf("   %s %s promoted\n", color.GreenString("✔"), topic.MirrorTopicName)
				slog.Debug("topic promotion initiated", "topic", topic.MirrorTopicName)
				delete(remaining, topic.MirrorTopicName)
			}
		}

		slog.Debug("waiting for promotion to complete before next check", "pollInterval", s.promotePollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.promotePollInterval):
		}
	}
}

// SwitchGateway applies the switchover gateway CR YAML to point to Confluent
// Cloud and waits for the operator to report the gateway as Ready. The wait
// uses the same no-deadline-by-default behavior as FenceGateway.
func (s *MigrationWorkflow) SwitchGateway(ctx context.Context, config *MigrationConfig) error {
	slog.Debug("switching gateway", "gateway", config.InitialCrName, "namespace", config.K8sNamespace)

	if err := s.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.InitialCrName, config.SwitchoverCrYAML); err != nil {
		return fmt.Errorf("failed to apply switchover gateway CR: %w", err)
	}
	slog.Debug("switchover gateway CR applied")
	fmt.Printf("   %s Switchover gateway CR applied\n", color.GreenString("✔"))

	fmt.Printf("   ↳ Waiting for gateway readiness...\n")
	slog.Debug("waiting for gateway readiness", "rolloutTimeout", s.rolloutTimeout)

	if err := s.gatewayService.WaitForGatewayReady(ctx, config.K8sNamespace, config.InitialCrName, 5*time.Second, s.rolloutTimeout, printGatewayReadinessProgress); err != nil {
		return fmt.Errorf("failed waiting for gateway readiness: %w", err)
	}

	slog.Debug("gateway switchover complete")
	fmt.Printf("   %s Gateway switchover complete\n", color.GreenString("✔"))
	return nil
}

// printGatewayReadinessProgress renders one line per poll tick combining the
// operator-reported readiness with elapsed time and a pod-readiness snapshot.
// A no-op signal (RolloutDetected=false) is preserved from the previous
// implementation so users see "no pod restart required" when an apply did not
// trigger a rollout.
func printGatewayReadinessProgress(p gateway.GatewayReadinessProgress) {
	if !p.RolloutDetected {
		fmt.Printf("   %s No pod restart required\n", color.GreenString("✔"))
		return
	}
	if p.InitialPodCount > 0 {
		fmt.Printf("   ↳ %d/%d pods ready (elapsed %s)\n",
			p.PodsReady, p.InitialPodCount, formatElapsed(p.Elapsed))
	} else {
		fmt.Printf("   ↳ gateway reconciling (elapsed %s)\n", formatElapsed(p.Elapsed))
	}
}

// formatElapsed rounds the elapsed duration to whole seconds so the progress
// line is stable across poll ticks (sub-second jitter would churn the
// rendered string each tick).
func formatElapsed(d time.Duration) string {
	return d.Round(time.Second).String()
}
