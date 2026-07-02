package cutover

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
)

type CutoverWorkflow struct {
	gatewayService      gateway.Service
	clusterLinkService  clusterlink.Service
	sourceOffset        offset.Provider
	destinationOffset   offset.Provider
	lagPollInterval     time.Duration
	promotePollInterval time.Duration
	// promoteBatchSize caps how many mirror topics are promoted per batch. A
	// value of 0 means unlimited — all zero-lag topics are promoted at once.
	// When set (>0), PromoteTopics promotes at most this many topics, waits for
	// them all to reach STOPPED, then moves on to the next batch.
	promoteBatchSize int
	// rolloutTimeout is the deadline applied to gateway-readiness waits in
	// FenceGateway and SwitchGateway. A value of 0 means no deadline — the
	// wait runs until the operator reports ready or the user cancels.
	rolloutTimeout time.Duration
}

func NewCutoverWorkflow(
	gatewayService gateway.Service,
	clusterLinkService clusterlink.Service,
) *CutoverWorkflow {
	return &CutoverWorkflow{
		gatewayService:      gatewayService,
		clusterLinkService:  clusterLinkService,
		lagPollInterval:     2 * time.Second,
		promotePollInterval: 5 * time.Second,
	}
}

func NewCutoverWorkflowWithOffsets(
	gatewayService gateway.Service,
	clusterLinkService clusterlink.Service,
	sourceOffset offset.Provider,
	destinationOffset offset.Provider,
) *CutoverWorkflow {
	return &CutoverWorkflow{
		gatewayService:      gatewayService,
		clusterLinkService:  clusterLinkService,
		sourceOffset:        sourceOffset,
		destinationOffset:   destinationOffset,
		lagPollInterval:     2 * time.Second,
		promotePollInterval: 5 * time.Second,
	}
}

// SetRolloutTimeout sets the deadline applied to gateway-readiness waits.
// A value of 0 means no deadline.
func (s *CutoverWorkflow) SetRolloutTimeout(d time.Duration) {
	s.rolloutTimeout = d
}

// SetPromoteBatchSize caps how many mirror topics are promoted per batch during
// PromoteTopics. A value of 0 (the default) means unlimited — all zero-lag
// topics are promoted at once. When set (>0), each batch is promoted and fully
// confirmed STOPPED before the next batch is submitted.
func (s *CutoverWorkflow) SetPromoteBatchSize(n int) {
	s.promoteBatchSize = n
}

func (s *CutoverWorkflow) Initialize(
	ctx context.Context,
	config *CutoverConfig,
	clusterApiKey, clusterApiSecret string,
) error {
	slog.Debug("initializing cutover", "cutoverId", config.CutoverId)

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
	// cmd/cutover/init), `configs` would reflect the post-disable live
	// state and clobber the snapshot RestoreOffsetSync needs to diff against
	// — silently leaving the cluster link disabled. Keep the existing
	// snapshot in that case.
	if !config.PauseConsumerOffsetSyncFlipped {
		config.ClusterLinkConfigs = configs
	}

	slog.Debug("cutover initialized successfully")
	return nil
}

// CheckLags polls source and destination offsets until lag is below threshold
func (s *CutoverWorkflow) CheckLags(
	ctx context.Context,
	config *CutoverConfig,
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
func (s *CutoverWorkflow) FenceGateway(ctx context.Context, config *CutoverConfig) error {
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

// PromoteTopics polls offsets and promotes mirror topics that reach zero lag
func (s *CutoverWorkflow) PromoteTopics(ctx context.Context, config *CutoverConfig, clusterApiKey, clusterApiSecret string) error {
	if s.sourceOffset == nil || s.destinationOffset == nil {
		return fmt.Errorf("source and destination offset services are required")
	}

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

	// Track which topics still need to reach the terminal STOPPED state.
	// `awaitingStop` holds topics whose promote request was accepted
	// (error_code 0) but which have not yet been confirmed STOPPED via
	// ListMirrorTopics — a promote is fire-and-forget, so error_code 0 only
	// means the request was enqueued, not that mirroring has actually stopped.
	remaining := make(map[string]bool)
	retryCount := make(map[string]int)
	awaitingStop := make(map[string]bool)
	for _, topic := range config.Topics {
		remaining[topic] = true
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Confirm accepted promotions have actually reached STOPPED. Until a
		// topic is verified STOPPED it stays in `remaining`, which keeps the
		// workflow in the promote phase and blocks the gateway switchover.
		if len(awaitingStop) > 0 {
			mirrorTopics, err := s.clusterLinkService.ListMirrorTopics(ctx, clusterLinkConfig)
			if err != nil {
				return fmt.Errorf("failed to verify mirror topic status: %w", err)
			}
			statusByTopic := make(map[string]string, len(mirrorTopics))
			for _, mt := range mirrorTopics {
				statusByTopic[mt.MirrorTopicName] = mt.MirrorStatus
			}
			for topic := range awaitingStop {
				status := statusByTopic[topic]
				if status == clusterlink.MirrorStatusStopped {
					fmt.Printf("   %s %s stopped\n", color.GreenString("✔"), topic)
					slog.Debug("mirror topic promotion confirmed stopped", "topic", topic)
					delete(awaitingStop, topic)
					delete(remaining, topic)
				} else {
					slog.Debug("mirror topic promotion still pending",
						"topic", topic, "status", status)
				}
			}
		}

		if len(remaining) == 0 {
			slog.Debug("all topics promoted and confirmed stopped")
			return nil
		}

		// In batch mode, don't start a new batch until the current one has
		// fully drained to STOPPED — this makes each batch synchronous.
		if s.promoteBatchSize > 0 && len(awaitingStop) > 0 {
			fmt.Printf("   ↳ Waiting for current batch of %d topic(s) to reach STOPPED...\n",
				len(awaitingStop))
			slog.Debug("batch in flight, waiting for STOPPED before next batch",
				"awaitingStop", len(awaitingStop), "pollInterval", s.promotePollInterval)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.promotePollInterval):
				continue
			}
		}

		// Find topics at zero lag that still need a promote request. Topics
		// already accepted (awaiting STOPPED confirmation) are skipped so we
		// don't re-promote them.
		var topicsToPromote []string
		for topic := range remaining {
			if awaitingStop[topic] {
				continue
			}
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

		// Cap the batch when a promote batch size is configured.
		if s.promoteBatchSize > 0 && len(topicsToPromote) > s.promoteBatchSize {
			topicsToPromote = topicsToPromote[:s.promoteBatchSize]
		}

		if len(topicsToPromote) == 0 {
			if len(awaitingStop) > 0 {
				fmt.Printf("   ↳ Waiting for %d promoted topic(s) to reach STOPPED...\n",
					len(awaitingStop))
				slog.Debug("waiting for accepted promotions to reach STOPPED",
					"awaitingStop", len(awaitingStop), "pollInterval", s.promotePollInterval)
			} else {
				fmt.Printf("   ↳ Waiting for lag to reach zero (%d topics remaining)...\n",
					len(remaining))
				slog.Debug("no topics at zero lag yet, waiting",
					"remaining", len(remaining), "pollInterval", s.promotePollInterval)
			}
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
				fmt.Printf("   %s %s promotion accepted (awaiting STOPPED)\n", color.GreenString("↳"), topic.MirrorTopicName)
				slog.Debug("topic promotion accepted, awaiting stopped confirmation", "topic", topic.MirrorTopicName)
				awaitingStop[topic.MirrorTopicName] = true
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
func (s *CutoverWorkflow) SwitchGateway(ctx context.Context, config *CutoverConfig) error {
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
