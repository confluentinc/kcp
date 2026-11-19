package migration

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/goccy/go-yaml"
	"go.opentelemetry.io/otel/attribute"
)

// maxConsecutiveSweepFailures is how many offset sweeps in a row may fail
// before CheckLags/PromoteTopics abort. A failed sweep is tolerated by
// waiting for the loop's next tick (the tick interval is the backoff — no
// separate schedule), so transient disruptions like leader elections and
// rolling broker restarts ride out across ~3 ticks (+ GetMany's internal
// refresh-and-retry per sweep) while a persistent failure still surfaces
// within seconds. The counter resets on any successful sweep.
const maxConsecutiveSweepFailures = 3

type MigrationActions struct {
	// gw applies and confirms every gateway patch, and owns the capability,
	// expected generation and start-of-run pod bookkeeping those need.
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
	gatewayService gateway.TransitionClient,
	clusterLinkService clusterlink.Service,
	opts ...gateway.TransitionerOption,
) *MigrationActions {
	s := &MigrationActions{
		clusterLinkService:  clusterLinkService,
		lagPollInterval:     2 * time.Second,
		promotePollInterval: 5 * time.Second,
		reporter:            newReporter(),
	}
	// The reporter is read at call time: tests replace s.reporter after construction.
	s.gw = gateway.NewTransitioner(gatewayService, func() gateway.Reporter { return s.reporter }, opts...)
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
	gatewayService gateway.TransitionClient,
	clusterLinkService clusterlink.Service,
	sourceOffset offset.Provider,
	destinationOffset offset.Provider,
	opts ...gateway.TransitionerOption,
) *MigrationActions {
	s := NewMigrationActions(gatewayService, clusterLinkService, opts...)
	s.sourceOffset = sourceOffset
	s.destinationOffset = destinationOffset
	return s
}

// target names the gateway this run drives. Probe derives the fenced and
// switched CRs the capability detector needs: applying either is what puts
// spec.hotReload into force, so a detector shown only the live gateway can
// pick rollout verification for a migration that will hot-reload, and then
// observe nothing.
func (s *MigrationActions) target(config *MigrationConfig) gateway.Target {
	return gateway.Target{
		Namespace:        config.K8sNamespace,
		Name:             config.InitialCrName,
		ConfigPort:       config.GatewayConfigPort,
		StartRouteFenced: config.FencedAtStart,
		Probe: func() ([]byte, []byte, error) {
			fencedCR, err := deriveFencedCRYAML(config)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to derive fenced gateway CR: %w", err)
			}
			switchedCR, err := deriveSwitchedCRYAML(config)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to derive switched gateway CR: %w", err)
			}
			return fencedCR, switchedCR, nil
		},
	}
}

// ResolveGatewayCapability determines how gateway state transitions will be
// verified on the live cluster and adopts it for this run, without the
// hot-reload smoke test: a caller that resolves directly owns that test (see
// gateway.Transitioner.ResolveCapability). Exported for tests only, with no
// production caller: every gateway patch resolves the capability itself
// through the Transitioner.
func (s *MigrationActions) ResolveGatewayCapability(ctx context.Context, config *MigrationConfig) error {
	return s.gw.ResolveCapability(ctx, s.target(config))
}

// SmokeTestHotReload proves the gateway really does apply config revisions,
// before any traffic-affecting change is made. It writes a fresh spec.configId
// to the LIVE Gateway CR. See gateway.TransitionVerifier.SmokeTestHotReload's
// doc comment for why this matters and why patching only spec.configId —
// touching no other field — makes it safe to call at any point, including a
// resume. Exported for tests only, with no production caller.
func (s *MigrationActions) SmokeTestHotReload(ctx context.Context, config *MigrationConfig) error {
	return s.gw.SmokeTestHotReload(ctx, s.target(config))
}

// gatewayHotReloadTimeout returns the configId verification deadline, never
// unbounded: a hot-reload moves no Kubernetes signal to wait on.
func (s *MigrationActions) gatewayHotReloadTimeout() time.Duration {
	return s.gw.HotReloadTimeout()
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
	config *MigrationConfig,
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
	s.gw.SetExpectedGeneration(res.GatewayGeneration)
	s.reporter.Success("Reconcile plan accepted (%d topic(s) in plan)", len(res.PromoteTopics))

	// Gateway capability is NOT resolved here: there is no separate advisory
	// moment to resolve it for anymore (execute is the only command), and
	// config.FenceYAML/SwitchoverYAML were only just set above in the same
	// process. The Transitioner resolves it lazily, once, on the first gateway
	// patch later in this same Execute() call.

	slog.Debug("migration initialized successfully")
	return nil
}

// CheckLags polls source and destination offsets until lag is below threshold
func (s *MigrationActions) CheckLags(
	ctx context.Context,
	config *MigrationConfig,
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
				color.YellowString(formatLag64(topicTotalLags[topic]))))
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

// formatLag64 formats an int64 with comma separators (e.g. 21655 -> "21,655")
func formatLag64(n int64) string {
	return gateway.FormatLag64(n)
}

// FenceGateway applies the fenced gateway CR YAML to block traffic, confirms the
// Confluent operator accepted the new spec, then waits until the gateway pods
// it replaced are gone (or, on a hot-reload gateway, every pod reports the new
// configId). The wait runs without a deadline by
// default — the operator drives convergence. An optional per-workflow
// rolloutTimeout caps the wait when set (via gateway.WithTimeouts). Ctrl-C does
// not unwind a wedged wait: kcp installs no signal handler, so it ends the
// process with the gateway as it is (fenced, if the fence was applied) and a
// re-run continues from there. Whatever the timeout, a replaced pod still
// listed more than replacedPodOverdueMargin past its deletion deadline fails
// the wait (gateway.ErrReplacedPodOverdue), so a lost node cannot wedge it.
func (s *MigrationActions) FenceGateway(ctx context.Context, config *MigrationConfig) error {
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

	// Derive the fence RoutePatch from config.FenceYAML's fence fragment onto
	// config.Route; unfence's whole-route replace (deriveUnfenceRoutePatch) is
	// its exact inverse, restoring the route to its captured state.
	fenceRP, err := deriveFenceRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build fence route patch: %w", err)
	}

	// Apply resolves the capability first (once per process), and records the
	// pods this fence's patch replaces so a rollback unfence does not wait on
	// them: they carry the pre-fence route the unfence restores.
	res := s.gw.Apply(ctx, s.target(config), gateway.Transition{
		Step:    "fence",
		Patch:   fenceRP,
		What:    "fenced gateway CR",
		Applied: "Fenced gateway CR applied",
	})
	// Once the patch has landed (including an ErrApplyUnverified read-back), the
	// fenced spec is live in the cluster, so every later failure leaves it there,
	// possibly holding client traffic on some or all pods. Marking them all lets
	// the orchestrator restore the initial CR rather than exiting with the
	// gateway in a state kcp created and did not resolve. A patch that never
	// reached the cluster is deliberately outside the mark: nothing to undo.
	if res.Landed && res.Err != nil {
		return fmt.Errorf("%w: %w", ErrFenceUnconfirmed, res.Err)
	}
	if res.Err != nil {
		return res.Err
	}

	slog.Debug("gateway fenced and ready")
	s.reporter.Success("Gateway fenced and ready")
	return nil
}

// parseGatewayYAML parses config.GatewayYAML — the whole gateway CR migplan
// pulled and cleaned of server-managed metadata (managedFields,
// resourceVersion, uid, creationTimestamp, generation, status — see
// migplan/gatewayfile.go's cleanGatewayDoc) — into a plain
// map[string]interface{} for the splice helpers below. It does no cleaning of
// its own: migplan cleans the CR when it pulls it.
func parseGatewayYAML(gatewayYAML string) (map[string]interface{}, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal([]byte(gatewayYAML), &obj); err != nil {
		return nil, fmt.Errorf("failed to parse gateway CR: %w", err)
	}
	return obj, nil
}

// deriveFencedCRYAML builds the fenced CR bytes from the gateway CR snapshot
// migplan captured (config.GatewayYAML) by splicing config.FenceYAML onto
// config.Route. There is no separately-snapshotted fenced CR: FenceGateway's
// apply and the Transitioner's capability detection both derive from the same
// source, so they can never drift from each other.
//
// This FSM only runs for static routes (the command layer sends a dynamic
// route to the TBM FSM), so FenceYAML here is always the static
// {fence: {...}} fragment gateway.ReplaceRouteFenceObj expects.
func deriveFencedCRYAML(config *MigrationConfig) ([]byte, error) {
	base, err := parseGatewayYAML(config.GatewayYAML)
	if err != nil {
		return nil, err
	}
	return gateway.ReplaceRouteFenceObj(base, config.Route, []byte(config.FenceYAML))
}

// deriveSwitchedCRYAML builds the switched CR bytes from the gateway CR
// snapshot migplan captured (config.GatewayYAML) by splicing
// config.SwitchoverYAML onto config.Route. There is no separately-snapshotted
// switched CR: SwitchGateway's apply and the Transitioner's capability
// detection both derive from the same source, so they can never drift from each other —
// the same property deriveFencedCRYAML gives the fenced CR.
//
// Static-route-only by construction — see deriveFencedCRYAML's comment.
func deriveSwitchedCRYAML(config *MigrationConfig) ([]byte, error) {
	base, err := parseGatewayYAML(config.GatewayYAML)
	if err != nil {
		return nil, err
	}
	return gateway.ReplaceRouteObj(base, config.Route, []byte(config.SwitchoverYAML))
}

// deriveFenceRoutePatch builds the RoutePatch that grafts config.FenceYAML's
// fence fragment onto config.Route. Consumed by FenceGateway's write path;
// the Transitioner's capability probe still uses deriveFencedCRYAML/full CR
// bytes since capability detection needs a complete CR to apply, not a route
// patch.
func deriveFenceRoutePatch(config *MigrationConfig) (gateway.RoutePatch, error) {
	v, err := gateway.FragmentValue([]byte(config.FenceYAML), "fence")
	if err != nil {
		return gateway.RoutePatch{}, err
	}
	return gateway.RoutePatch{RouteName: config.Route, Field: "fence", Value: v}, nil
}

// deriveSwitchRoutePatch builds the RoutePatch SwitchGateway applies: reconcile's
// switched route (config.SwitchoverYAML) — unfenced, bound to the target
// domain, carrying the route's pre-staged target auth — replacing config.Route
// whole.
func deriveSwitchRoutePatch(config *MigrationConfig) (gateway.RoutePatch, error) {
	return wholeRoutePatch(config, config.SwitchoverYAML)
}

// deriveUnfenceRoutePatch builds the RoutePatch a rollback applies: reconcile's
// rollback route (config.RollbackFenceYAML) — the start-of-run route with the
// fence taken out — replacing config.Route whole.
func deriveUnfenceRoutePatch(config *MigrationConfig) (gateway.RoutePatch, error) {
	return wholeRoutePatch(config, config.RollbackFenceYAML)
}

// wholeRoutePatch is the RoutePatch replacing config.Route with artifact's
// {route: …} exactly as reconcile built it. The static switch and rollback both
// remove the fence, which a single-field patch cannot, so both replace the
// whole route.
func wholeRoutePatch(config *MigrationConfig, artifact string) (gateway.RoutePatch, error) {
	route, err := gateway.FragmentValue([]byte(artifact), "route")
	if err != nil {
		return gateway.RoutePatch{}, err
	}
	return gateway.RoutePatch{RouteName: config.Route, Value: route}, nil
}

// unfenceGateway replaces config.Route with reconcile's rollback route
// (config.RollbackFenceYAML, the start-of-run route with the fence taken out)
// to restore normal traffic, then waits for the replaced gateway pods to be gone
// (or the configId applied on every pod) — the same convergence check
// FenceGateway uses. Without the wait we would report traffic restored while
// pods are still cycling, and miss rollout failures entirely.
func (s *MigrationActions) unfenceGateway(ctx context.Context, config *MigrationConfig) error {
	// The rollback patch gets a fresh configId too. Without one it would carry
	// whatever revision the initial CR was captured with, and the verification
	// below would match against a value the pods already report — passing
	// instantly while the gateway is still fenced.
	unfenceRP, err := deriveUnfenceRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build unfence route patch: %w", err)
	}

	// Apply resolves the capability first: a rollback whose fence step never
	// ran in this process has not resolved it yet. It also confirms the
	// operator accepted the patch (rollback is the worst place to be blind to a
	// rejected apply) and skips only this run's pre-fence pods.
	return s.gw.Apply(ctx, s.target(config), gateway.Transition{
		Step:          "unfence",
		Patch:         unfenceRP,
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

// PauseOffsetSync runs the pause_offset_sync stage: with the operator's
// spec.staticMigration.consumerOffsetSync.pause opt-in, and only when this run has an actual
// cutover in flight (config.FenceYAML set by the plan), it applies an
// idempotent AlterConfigs SET disabling cluster-link consumer offset sync;
// otherwise it passes through so the FSM still records offset_sync_paused.
// Plan- and manifest-driven only: it never reads the live cluster link to
// decide anything, so re-running it (a resume, a retry) simply re-applies the
// same SET — safe whether or not a prior attempt already landed it.
func (s *MigrationActions) PauseOffsetSync(
	ctx context.Context,
	config *MigrationConfig,
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
	config *MigrationConfig,
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
func (s *MigrationActions) RestoreOffsetSync(ctx context.Context, config *MigrationConfig, restAuth clusterlink.Authenticator) error {
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
func (s *MigrationActions) VerifyFence(ctx context.Context, config *MigrationConfig) error {
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
func (s *MigrationActions) PromoteTopics(ctx context.Context, config *MigrationConfig, restAuth clusterlink.Authenticator) error {
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
		checkGeneration := func(ctx context.Context) error {
			return s.gw.CheckGeneration(ctx, s.target(config), "promote")
		}
		if err := checkGeneration(ctx); err != nil {
			return err
		}
		batches++
		s.promoteSubmitted = true
		promoteResponse, err := s.clusterLinkService.PromoteMirrorTopics(ctx, clusterLinkConfig, topicsToPromote, clusterlink.CheckBeforeResend(checkGeneration))
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

// SwitchGateway derives the switch RoutePatch for config.Route (a whole-route
// replace of the captured route with the target streamingDomain — see
// deriveSwitchRoutePatch) — patches it in, confirms the operator accepted the
// new spec, then waits for the replaced gateway pods to be gone (or the configId
// to be applied on every pod). The wait uses the
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
// switchover the operator refused — the failure mode described on
// gateway.TransitionVerifier.WaitForAccepted, hit on 2026-07-27 while setting up the live-cluster
// e2e test infrastructure.
func (s *MigrationActions) SwitchGateway(ctx context.Context, config *MigrationConfig) error {
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

	switchRP, err := deriveSwitchRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build switch route patch: %w", err)
	}

	// Apply resolves the capability first; that is a no-op when FenceGateway
	// already did in this process, and load-bearing for a resume that jumps
	// straight to switch in a fresh process.
	// Once the patch has reached the cluster every failure is marked
	// ErrSwitchUnconfirmed, as d2s's Switch does.
	res := s.gw.Apply(ctx, s.target(config), gateway.Transition{
		Step:    "switchover",
		Patch:   switchRP,
		What:    "switchover gateway CR",
		Applied: "Switchover gateway CR applied",
	})
	if res.Landed && res.Err != nil {
		return fmt.Errorf("%w: %w", ErrSwitchUnconfirmed, res.Err)
	}
	if res.Err != nil {
		return res.Err
	}

	slog.Debug("gateway switchover complete")
	s.reporter.Success("Gateway switchover complete")
	return nil
}
