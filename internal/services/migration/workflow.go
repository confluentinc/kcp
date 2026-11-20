package migration

import (
	"context"
	"errors"
	"fmt"
	"github.com/confluentinc/kcp/internal/services/migration/migrun"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/fatih/color"
	"go.opentelemetry.io/otel/attribute"
)

// maxConsecutiveSweepFailures is how many offset sweeps in a row may fail
// before CheckLags/PromoteTopics abort. A failed sweep is tolerated by
// waiting for the loop's next tick (the tick interval is the backoff — no
// separate schedule), so transient disruptions like leader elections and
// broker restarts ride out across ~3 ticks (+ GetMany's internal
// refresh-and-retry per sweep) while a persistent failure still surfaces
// within seconds. The counter resets on any successful sweep.
const maxConsecutiveSweepFailures = 3

type MigrationActions struct {
	// gw applies and confirms every gateway patch, and owns the bookkeeping
	// that needs, including whether the gateway changed under kcp since the plan.
	gw                  *gateway.Transitioner
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
	// promoteSubmitted is set once this run has sent a promote request: from
	// then on some topics may be promoted, so a stop must not say otherwise.
	promoteSubmitted bool
	reporter         *reporter // user-facing terminal output
}

func NewMigrationActions(
	gatewayTransitioner gateway.TransitionerFactory,
	clusterLinkService clusterlink.Service,
) *MigrationActions {
	s := &MigrationActions{
		clusterLinkService:  clusterLinkService,
		lagPollInterval:     2 * time.Second,
		promotePollInterval: 5 * time.Second,
		reporter:            newReporter(),
	}
	// The reporter is read at call time: tests replace s.reporter after construction.
	s.gw = gatewayTransitioner(func() gateway.Reporter { return s.reporter })
	return s
}

// validate reports the dependencies a migration cannot run without. The
// offset providers are not checked here: the steps that read them do.
func (s *MigrationActions) validate() error {
	var missing []string
	if s.clusterLinkService == nil {
		missing = append(missing, "ClusterLinkService")
	}
	if s.gw == nil || !s.gw.HasClient() {
		missing = append(missing, "GatewayService")
	}
	if len(missing) > 0 {
		return fmt.Errorf("the migration is missing dependencies: %s", strings.Join(missing, ", "))
	}
	return nil
}

func NewMigrationActionsWithOffsets(
	gatewayTransitioner gateway.TransitionerFactory,
	clusterLinkService clusterlink.Service,
	sourceOffset offset.Provider,
	destinationOffset offset.Provider,
) *MigrationActions {
	s := NewMigrationActions(gatewayTransitioner, clusterLinkService)
	s.sourceOffset = sourceOffset
	s.destinationOffset = destinationOffset
	return s
}

// target names the gateway this run drives and the fence and switch edits it
// makes; the gateway package derives whatever verification needs from them.
func (s *MigrationActions) target(config *migrun.MigrationConfig) gateway.Target {
	return gateway.Target{
		Namespace:        config.K8sNamespace,
		Name:             config.InitialCrName,
		StartRouteFenced: config.FencedAtStart,
		GatewayCR:        []byte(config.GatewayYAML),
		Route:            config.Route,
		Fence:            fenceChange(config),
		Switch:           switchChange(config),
		PlanBaseline:     config.PlanBaseline,
	}
}

// SetPromoteBatchSize caps how many mirror topics are promoted per batch during
// PromoteTopics. A value of 0 (the default) means unlimited — all zero-lag
// topics are promoted at once. When set (>0), each batch is promoted and fully
// confirmed STOPPED before the next batch is submitted.
func (s *MigrationActions) SetPromoteBatchSize(n int) {
	s.promoteBatchSize = n
}

// Initialize captures the migplan-derived artifacts onto config. Mirrors
// TBMActions.Initialize's shape (check res.Refused, copy fields) — everything
// migplan.Reconcile already decided (staged-auth/secret existence, cluster-link
// topic classification, whether an offset-sync restore is owed) is NOT
// re-checked here.
func (s *MigrationActions) Initialize(
	ctx context.Context,
	config *migrun.MigrationConfig,
	restAuth clusterlink.Authenticator,
	res *migplan.Result,
) error {
	slog.Debug("initializing migration", "migrationId", config.MigrationId)

	if res == nil {
		return errors.New("no reconcile result to initialize the migration from")
	}
	if res.Refused {
		return fmt.Errorf("reconcile plan refused:\n%s", strings.Join(res.Reasons, "\n"))
	}

	config.Topics = res.PromoteTopics
	config.AwaitStopped = res.AwaitStopped
	config.FenceYAML = res.FenceYAML
	config.SwitchoverYAML = res.SwitchoverYAML
	config.RestoreOffsetSync = res.RestoreOffsetSync
	config.RollbackFenceYAML = res.RollbackFenceYAML
	config.RollbackAllowed = res.RollbackAllowed
	config.FencedAtStart = res.FencedAtStart
	config.MigrateTopics = res.MigrateTopics
	config.GatewayYAML = res.GatewayYAML
	config.Route = res.Route
	config.PlanBaseline = res.PlanBaseline
	s.reporter.Success("Reconcile plan accepted (%d topic(s) in plan)", len(res.PromoteTopics))

	// How gateway transitions are verified is NOT decided here: there is no
	// separate advisory moment to decide it for anymore (execute is the only
	// command), and config.FenceYAML/SwitchoverYAML were only just set above in
	// the same process. The Transitioner decides lazily, once, on the first
	// gateway patch later in this same Execute() call.

	slog.Debug("migration initialized successfully")
	return nil
}

// CheckLags polls source and destination offsets until lag is below threshold
func (s *MigrationActions) CheckLags(
	ctx context.Context,
	config *migrun.MigrationConfig,
	lagThreshold int64,
	restAuth clusterlink.Authenticator,
) error {
	if s.sourceOffset == nil || s.destinationOffset == nil {
		return fmt.Errorf("source and destination offset services are required")
	}

	s.reporter.blank()
	s.reporter.line(fmt.Sprintf("Checking replication lag across %s (threshold: %s)",
		color.CyanString("%d topics", len(config.Topics)),
		color.YellowString("%d", lagThreshold)))
	s.reporter.blank()

	if len(config.Topics) == 0 {
		s.reporter.line(fmt.Sprintf("%s No topics to check", color.GreenString("[OK]")))
		tracing.MarkSkipped(ctx, "no_topics")
		return nil
	}

	polls, behind := 0, 0
	defer func() {
		tracing.SetAttributes(ctx, attribute.Int("kcp.polls", polls), attribute.Int("kcp.topics_behind", behind))
	}()

	ticker := time.NewTicker(s.lagPollInterval)
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

		polls++
		sourceOffsets, destinationOffsets, err := s.fetchSourceAndDestinationOffsets(ctx, config.Topics)
		if err != nil {
			sweepFailures++
			if sweepFailures >= maxConsecutiveSweepFailures {
				return fmt.Errorf("offset sweep failed %d consecutive times: %w", sweepFailures, err)
			}
			slog.Warn("offset sweep failed, retrying on next tick",
				"attempt", sweepFailures, "maxAttempts", maxConsecutiveSweepFailures, "error", err)
			// Deliberately not ticker.C: a slow failing sweep can outlast the
			// tick interval, leaving a tick buffered in the ticker's channel —
			// receiving that would retry immediately with zero backoff. A fresh
			// timer guarantees a full interval's pause between failed sweeps.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.lagPollInterval):
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

		behind = len(topicTotalLags)

		if allBelowThreshold {
			s.reporter.blank()
			s.reporter.line(fmt.Sprintf("%s All topic lags below threshold (%d)",
				color.GreenString("[OK]"),
				lagThreshold))
			return nil
		}

		lagTopics := make([]string, 0, len(topicTotalLags))
		for topic := range topicTotalLags {
			lagTopics = append(lagTopics, topic)
		}
		sort.Strings(lagTopics)

		elapsed := time.Since(startTime)

		s.reporter.line(fmt.Sprintf("   %s Waiting for lag to clear  %s  %s",
			color.YellowString("↳"),
			color.YellowString("%d/%d topics behind", len(topicTotalLags), len(config.Topics)),
			color.CyanString("elapsed %s", elapsed.Round(time.Second))))

		for _, topic := range lagTopics {
			s.reporter.line(fmt.Sprintf("   %s %s  %s %s",
				color.YellowString("↳"),
				color.WhiteString(topic),
				color.CyanString("lag:"),
				color.YellowString(offset.FormatLag64(topicTotalLags[topic]))))
		}
		s.reporter.blank()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// fetchSourceAndDestinationOffsets sweeps both clusters' offsets for the
// given topics concurrently — the clusters are independent, so a poll tick
// pays the slower of the two sweeps rather than their sum.
func (s *MigrationActions) fetchSourceAndDestinationOffsets(ctx context.Context, topics []string) (map[string]map[int32]int64, map[string]map[int32]int64, error) {
	return offset.FetchSourceAndDestinationOffsets(ctx, s.sourceOffset, s.destinationOffset, topics)
}

// FenceGateway applies the fenced gateway CR YAML to block traffic, confirms the
// gateway accepted the new spec, then waits until the change has taken effect
// (every gateway pod serves the fenced route). The wait runs without a
// deadline by
// default — the gateway drives convergence. The run's configured timeout caps
// the wait when set. Ctrl-C does
// not unwind a wedged wait: kcp installs no signal handler, so it ends the
// process with the gateway as it is (fenced, if the fence was applied) and a
// re-run continues from there. Whatever the timeout, a pod that never goes
// away is reported as an error by the gateway layer, so a lost node cannot wedge
// the wait.
func (s *MigrationActions) FenceGateway(ctx context.Context, config *migrun.MigrationConfig) error {
	slog.Debug("fencing gateway", "gateway", config.InitialCrName, "namespace", config.K8sNamespace)

	// Plan-driven no-op: reconcile emitted no fence artifact, so there is
	// nothing to fence this run. Read the plan (config.FenceYAML), never the
	// live cluster — the reconcile engine already decided. This is
	// deliberately NOT keyed on config.Topics: fence is a whole-route action
	// independent of per-topic promote status, and reconcile can return an
	// empty promote set (nothing left to promote) while still owing a fence —
	// e.g. every topic already promoted to STOPPED but the switch not yet
	// applied. Gating on Topics there would silently skip
	// the still-owed fence/switch.
	if config.FenceYAML == "" {
		s.reporter.Detail("No fence artifact in plan — nothing to fence")
		tracing.MarkSkipped(ctx, "no_fence_in_plan")
		return nil
	}

	// The fence is config.FenceYAML's fence fragment set on config.Route;
	// unfence's whole-route replace (unfenceChange) is its exact inverse,
	// restoring the route to its captured state.

	// Apply also remembers the gateway pods as this fence found them, so a
	// rollback unfence does not wait on them: they carry the pre-fence route the
	// unfence restores.
	res := s.gw.Apply(ctx, s.target(config), gateway.Transition{
		Step:    "fence",
		Label:   "fence",
		Change:  fenceChange(config),
		What:    "fenced gateway CR",
		Applied: "Fenced gateway CR applied",
	})
	// Once the patch has landed (including an ErrApplyUnverified read-back), the
	// fenced spec is live in the cluster, so every later failure leaves it there,
	// possibly holding client traffic on some or all of the gateway. Marking them all lets
	// the orchestrator restore the initial CR rather than exiting with the
	// gateway in a state kcp created and did not resolve. A patch that never
	// reached the cluster is deliberately outside the mark: nothing to undo.
	if res.Landed && res.Err != nil {
		return fmt.Errorf("%w: %w", migrun.ErrFenceUnconfirmed, res.Err)
	}
	if res.Err != nil {
		return res.Err
	}

	slog.Debug("gateway fenced and ready")
	s.reporter.Success("Gateway fenced and ready")
	return nil
}

// fenceChange is the fence edit: config.FenceYAML's static {fence: {...}}
// fragment set on config.Route's fence. This FSM only runs for static routes
// (the command layer sends a dynamic route to the TBM FSM). The write path
// derives its route patch from it, and the gateway's choice of how to verify
// the fence builds the whole fenced CR from it, so the two can never drift from each other.
func fenceChange(config *migrun.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRouteFence, Artifact: []byte(config.FenceYAML)}
}

// switchChange is the switch edit: reconcile's switched route
// (config.SwitchoverYAML) -- unfenced, bound to the target domain, carrying the
// route's pre-staged target auth -- replacing config.Route whole.
func switchChange(config *migrun.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRoute, Artifact: []byte(config.SwitchoverYAML)}
}

// unfenceChange is the rollback edit: reconcile's rollback route
// (config.RollbackFenceYAML) -- the start-of-run route with the fence taken
// out -- replacing config.Route whole. The static switch and rollback both
// remove the fence, which a single-field patch cannot.
func unfenceChange(config *migrun.MigrationConfig) gateway.RouteChange {
	return gateway.RouteChange{Edit: gateway.EditRoute, Artifact: []byte(config.RollbackFenceYAML)}
}

// unfenceGateway replaces config.Route with reconcile's rollback route
// (config.RollbackFenceYAML, the start-of-run route with the fence taken out)
// to restore normal traffic, then waits for the change to take effect — the same
// convergence check FenceGateway uses. Without the wait we would report traffic
// restored while the gateway is still changing over, and miss failures to take
// effect entirely.
func (s *MigrationActions) unfenceGateway(ctx context.Context, config *migrun.MigrationConfig) error {
	// The rollback is confirmed as a change of its own too. Confirmed against
	// the marker the start-of-run CR carried, it would match a value the
	// gateway already reports — passing instantly while the gateway is still
	// fenced.
	// Apply works even when the fence step never ran in this process (a
	// rollback can come first). It also confirms the gateway accepted the
	// patch (rollback is the worst place to be blind to a rejected apply) and
	// does not wait on this run's pre-fence pods.
	return s.gw.Apply(ctx, s.target(config), gateway.Transition{
		Step:          "unfence",
		Label:         "unfence",
		Change:        unfenceChange(config),
		RestoresStart: true,
		What:          "initial gateway CR",
		Applied:       "Initial gateway CR applied",
	}).Err
}

// detectUnroutedProducers takes two source offset snapshots separated by the
// given duration. If any partition's offset increases between snapshots, it
// means a producer is writing directly to the source cluster (bypassing the
// fenced gateway) and the migration should not proceed.
func (s *MigrationActions) detectUnroutedProducers(ctx context.Context, topics []string, duration time.Duration) error {
	return offset.DetectUnroutedProducers(ctx, s.sourceOffset, s.reporter, topics, duration)
}

// PauseOffsetSync runs the pause_offset_sync stage: with the user's
// spec.staticMigration.consumerOffsetSync.pause opt-in, and only when this run has an actual
// cutover in flight (config.FenceYAML set by the plan), it applies an
// idempotent AlterConfigs SET disabling cluster-link consumer offset sync;
// otherwise it passes through so the FSM still records offset_sync_paused.
// Plan- and manifest-driven only: it never reads the live cluster link to
// decide anything, so re-running it (a resume, a retry) simply re-applies the
// same SET — safe whether or not a prior attempt already landed it.
func (s *MigrationActions) PauseOffsetSync(
	ctx context.Context,
	config *migrun.MigrationConfig,
	restAuth clusterlink.Authenticator,
) error {
	if !config.PauseConsumerOffsetSync {
		slog.Debug("consumer offset sync pause not requested, skipping")
		s.reporter.Detail("Offset-sync pause not requested — skipping")
		tracing.MarkSkipped(ctx, "offset_sync_pause_not_requested")
		return nil
	}
	// Plan-driven: only pause when there is an active cutover this run
	// (reconcile emitted fence work). Read the plan (FenceYAML), never the
	// live link.
	if config.FenceYAML == "" {
		slog.Debug("no cutover in flight, skipping offset-sync pause")
		s.reporter.Detail("No cutover in flight — offset-sync pause skipped")
		tracing.MarkSkipped(ctx, "no_fence_in_plan")
		return nil
	}

	clCfg := BuildClusterLinkConfig(config, restAuth)

	s.reporter.section("Pausing consumer.offset.sync on cluster link...")

	// Optional drain window (--consumer-offset-sync-drain-duration): hold here
	// with sync still enabled before disabling it. The fence has frozen the
	// source consumer offsets — clients can no longer commit — so letting the
	// cluster link run one or more further sync cycles propagates those final
	// committed offsets to the destination, shrinking the set of messages that
	// would otherwise be reprocessed after switchover. Best-effort: offset sync
	// is asynchronous, so this reduces but does not guarantee zero duplicates. A
	// ctx cancellation here leaves sync still enabled (nothing flipped) and
	// cancels the transition. 0 (the default) skips the wait entirely — the
	// prior immediate-disable behaviour.
	if drain := config.ConsumerOffsetSyncDrainDuration; drain > 0 {
		s.reporter.Detail("Draining consumer offset sync for %s before pausing...", drain)
		slog.Debug("draining consumer offset sync before disable", "duration", drain, "clusterLinkName", config.ClusterLinkName)
		if err := tracing.Wait(ctx, "offset_sync_drain", drain); err != nil {
			return err
		}
	}

	// Idempotent: re-applying enable=false on a resume or retry is a no-op
	// AlterConfigs against the cluster link.
	alterCtx, alterCancel := context.WithTimeout(ctx, offsetSyncCallTimeout)
	defer alterCancel()
	if err := s.clusterLinkService.AlterConfigs(alterCtx, clCfg, []clusterlink.ConfigAlteration{
		{Name: offsetSyncEnableKey, Value: "false", Operation: clusterlink.OperationSet},
	}); err != nil {
		return fmt.Errorf("failed to disable %s on cluster link %q: %w", offsetSyncEnableKey, config.ClusterLinkName, err)
	}

	s.reporter.Success("%s set to false on cluster link %s", offsetSyncEnableKey, config.ClusterLinkName)
	return nil
}

// restoreOffsetSyncAfterRollback sets consumer.offset.sync.enable back to the
// declared baseline, as the second half of the abort_fence rollback.
// Soft-fail: the unfence already succeeded, the run is already failing with
// the step error that triggered the rollback, and a restore error must not
// undo the unfence, so a failure prints manual remediation instead; the error
// is returned only so the rollback's span can record it. Gated only
// on config.PauseConsumerOffsetSync, never on whether a prior pause actually
// landed, so it is safe to call even when the pause failed before its own
// AlterConfigs. It runs on a context that ignores cancellation but keeps the
// caller's span: the rollback only fires while the run's context is still
// live, and the restore must not be cut short by a signal arriving after the
// unfence.
func (s *MigrationActions) restoreOffsetSyncAfterRollback(
	ctx context.Context,
	config *migrun.MigrationConfig,
	restAuth clusterlink.Authenticator,
) error {
	if !config.PauseConsumerOffsetSync {
		return nil
	}
	r := newReporter()
	r.section("Restoring consumer.offset.sync on cluster link...")
	want, err := setOffsetSyncBaseline(context.WithoutCancel(ctx), s.clusterLinkService, BuildClusterLinkConfig(config, restAuth), config)
	if err != nil {
		r.Remediation("Gateway unfenced but %v — re-apply manually.", err)
		return err
	}
	r.Success("%s set to %s on cluster link %s", offsetSyncEnableKey, want, config.ClusterLinkName)
	return nil
}

// RestoreOffsetSync runs the restore_offset_sync stage, the last in the
// workflow: when reconcile found a restore owed (config.RestoreOffsetSync), it
// applies an idempotent AlterConfigs SET of consumer.offset.sync.enable to the
// declared baseline; otherwise it passes through so the FSM still records
// offset_sync_restored. Plan- and manifest-driven only: it never reads the
// live cluster link. A failed SET fails the step and leaves the FSM at
// switched. The switch has landed, so nothing rolls back; a re-run, whose
// reconcile still finds the restore owed, retries it.
func (s *MigrationActions) RestoreOffsetSync(ctx context.Context, config *migrun.MigrationConfig, restAuth clusterlink.Authenticator) error {
	if !config.RestoreOffsetSync {
		tracing.MarkSkipped(ctx, "nothing_to_restore")
		if !config.PauseConsumerOffsetSync {
			slog.Debug("consumer offset sync pause not requested, nothing to restore")
			s.reporter.Detail("Offset-sync pause not requested — nothing to restore")
		} else {
			slog.Debug("consumer offset sync already at its baseline, nothing to restore")
			s.reporter.Detail("Consumer offset sync already at its baseline — nothing to restore")
		}
		return nil
	}

	s.reporter.section("Restoring consumer.offset.sync on cluster link...")
	want, err := setOffsetSyncBaseline(ctx, s.clusterLinkService, BuildClusterLinkConfig(config, restAuth), config)
	if err != nil {
		return err
	}
	s.reporter.Success("%s set to %s on cluster link %s", offsetSyncEnableKey, want, config.ClusterLinkName)
	return nil
}

// VerifyFence verifies the fence held: the source offsets of every topic the
// run migrates (config.MigrateTopics, already-promoted ones included) must be
// stable, because an increasing offset after fencing indicates a producer
// bypassing the gateway. An already-promoted topic's mirror no longer copies
// from the source, so a write straight to it would never reach the target.
// When detection is disabled (DetectUnroutedProducersDuration == 0) the step
// succeeds immediately so the FSM still records fence_verified.
//
// detectUnroutedProducers wraps ErrUnroutedProducers only for a real
// detection; a network/fetch error propagates as-is. Either way we just
// return it — what happens next (a rollback, or keeping the fence when
// reconcile forbids one) is the orchestrator's handleStepFailure.
func (s *MigrationActions) VerifyFence(ctx context.Context, config *migrun.MigrationConfig) error {
	if config.DetectUnroutedProducersDuration <= 0 {
		slog.Debug("unrouted producer detection disabled, skipping")
		s.reporter.Detail("Detection disabled (spec.staticMigration.policies.detectUnroutedProducersDuration=0) — skipping check")
		tracing.MarkSkipped(ctx, "detection_disabled")
		return nil
	}

	// Plan-driven no-op: reconcile left no topic to migrate (a run that owes
	// only the offset-sync restore), so there is no source to watch. Read the
	// plan (config.MigrateTopics), never the live cluster.
	if len(config.MigrateTopics) == 0 {
		s.reporter.Detail("No topics to migrate — nothing to check for unrouted producers")
		tracing.MarkSkipped(ctx, "no_topics")
		return nil
	}

	if s.sourceOffset == nil {
		return fmt.Errorf("source offset service is required for unrouted producer detection")
	}

	if err := s.detectUnroutedProducers(ctx, config.MigrateTopics, config.DetectUnroutedProducersDuration); err != nil {
		return err
	}
	s.reporter.Success("Source offsets stable — no unrouted producers detected")
	return nil
}

// PromoteTopics polls offsets and promotes mirror topics that reach zero lag
func (s *MigrationActions) PromoteTopics(ctx context.Context, config *migrun.MigrationConfig, restAuth clusterlink.Authenticator) error {
	// Plan-driven no-op: reconcile emitted no migratable topics, so there is
	// nothing to promote this run. Read the plan (config.Topics), never the
	// live cluster — the reconcile engine already decided.
	if len(config.Topics) == 0 {
		s.reporter.Detail("No topics to migrate — nothing to promote")
		tracing.MarkSkipped(ctx, "no_topics")
		return nil
	}

	if s.sourceOffset == nil || s.destinationOffset == nil {
		return fmt.Errorf("source and destination offset services are required")
	}

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
	// Resume seeding: topics reconcile classified AwaitStopped are already
	// mid-promotion (PENDING_STOPPED) on the link from an earlier run. Seed them
	// straight into awaitingStop so the loop's first pass confirms them via
	// ListMirrorTopics and waits for STOPPED, instead of treating them as
	// promote candidates and re-issuing a promote on an already-promoting mirror
	// (which CC rejects → 3 retries → fatal). They still count toward remaining,
	// so the switch is blocked until they reach STOPPED.
	for _, topic := range config.AwaitStopped {
		if remaining[topic] {
			awaitingStop[topic] = true
		}
	}
	sweepFailures := 0
	polls, batches, promoteErrors := 0, 0, 0
	defer func() {
		tracing.SetAttributes(ctx,
			attribute.Int("kcp.polls", polls),
			attribute.Int("kcp.batches", batches),
			attribute.Int("kcp.promote_errors", promoteErrors))
	}()

	for {
		polls++
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
					s.reporter.Success("%s stopped", topic)
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
			s.reporter.Detail("Waiting for current batch of %d topic(s) to reach STOPPED...",
				len(awaitingStop))
			slog.Debug("batch in flight, waiting for STOPPED before next batch",
				"awaitingStop", len(awaitingStop), "pollInterval", s.promotePollInterval)
			if err := tracing.Wait(ctx, "promote_poll", s.promotePollInterval); err != nil {
				return err
			}
			continue
		}

		// Find topics at zero lag that still need a promote request. Topics
		// already accepted (awaiting STOPPED confirmation) are skipped so we
		// don't re-promote them.
		candidates := make([]string, 0, len(remaining))
		for topic := range remaining {
			if awaitingStop[topic] {
				continue
			}
			candidates = append(candidates, topic)
		}
		sort.Strings(candidates)

		sourceOffsets, destinationOffsets, err := s.fetchSourceAndDestinationOffsets(ctx, candidates)
		if err != nil {
			sweepFailures++
			if sweepFailures >= maxConsecutiveSweepFailures {
				return fmt.Errorf("offset sweep failed %d consecutive times: %w", sweepFailures, err)
			}
			slog.Warn("offset sweep failed, retrying on next tick",
				"attempt", sweepFailures, "maxAttempts", maxConsecutiveSweepFailures, "error", err)
			if err := tracing.Wait(ctx, "promote_poll", s.promotePollInterval); err != nil {
				return err
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

		// Cap the batch when a promote batch size is configured.
		if s.promoteBatchSize > 0 && len(topicsToPromote) > s.promoteBatchSize {
			topicsToPromote = topicsToPromote[:s.promoteBatchSize]
		}

		if len(topicsToPromote) == 0 {
			if len(awaitingStop) > 0 {
				s.reporter.Detail("Waiting for %d promoted topic(s) to reach STOPPED...",
					len(awaitingStop))
				slog.Debug("waiting for accepted promotions to reach STOPPED",
					"awaitingStop", len(awaitingStop), "pollInterval", s.promotePollInterval)
			} else {
				s.reporter.Detail("Waiting for lag to reach zero (%d topics remaining)...",
					len(remaining))
				slog.Debug("no topics at zero lag yet, waiting",
					"remaining", len(remaining), "pollInterval", s.promotePollInterval)
			}
			if err := tracing.Wait(ctx, "promote_poll", s.promotePollInterval); err != nil {
				return err
			}
			continue
		}

		// Promote topics confirmed at zero lag
		s.reporter.Success("%s confirmed at zero lag",
			color.WhiteString("%d/%d topics", len(topicsToPromote), len(remaining)))
		for _, topic := range topicsToPromote {
			s.reporter.line(fmt.Sprintf("   %s %s  %s %s",
				color.GreenString("↳"),
				color.WhiteString(topic),
				color.CyanString("lag:"),
				color.GreenString("0")))
		}
		s.reporter.Detail("Promoting %d mirror topics...", len(topicsToPromote))
		slog.Debug("promoting mirror topics", "topicCount", len(topicsToPromote), "topics", topicsToPromote)

		// Each promote request is irreversible, so confirm the CR is still the
		// one kcp's own fence left immediately before sending it: every batch,
		// every retry, and every request PromoteMirrorTopics resends after a
		// connection-level failure.
		checkUnchanged := func(ctx context.Context) error {
			return s.gw.VerifyUnchangedSincePlan(ctx, s.target(config), "promote")
		}
		if err := checkUnchanged(ctx); err != nil {
			return err
		}
		batches++
		s.promoteSubmitted = true
		promoteResponse, err := s.clusterLinkService.PromoteMirrorTopics(ctx, clusterLinkConfig, topicsToPromote, clusterlink.CheckBeforeResend(checkUnchanged))
		if err != nil {
			return fmt.Errorf("failed to promote mirror topics: %w", err)
		}

		for _, topic := range promoteResponse.Data {
			if topic.ErrorCode != 0 {
				promoteErrors++
				retryCount[topic.MirrorTopicName]++
				s.reporter.line(fmt.Sprintf("   %s Topic %s promotion error (attempt %d/%d): %s",
					color.RedString("[FAIL]"), topic.MirrorTopicName, retryCount[topic.MirrorTopicName], maxPromoteRetries, topic.ErrorMessage))
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
				s.reporter.line(fmt.Sprintf("   %s %s promotion accepted (awaiting STOPPED)",
					color.GreenString("↳"), topic.MirrorTopicName))
				slog.Debug("topic promotion accepted, awaiting stopped confirmation", "topic", topic.MirrorTopicName)
				awaitingStop[topic.MirrorTopicName] = true
			}
		}

		// Test-only intra-promote kill-point: topics accepted but not yet
		// confirmed STOPPED — a real abrupt exit here leaves them PENDING_STOPPED.
		// Returns a plain error so the partial world is left for a resume. No-op
		// in production.
		if len(awaitingStop) > 0 && killpoint.ShouldCancelAfter(killpoint.AfterPromoteAccepted) {
			slog.Warn("test kill-point reached — cancelling run to simulate an abrupt exit", "afterCheckpoint", killpoint.AfterPromoteAccepted)
			return fmt.Errorf("test kill-point %q reached (promote accepted, mirrors not yet confirmed STOPPED)", killpoint.AfterPromoteAccepted)
		}

		slog.Debug("waiting for promotion to complete before next check", "pollInterval", s.promotePollInterval)
		if err := tracing.Wait(ctx, "promote_poll", s.promotePollInterval); err != nil {
			return err
		}
	}
}

// SwitchGateway derives the switch patch for config.Route (a whole-route
// replace of the captured route with the target streamingDomain — see
// switchChange) — patches it in, confirms the gateway accepted the
// new spec, then waits for the change to take effect. The wait uses the
// same no-deadline-by-default behavior as FenceGateway.
//
// Because the captured route is unfenced and already carries pre-staged
// ("redundant") auth for the target domain (checked by migplan.Reconcile's
// redundant-auth precondition), this one whole-route replace yields unfenced +
// target-domain + target-auth with no secret or auth change at cutover — it
// both drops the fence and flips the domain, and there is no separately-authored
// switchover CR to apply.
//
// The acceptance check is what stops this reporting a completed migration for a
// switchover the gateway refused — the failure mode described on
// gateway.TransitionVerifier.WaitForAccepted, hit on 2026-07-27 while setting up the live-cluster
// e2e test infrastructure.
func (s *MigrationActions) SwitchGateway(ctx context.Context, config *migrun.MigrationConfig) error {
	slog.Debug("switching gateway", "gateway", config.InitialCrName, "namespace", config.K8sNamespace)

	// Plan-driven no-op: reconcile emitted no switchover artifact, so there is
	// nothing to switch this run. Read the plan (config.SwitchoverYAML),
	// never the live cluster — the reconcile engine already decided. Not
	// keyed on config.Topics for the same reason as FenceGateway's guard
	// (see its comment): switch is a whole-route action independent of
	// per-topic promote status, and an empty promote set does not mean the
	// switch itself is done.
	if config.SwitchoverYAML == "" {
		s.reporter.Detail("No switchover artifact in plan — nothing to switch")
		tracing.MarkSkipped(ctx, "no_switchover_in_plan")
		return nil
	}

	// Apply prepares its verification on first use; that is a no-op when
	// FenceGateway already ran in this process, and load-bearing for a resume that jumps
	// straight to switch in a fresh process.
	// Once the patch has reached the cluster every failure is marked
	// migrun.ErrSwitchUnconfirmed, as d2s's Switch does.
	res := s.gw.Apply(ctx, s.target(config), gateway.Transition{
		Step:    "switchover",
		Label:   "switch",
		Change:  switchChange(config),
		What:    "switchover gateway CR",
		Applied: "Switchover gateway CR applied",
	})
	if res.Landed && res.Err != nil {
		return fmt.Errorf("%w: %w", migrun.ErrSwitchUnconfirmed, res.Err)
	}
	if res.Err != nil {
		return res.Err
	}

	slog.Debug("gateway switchover complete")
	s.reporter.Success("Gateway switchover complete")
	return nil
}
