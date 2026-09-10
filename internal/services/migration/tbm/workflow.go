package tbm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/fatih/color"
)

// maxConsecutiveSweepFailures is how many offset sweeps in a row may fail
// before WaitForLags aborts. Mirrors migration.maxConsecutiveSweepFailures —
// duplicated, not shared: the two packages intentionally have no
// cross-imports (see the tbm package doc comment in state.go).
const maxConsecutiveSweepFailures = 3

// TransitionSimulatedDelay is how long each still-noop action sleeps to
// simulate real execution timing, until real per-batch migration logic
// replaces it. A package variable, not a const, so tests can shrink it — see
// setFastTransitions in workflow_test.go.
var TransitionSimulatedDelay = 7 * time.Second

// TBMActions holds the business logic behind each FSM transition. Initialize,
// WaitForLags, Fence, Promote and Switch are real; only VerifyFence remains a
// noop that sleeps TransitionSimulatedDelay — cancellable via ctx, mirroring
// the wait pattern in migration.MigrationActions.CheckLags — then reports
// completion.
type TBMActions struct {
	reporter          *reporter
	sourceOffset      offset.Provider
	destinationOffset offset.Provider
	// lagPollInterval is how often WaitForLags re-sweeps offsets. A struct
	// field (not a const), so tests can shrink it — mirrors
	// migration.MigrationActions.lagPollInterval.
	lagPollInterval time.Duration

	gatewayService gateway.Service
	// gatewayCapability is how gateway transitions are verified for this run.
	// Zero value (VerifyRollout, no configId) is the safe default — see
	// migration.MigrationActions.gatewayCapability.
	gatewayCapability gateway.Capability
	// capabilityResolved is true once gatewayCapability has actually been
	// resolved against the live cluster this process — see
	// ensureGatewayCapability in gateway.go. Deliberately NOT persisted to
	// TBMConfig/the state file: a new process always starts false and
	// re-resolves fresh the first time Fence or Switch needs it, which is
	// exactly the correctness property this field exists to provide (a run
	// resuming directly at switch, with fence already done in an earlier
	// process, must not silently use an unresolved zero-value capability).
	capabilityResolved bool
	// rolloutTimeout bounds the gateway-readiness wait in Fence. 0 means no
	// deadline.
	rolloutTimeout time.Duration
	// hotReloadTimeout bounds per-pod configId verification. Unlike
	// rolloutTimeout this has a real default (gatewayHotReloadTimeout) since a
	// hot-reload moves no Kubernetes signal to wait on.
	hotReloadTimeout time.Duration

	clusterLinkService clusterlink.Service
	// promotePollInterval is how often Promote re-sweeps offsets/mirror status.
	// Mirrors migration.MigrationActions.promotePollInterval.
	promotePollInterval time.Duration
	// promoteBatchSize caps how many mirror topics are promoted per batch. A
	// value of 0 means unlimited — all zero-lag topics are promoted at once.
	// Mirrors migration.MigrationActions.promoteBatchSize.
	promoteBatchSize int
}

// NewTBMActions creates a new TBMActions. sourceOffset, destinationOffset,
// gatewayService and clusterLinkService are all required — TBM has a single
// command (execute-tbm) that always eventually reaches wait_for_lags, fence
// and promote, unlike migration's separate init-only path, so there is no
// legitimate construction path without them.
func NewTBMActions(sourceOffset, destinationOffset offset.Provider, gatewayService gateway.Service, clusterLinkService clusterlink.Service) *TBMActions {
	return &TBMActions{
		reporter:            newReporter(),
		sourceOffset:        sourceOffset,
		destinationOffset:   destinationOffset,
		lagPollInterval:     2 * time.Second,
		gatewayService:      gatewayService,
		clusterLinkService:  clusterLinkService,
		promotePollInterval: 5 * time.Second,
	}
}

// SetRolloutTimeout sets the deadline applied to gateway-readiness waits.
// A value of 0 means no deadline.
func (a *TBMActions) SetRolloutTimeout(d time.Duration) {
	a.rolloutTimeout = d
}

// SetHotReloadTimeout sets the deadline for per-pod configId verification.
// A value of 0 falls back to gateway.DefaultHotReloadTimeout.
func (a *TBMActions) SetHotReloadTimeout(d time.Duration) {
	a.hotReloadTimeout = d
}

// SetPromoteBatchSize caps how many mirror topics Promote submits per batch.
// A value of 0 (the default) means unlimited.
func (a *TBMActions) SetPromoteBatchSize(n int) {
	a.promoteBatchSize = n
}

// simulateTransition is the shared noop body the sole remaining noop action
// (verify_fence) calls.
func (a *TBMActions) simulateTransition(ctx context.Context, doneMsg string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(TransitionSimulatedDelay):
	}
	a.reporter.success("%s", doneMsg)
	return nil
}

// Initialize runs the initialize transition: validates the reconcile plan
// migplan.Reconcile already computed live (before Execute was even invoked)
// and, if the plan is feasible, captures its artifacts onto config for every
// later transition to consume — never re-derived. res.Refused is checked
// before config is touched, so a cancelled transition never leaves config
// partially mutated. There is no simulated delay here: the expensive work
// (contacting source/target/gateway/cluster-link) already happened producing
// res; this step is pure validate-and-copy.
func (a *TBMActions) Initialize(ctx context.Context, config *TBMConfig, res *migplan.Result) error {
	if res.Refused {
		return fmt.Errorf("reconcile plan refused:\n%s", strings.Join(res.Reasons, "\n"))
	}

	config.Topics = res.Topics
	config.FenceYAML = res.FenceYAML
	config.SwitchoverYAML = res.SwitchoverYAML
	config.GatewayYAML = res.GatewayYAML
	config.Route = res.Route

	a.reporter.success("TBM migration initialized (%d topic(s) in plan)", len(res.Topics))
	return nil
}

// WaitForLags runs the wait_for_lags transition: polls source and destination
// offsets for config.Topics until every topic's total lag is at or below
// lagThreshold. Mirrors migration.MigrationActions.CheckLags.
func (a *TBMActions) WaitForLags(ctx context.Context, config *TBMConfig, lagThreshold int64) error {
	a.reporter.blank()
	a.reporter.line(fmt.Sprintf("%s Checking replication lag across %s (threshold: %s)",
		color.CyanString("⏳"),
		color.CyanString("%d topics", len(config.Topics)),
		color.YellowString("%d", lagThreshold)))
	a.reporter.blank()

	if len(config.Topics) == 0 {
		a.reporter.line(fmt.Sprintf("%s No topics to check", color.GreenString("✔")))
		return nil
	}

	ticker := time.NewTicker(a.lagPollInterval)
	defer ticker.Stop()

	startTime := time.Now()
	sweepFailures := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		allBelowThreshold := true
		topicTotalLags := make(map[string]int64)

		sourceOffsets, destinationOffsets, err := a.fetchSourceAndDestinationOffsets(ctx, config.Topics)
		if err != nil {
			sweepFailures++
			if sweepFailures >= maxConsecutiveSweepFailures {
				return fmt.Errorf("offset sweep failed %d consecutive times: %w", sweepFailures, err)
			}
			slog.Warn("⚠️ offset sweep failed, retrying on next tick",
				"attempt", sweepFailures, "maxAttempts", maxConsecutiveSweepFailures, "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(a.lagPollInterval):
			}
			continue
		}
		sweepFailures = 0

		for _, topic := range config.Topics {
			lag := offset.ComputeTotalLag(sourceOffsets[topic], destinationOffsets[topic])
			if lag > lagThreshold {
				allBelowThreshold = false
				topicTotalLags[topic] = lag
			}
		}

		if allBelowThreshold {
			a.reporter.blank()
			a.reporter.line(fmt.Sprintf("%s All topic lags below threshold (%d)",
				color.GreenString("✔"),
				lagThreshold))
			return nil
		}

		lagTopics := make([]string, 0, len(topicTotalLags))
		for topic := range topicTotalLags {
			lagTopics = append(lagTopics, topic)
		}
		sort.Strings(lagTopics)

		elapsed := time.Since(startTime)

		a.reporter.line(fmt.Sprintf("   %s Waiting for lag to clear  %s  %s",
			color.YellowString("↳"),
			color.YellowString("%d/%d topics behind", len(topicTotalLags), len(config.Topics)),
			color.CyanString("elapsed %s", elapsed.Round(time.Second))))

		for _, topic := range lagTopics {
			a.reporter.line(fmt.Sprintf("   %s %s  %s %s",
				color.YellowString("↳"),
				color.WhiteString(topic),
				color.CyanString("lag:"),
				color.YellowString(formatLag64(topicTotalLags[topic]))))
		}
		a.reporter.blank()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// fetchSourceAndDestinationOffsets sweeps both clusters' offsets for the
// given topics concurrently — the clusters are independent, so a poll tick
// pays the slower of the two sweeps rather than their sum. Mirrors
// migration.MigrationActions.fetchSourceAndDestinationOffsets.
func (a *TBMActions) fetchSourceAndDestinationOffsets(ctx context.Context, topics []string) (map[string]map[int32]int64, map[string]map[int32]int64, error) {
	var (
		wg                 sync.WaitGroup
		source, dest       map[string]map[int32]int64
		sourceErr, destErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		source, sourceErr = a.sourceOffset.GetMany(ctx, topics)
	}()
	go func() {
		defer wg.Done()
		dest, destErr = a.destinationOffset.GetMany(ctx, topics)
	}()
	wg.Wait()

	var errs []error
	if sourceErr != nil {
		errs = append(errs, fmt.Errorf("failed to get source offsets: %w", sourceErr))
	}
	if destErr != nil {
		errs = append(errs, fmt.Errorf("failed to get destination offsets: %w", destErr))
	}
	if len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}
	return source, dest, nil
}

// formatLag64 formats an int64 with comma separators (e.g. 21655 -> "21,655").
// Mirrors migration.formatLag64.
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

// Fence runs the fence transition: resolves how gateway transitions will be
// verified on the live cluster, proves hot-reload actually works if the
// gateway claims to support it, derives the fenced CR by replacing
// config.Route's rules with config.FenceYAML, applies it, and confirms it
// landed. Unlike migration.FenceGateway there is no pod-UID capture for
// rogue-producer detection (that is verify_fence's concern, not yet built)
// and no compensating rollback on failure — a failure here just returns an
// error and leaves the FSM at lags_ok; re-running execute-tbm retries fencing.
func (a *TBMActions) Fence(ctx context.Context, config *TBMConfig) error {
	// config.Topics is empty whenever migplan.Reconcile's Result was a
	// legitimate "nothing to migrate" outcome (Refused: false, Artifacts nil —
	// see reconcile.go: Refused() is checked first, then len(migratable)==0 is
	// a separate, distinct success path for an already-migrated/steady-state
	// batch). config.FenceYAML is then "", which deriveFencedCRYAML cannot
	// parse. Mirrors WaitForLags's identical guard.
	if len(config.Topics) == 0 {
		a.reporter.success("No topics to fence")
		return nil
	}

	if err := a.ensureGatewayCapability(ctx, config); err != nil {
		return fmt.Errorf("failed to resolve gateway capability: %w", err)
	}

	fencedCrYAML, err := deriveFencedCRYAML(config)
	if err != nil {
		return fmt.Errorf("failed to build fenced gateway CR: %w", err)
	}

	applied, err := a.applyGatewayCR(ctx, config, fencedCrYAML, "fence")
	if err != nil {
		return fmt.Errorf("failed to apply fenced gateway CR: %w", err)
	}
	a.reporter.success("Fenced gateway CR applied")

	if err := a.waitForGatewayAccepted(ctx, config, "fence"); err != nil {
		return err
	}
	if err := a.verifyGatewayTransition(ctx, config, applied, "fence"); err != nil {
		return err
	}

	a.reporter.success("Gateway fenced and ready")
	return nil
}

// VerifyFence runs the verify_fence transition.
func (a *TBMActions) VerifyFence(ctx context.Context, config *TBMConfig) error {
	return a.simulateTransition(ctx, "Fence verified")
}

// Promote runs the promote transition: polls source/destination offsets for
// config.Topics until each reaches exact zero lag, then promotes that
// topic's mirror, confirming it reaches the terminal STOPPED status before
// considering it done. Mirrors migration.MigrationActions.PromoteTopics.
// restAuth is per-run authentication for the cluster-link REST surface — not
// stored on TBMActions, mirroring how AAO's own PromoteTopics takes it as a
// parameter rather than construction-time state.
func (a *TBMActions) Promote(ctx context.Context, config *TBMConfig, restAuth clusterlink.Authenticator) error {
	slog.Debug("topic promotion process started")

	const maxPromoteRetries = 3

	clusterLinkConfig := clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		Auth:         restAuth,
		Topics:       config.Topics,
	}

	// Track which topics still need to reach the terminal STOPPED state.
	// awaitingStop holds topics whose promote request was accepted
	// (error_code 0) but which have not yet been confirmed STOPPED via
	// ListMirrorTopics — a promote is fire-and-forget, so error_code 0 only
	// means the request was enqueued, not that mirroring has actually stopped.
	remaining := make(map[string]bool)
	retryCount := make(map[string]int)
	awaitingStop := make(map[string]bool)
	for _, topic := range config.Topics {
		remaining[topic] = true
	}
	sweepFailures := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Confirm accepted promotions have actually reached STOPPED. Until a
		// topic is verified STOPPED it stays in remaining, which keeps the
		// workflow in the promote phase.
		if len(awaitingStop) > 0 {
			mirrorTopics, err := a.clusterLinkService.ListMirrorTopics(ctx, clusterLinkConfig)
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
					a.reporter.success("%s stopped", topic)
					slog.Debug("mirror topic promotion confirmed stopped", "topic", topic)
					delete(awaitingStop, topic)
					delete(remaining, topic)
				} else {
					slog.Debug("mirror topic promotion still pending", "topic", topic, "status", status)
				}
			}
		}

		// config.Topics is empty whenever migplan.Reconcile's Result was a
		// legitimate "nothing to migrate" outcome — remaining starts empty and
		// this returns immediately, without ever touching clusterLinkService.
		if len(remaining) == 0 {
			slog.Debug("all topics promoted and confirmed stopped")
			return nil
		}

		// In batch mode, don't start a new batch until the current one has
		// fully drained to STOPPED — this makes each batch synchronous.
		if a.promoteBatchSize > 0 && len(awaitingStop) > 0 {
			a.reporter.detail("Waiting for current batch of %d topic(s) to reach STOPPED...", len(awaitingStop))
			slog.Debug("batch in flight, waiting for STOPPED before next batch",
				"awaitingStop", len(awaitingStop), "pollInterval", a.promotePollInterval)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(a.promotePollInterval):
				continue
			}
		}

		candidates := make([]string, 0, len(remaining))
		for topic := range remaining {
			if awaitingStop[topic] {
				continue
			}
			candidates = append(candidates, topic)
		}
		sort.Strings(candidates)

		sourceOffsets, destinationOffsets, err := a.fetchSourceAndDestinationOffsets(ctx, candidates)
		if err != nil {
			sweepFailures++
			if sweepFailures >= maxConsecutiveSweepFailures {
				return fmt.Errorf("offset sweep failed %d consecutive times: %w", sweepFailures, err)
			}
			slog.Warn("⚠️ offset sweep failed, retrying on next tick",
				"attempt", sweepFailures, "maxAttempts", maxConsecutiveSweepFailures, "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(a.promotePollInterval):
			}
			continue
		}
		sweepFailures = 0

		var topicsToPromote []string
		for _, topic := range candidates {
			lag := offset.ComputeTotalLag(sourceOffsets[topic], destinationOffsets[topic])
			if lag == 0 {
				topicsToPromote = append(topicsToPromote, topic)
			}
		}

		if a.promoteBatchSize > 0 && len(topicsToPromote) > a.promoteBatchSize {
			topicsToPromote = topicsToPromote[:a.promoteBatchSize]
		}

		if len(topicsToPromote) == 0 {
			if len(awaitingStop) > 0 {
				a.reporter.detail("Waiting for %d promoted topic(s) to reach STOPPED...", len(awaitingStop))
				slog.Debug("waiting for accepted promotions to reach STOPPED",
					"awaitingStop", len(awaitingStop), "pollInterval", a.promotePollInterval)
			} else {
				a.reporter.detail("Waiting for lag to reach zero (%d topics remaining)...", len(remaining))
				slog.Debug("no topics at zero lag yet, waiting",
					"remaining", len(remaining), "pollInterval", a.promotePollInterval)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(a.promotePollInterval):
				continue
			}
		}

		a.reporter.success("%s confirmed at zero lag", color.WhiteString("%d/%d topics", len(topicsToPromote), len(remaining)))
		for _, topic := range topicsToPromote {
			a.reporter.line(fmt.Sprintf("   %s %s  %s %s",
				color.GreenString("↳"), color.WhiteString(topic), color.CyanString("lag:"), color.GreenString("0")))
		}
		a.reporter.detail("Promoting %d mirror topics...", len(topicsToPromote))
		slog.Debug("promoting mirror topics", "topicCount", len(topicsToPromote), "topics", topicsToPromote)

		promoteResponse, err := a.clusterLinkService.PromoteMirrorTopics(ctx, clusterLinkConfig, topicsToPromote)
		if err != nil {
			return fmt.Errorf("failed to promote mirror topics: %w", err)
		}

		for _, topic := range promoteResponse.Data {
			if topic.ErrorCode != 0 {
				retryCount[topic.MirrorTopicName]++
				a.reporter.line(fmt.Sprintf("   %s Topic %s promotion error (attempt %d/%d): %s",
					color.RedString("✗"), topic.MirrorTopicName, retryCount[topic.MirrorTopicName], maxPromoteRetries, topic.ErrorMessage))
				slog.Warn("topic promotion error",
					"topic", topic.MirrorTopicName, "errorCode", topic.ErrorCode,
					"errorMessage", topic.ErrorMessage, "attempt", retryCount[topic.MirrorTopicName])
				if retryCount[topic.MirrorTopicName] >= maxPromoteRetries {
					return fmt.Errorf("topic %s failed promotion after %d attempts: %s",
						topic.MirrorTopicName, maxPromoteRetries, topic.ErrorMessage)
				}
			} else {
				a.reporter.line(fmt.Sprintf("   %s %s promotion accepted (awaiting STOPPED)", color.GreenString("↳"), topic.MirrorTopicName))
				slog.Debug("topic promotion accepted, awaiting stopped confirmation", "topic", topic.MirrorTopicName)
				awaitingStop[topic.MirrorTopicName] = true
			}
		}

		slog.Debug("waiting for promotion to complete before next check", "pollInterval", a.promotePollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(a.promotePollInterval):
		}
	}
}

// Switch runs the switch transition: applies config.SwitchoverYAML — the
// migplan engine's own pre-computed artifact, captured at initialize exactly
// like FenceYAML — to config.Route's rules subtree, replacing it wholesale,
// then confirms the transition landed. Unlike migration.SwitchGateway, there
// is no field-flip to derive here: migplan already renders a full switchover
// rules block ahead of time (see deriveSwitchedCRYAML's own doc comment),
// because TBM's dynamic routes need a whole-subtree rules replacement, not a
// single streamingDomain flip on a static route. There is no pod-UID
// capture or compensating rollback on failure — a failure here just returns
// an error and leaves the FSM at promoted; re-running execute-tbm retries
// switching.
func (a *TBMActions) Switch(ctx context.Context, config *TBMConfig) error {
	// config.Topics is empty whenever migplan.Reconcile's Result was a
	// legitimate "nothing to migrate" outcome — see Fence's identical guard
	// for the full explanation. config.SwitchoverYAML is then "", which
	// deriveSwitchedCRYAML cannot parse.
	if len(config.Topics) == 0 {
		a.reporter.success("No topics to switch")
		return nil
	}

	if err := a.ensureGatewayCapability(ctx, config); err != nil {
		return fmt.Errorf("failed to resolve gateway capability: %w", err)
	}

	switchedCrYAML, err := deriveSwitchedCRYAML(config)
	if err != nil {
		return fmt.Errorf("failed to build switched gateway CR: %w", err)
	}

	applied, err := a.applyGatewayCR(ctx, config, switchedCrYAML, "switchover")
	if err != nil {
		return fmt.Errorf("failed to apply switchover gateway CR: %w", err)
	}
	a.reporter.success("Switchover gateway CR applied")

	if err := a.waitForGatewayAccepted(ctx, config, "switchover"); err != nil {
		return err
	}
	if err := a.verifyGatewayTransition(ctx, config, applied, "switchover"); err != nil {
		return err
	}

	a.reporter.success("Gateway switchover complete")
	return nil
}
