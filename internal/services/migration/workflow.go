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
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/fatih/color"
	"github.com/goccy/go-yaml"
	k8stypes "k8s.io/apimachinery/pkg/types"
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
	// hotReloadTimeout is the deadline applied to per-pod configId verification.
	// Unlike rolloutTimeout this has a real default: a hot-reload does not move
	// any Kubernetes signal, so without a deadline a gateway whose config
	// watcher never started would hang forever with nothing to show for it.
	hotReloadTimeout time.Duration
	// gatewayCapability is how gateway transitions are verified for this run.
	// Zero value (VerifyRollout, no configId) is deliberately the safe default:
	// an unresolved capability behaves exactly as kcp did before hot-reload
	// support existed.
	gatewayCapability gateway.Capability
	// capabilityResolved guards ensureGatewayCapability so resolution happens
	// at most once per process, no matter which of FenceGateway/SwitchGateway
	// runs first.
	capabilityResolved bool
	reporter           *reporter // user-facing terminal output
}

func NewMigrationActions(
	gatewayService gateway.Service,
	clusterLinkService clusterlink.Service,
) *MigrationActions {
	return &MigrationActions{
		gatewayService:      gatewayService,
		clusterLinkService:  clusterLinkService,
		lagPollInterval:     2 * time.Second,
		promotePollInterval: 5 * time.Second,
		reporter:            newReporter(),
	}
}

func NewMigrationActionsWithOffsets(
	gatewayService gateway.Service,
	clusterLinkService clusterlink.Service,
	sourceOffset offset.Provider,
	destinationOffset offset.Provider,
) *MigrationActions {
	return &MigrationActions{
		gatewayService:      gatewayService,
		clusterLinkService:  clusterLinkService,
		sourceOffset:        sourceOffset,
		destinationOffset:   destinationOffset,
		lagPollInterval:     2 * time.Second,
		promotePollInterval: 5 * time.Second,
		reporter:            newReporter(),
	}
}

// SetRolloutTimeout sets the deadline applied to gateway-readiness waits.
// A value of 0 means no deadline.
func (s *MigrationActions) SetRolloutTimeout(d time.Duration) {
	s.rolloutTimeout = d
}

// SetHotReloadTimeout sets the deadline for per-pod configId verification.
// A value of 0 falls back to gateway.DefaultHotReloadTimeout — unlike the
// rollout waits, this one is never allowed to run unbounded.
func (s *MigrationActions) SetHotReloadTimeout(d time.Duration) {
	s.hotReloadTimeout = d
}

// verifier builds the shared gateway apply/wait/verify mechanism, seeded
// with this run's current capability and timeouts. TBM's own Actions type
// builds the identical thing (see tbm's verifier() method) — only how each
// derives CR bytes and adopts a newly resolved capability differs, which
// stays here rather than in gateway.TransitionVerifier itself.
func (s *MigrationActions) verifier() *gateway.TransitionVerifier {
	return &gateway.TransitionVerifier{
		Service:          s.gatewayService,
		Reporter:         s.reporter,
		Capability:       s.gatewayCapability,
		RolloutTimeout:   s.rolloutTimeout,
		HotReloadTimeout: s.hotReloadTimeout,
	}
}

// ensureGatewayCapability resolves gatewayCapability at most once per
// process: the first of FenceGateway or SwitchGateway to run this call
// actually resolves it (and smoke-tests hot-reload); whichever runs second,
// if any, in the same process is then a no-op. Mirrors
// tbm.TBMActions.ensureGatewayCapability: resolution is lazy, tied to the
// first gateway-touching step, rather than a blanket pre-Execute check in
// the command layer — the command layer cannot resolve capability before a
// fresh migration's Initialize step has populated config.FenceYAML and
// config.SwitchoverYAML, which deriveFencedCRYAML/deriveSwitchedCRYAML need.
func (s *MigrationActions) ensureGatewayCapability(ctx context.Context, config *MigrationConfig) error {
	if s.capabilityResolved {
		return nil
	}
	if err := s.ResolveGatewayCapability(ctx, config); err != nil {
		return err
	}
	return s.VerifyHotReloadCapability(ctx, config)
}

// ResolveGatewayCapability determines how gateway state transitions will be
// verified on the live cluster, adopts it for this run, and records it on the
// migration config. Called once, authoritatively, via ensureGatewayCapability
// — there is no separate advisory call at Initialize: execute is the only
// command now, so there is no earlier "review, then commit" moment to advise
// at (this function used to be called a second, advisory time from Initialize,
// back when init and execute were separate commands).
func (s *MigrationActions) ResolveGatewayCapability(ctx context.Context, config *MigrationConfig) error {
	// Settle the port first: the last gate probes /config, so detection needs it.
	if config.GatewayConfigPort == 0 {
		config.GatewayConfigPort = gateway.DefaultGatewayConfigPort
	}

	// The fenced and switched CRs are inputs to detection, not just payloads to
	// apply later: applying one is what puts spec.hotReload into force, so a
	// detector shown only the live gateway can pick rollout verification for a
	// migration that will hot-reload — and then observe nothing. Neither is
	// snapshotted: both are derived the same way, injecting onto the live
	// initial CR (a fence block for the fenced CR, a streamingDomain flip for
	// the switched CR).
	fencedCrYAML, err := deriveFencedCRYAML(config)
	if err != nil {
		return fmt.Errorf("failed to derive fenced gateway CR: %w", err)
	}
	switchedCrYAML, err := deriveSwitchedCRYAML(config)
	if err != nil {
		return fmt.Errorf("failed to derive switched gateway CR: %w", err)
	}

	previous := config.GatewayVerificationMode
	capability, err := s.verifier().ResolveCapability(ctx, config.K8sNamespace, config.InitialCrName,
		gatewayConfigPort(config), fencedCrYAML, switchedCrYAML)
	if err != nil {
		return err
	}
	s.gatewayCapability = capability
	// Set here, not only in ensureGatewayCapability: a caller that resolves
	// capability directly (bypassing ensureGatewayCapability — every
	// pre-existing unit test in gateway_capability_test.go/
	// gateway_transition_test.go/gateway_baseline_test.go does exactly this)
	// must still be honored as "already resolved" by a later
	// FenceGateway/SwitchGateway call in the same process — otherwise
	// ensureGatewayCapability would silently re-resolve (harmless) AND
	// re-run VerifyHotReloadCapability a second, unrequested time.
	s.capabilityResolved = true
	config.GatewayVerificationMode = string(capability.Mode)
	config.GatewayHotReloadEnabled = capability.HotReloadEnabled

	// A change since init is worth saying out loud either way: it means the
	// cluster moved under the migration.
	if previous != "" && previous != string(capability.Mode) {
		s.reporter.warn("Gateway verification changed since this migration was initialised: %q -> %q. Using the live cluster's capability.",
			previous, capability.Mode)
	}

	return nil
}

// patchGatewayRoute patches one route mutation onto the gateway CR, attaching a
// fresh config revision id when the cluster supports one. See
// gateway.TransitionVerifier.PatchCR.
func (s *MigrationActions) patchGatewayRoute(ctx context.Context, config *MigrationConfig, rp gateway.RoutePatch, step string) (gateway.ApplyResult, error) {
	return s.verifier().PatchCR(ctx, config.K8sNamespace, config.InitialCrName, rp, step)
}

// waitForGatewayConfigApplied blocks until every ready gateway pod reports the
// applied configId. This is the mechanism-agnostic check: it holds whether CFK
// chose a hot-reload or a pod roll, so kcp never has to predict which.
//
// Correctness does not depend on the mechanism, but the budget does. A
// hot-reload converges in seconds and gets the bounded hot-reload budget; a roll
// has to pull images and pass readiness probes, so the moment one is observed the
// wait switches to the user's --rollout-timeout. Without that switch a
// transition that legitimately rolls — a switchover whose CR adds a route or a
// TLS secret, exactly the case that motivated per-pod verification — would be cut
// off at 90s with traffic already fenced.
func (s *MigrationActions) waitForGatewayConfigApplied(ctx context.Context, config *MigrationConfig, applied gateway.ApplyResult, step string) error {
	return s.verifier().WaitForConfigApplied(ctx, config.K8sNamespace, config.InitialCrName, gatewayConfigPort(config), applied, step)
}

// verifyGatewayTransition confirms a transition landed, by whichever means the
// cluster supports. An empty applied.ConfigID means the cluster cannot report a
// config revision, so this falls back to the Deployment rollout wait.
func (s *MigrationActions) verifyGatewayTransition(ctx context.Context, config *MigrationConfig, applied gateway.ApplyResult, step string) error {
	return s.verifier().VerifyTransition(ctx, config.K8sNamespace, config.InitialCrName, gatewayConfigPort(config), applied, step)
}

// VerifyHotReloadCapability proves the gateway really does apply config
// revisions, before any traffic-affecting change is made. See
// gateway.TransitionVerifier.VerifyHotReloadCapability's doc comment for why
// this matters and why patching only spec.configId — touching no other
// field — makes it safe to call at any point, including a resume.
func (s *MigrationActions) VerifyHotReloadCapability(ctx context.Context, config *MigrationConfig) error {
	return s.verifier().VerifyHotReloadCapability(ctx, config.K8sNamespace, config.InitialCrName, gatewayConfigPort(config))
}

// gatewayConfigPort returns the port to poll GET /config on, tolerating a
// migration state file written before the field existed.
func gatewayConfigPort(config *MigrationConfig) int {
	if config.GatewayConfigPort <= 0 {
		return gateway.DefaultGatewayConfigPort
	}
	return config.GatewayConfigPort
}

// gatewayHotReloadTimeout returns the configId verification deadline, never
// unbounded — see the field comment.
func (s *MigrationActions) gatewayHotReloadTimeout() time.Duration {
	return s.verifier().HotReloadTimeoutOrDefault()
}

// SetPromoteBatchSize caps how many mirror topics are promoted per batch during
// PromoteTopics. A value of 0 (the default) means unlimited — all zero-lag
// topics are promoted at once. When set (>0), each batch is promoted and fully
// confirmed STOPPED before the next batch is submitted.
func (s *MigrationActions) SetPromoteBatchSize(n int) {
	s.promoteBatchSize = n
}

// Initialize captures the migplan-derived artifacts onto config and runs the
// AAO-specific preconditions migplan does not cover: gateway capability
// resolution and the PauseConsumerOffsetSync live precondition. Mirrors
// TBMActions.Initialize's shape (check res.Refused, copy fields) — everything
// migplan.Reconcile already validated (staged-auth/secret existence,
// cluster-link topic classification) is NOT re-checked here.
func (s *MigrationActions) Initialize(
	ctx context.Context,
	config *MigrationConfig,
	restAuth clusterlink.Authenticator,
	res *migplan.Result,
) error {
	slog.Debug("initializing migration", "migrationId", config.MigrationId)

	if res.Refused {
		return fmt.Errorf("reconcile plan refused:\n%s", strings.Join(res.Reasons, "\n"))
	}

	config.Topics = res.Topics
	config.FenceYAML = res.FenceYAML
	config.SwitchoverYAML = res.SwitchoverYAML
	config.GatewayYAML = res.GatewayYAML
	config.Route = res.Route
	config.Mode = res.Mode
	s.reporter.Success("Reconcile plan accepted (%d topic(s) in plan)", len(res.Topics))

	// Gateway capability is NOT resolved here: there is no separate advisory
	// moment to resolve it for anymore (execute is the only command), and
	// config.FenceYAML/SwitchoverYAML were only just set above in the same
	// process — ensureGatewayCapability resolves it lazily, once, from
	// whichever of FenceGateway/SwitchGateway runs first later in this same
	// Execute() call.

	clusterLinkConfig := clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		Auth:         restAuth,
		Topics:       config.Topics,
	}

	// Get cluster link configs — still needed for the PauseConsumerOffsetSync
	// precondition below and for the offset-sync restore bookend's diff
	// baseline. Topic classification/validation is no longer done here —
	// migplan.Reconcile's Classify already proved config.Topics feasible.
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
	// here is the expected mid-flight state, not drift.
	if config.PauseConsumerOffsetSync && !config.PauseConsumerOffsetSyncFlipped {
		observed, present := configs[offsetSyncEnableKey]
		switch {
		case !present:
			return fmt.Errorf("spec.clusterLink.pauseConsumerOffsetSync refused: cluster link %q has no %s config key (expected %q)", config.ClusterLinkName, offsetSyncEnableKey, "true")
		case observed != "true":
			return fmt.Errorf("spec.clusterLink.pauseConsumerOffsetSync refused: cluster link %q has %s=%q (expected %q)", config.ClusterLinkName, offsetSyncEnableKey, observed, "true")
		}
		s.reporter.Success("Cluster link %s=true (pause-on-execute intent recorded)", offsetSyncEnableKey)
	}

	// Defensive guard: never overwrite the pre-disable snapshot once the
	// bookend has flipped consumer.offset.sync.enable=false.
	if !config.PauseConsumerOffsetSyncFlipped {
		config.ClusterLinkConfigs = configs
	}

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
	s.reporter.line(fmt.Sprintf("%s Checking replication lag across %s (threshold: %s)",
		color.CyanString("⏳"),
		color.CyanString("%d topics", len(config.Topics)),
		color.YellowString("%d", lagThreshold)))
	s.reporter.blank()

	if len(config.Topics) == 0 {
		s.reporter.line(fmt.Sprintf("%s No topics to check", color.GreenString("✔")))
		return nil
	}

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

		sourceOffsets, destinationOffsets, err := s.fetchSourceAndDestinationOffsets(ctx, config.Topics)
		if err != nil {
			sweepFailures++
			if sweepFailures >= maxConsecutiveSweepFailures {
				return fmt.Errorf("offset sweep failed %d consecutive times: %w", sweepFailures, err)
			}
			slog.Warn("⚠️ offset sweep failed, retrying on next tick",
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

		if allBelowThreshold {
			s.reporter.blank()
			s.reporter.line(fmt.Sprintf("%s All topic lags below threshold (%d)",
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

// waitForGatewayAccepted blocks until the Confluent operator has accepted the
// gateway CR just applied. See gateway.TransitionVerifier.WaitForAccepted's
// doc comment for why this matters.
func (s *MigrationActions) waitForGatewayAccepted(ctx context.Context, config *MigrationConfig, step string) error {
	return s.verifier().WaitForAccepted(ctx, config.K8sNamespace, config.InitialCrName, step)
}

// FenceGateway applies the fenced gateway CR YAML to block traffic, confirms the
// Confluent operator accepted the new spec, then waits for it to report the
// gateway as Ready at that generation. The wait runs without a deadline by
// default — the operator drives convergence and the user can Ctrl-C if a
// rollout wedges. An optional per-workflow rolloutTimeout caps the wait when
// set (via SetRolloutTimeout).
func (s *MigrationActions) FenceGateway(ctx context.Context, config *MigrationConfig) error {
	slog.Debug("fencing gateway", "gateway", config.InitialCrName, "namespace", config.K8sNamespace)

	// Capability must be resolved before capturePods below reads it (and
	// before deriveFenceRoutePatch, which needs config.FenceYAML — already set
	// by Initialize earlier in this same run). A no-op on any run past the
	// first gateway-touching step in this process.
	if err := s.ensureGatewayCapability(ctx, config); err != nil {
		return err
	}

	// When unrouted-producer detection is enabled the fence must be genuinely
	// in effect before the detector's first source-offset snapshot. A plain
	// readiness wait returns as soon as the new fenced pod reports Ready, but
	// Kubernetes keeps the old, still-unfenced pod serving behind the same
	// Service until its readiness probe scales it down (~30-40s). A legitimate
	// producer routed through the gateway keeps landing writes on the source
	// through that old pod for the whole window, so the detector sees source
	// offsets rising and false-positives. Capturing the current pod set here —
	// before the CR apply — lets us then wait for those old pods to actually
	// terminate. Off by default: the stronger (and slower) wait only runs when
	// detection is requested.
	detecting := config.DetectUnroutedProducersDuration > 0

	// The pod-replacement wait is only usable when the fence is expected to
	// replace pods at all. Under a hot-reload the running pods apply the fence in
	// place and are never replaced, so waiting for the pre-fence pods to
	// disappear could never be satisfied. Per-pod configId verification gives the
	// detector a strictly stronger guarantee anyway: it proves every serving pod
	// has the fenced config, rather than inferring it from pod turnover.
	capturePods := detecting && !s.gatewayCapability.InjectsConfigID()

	var oldPodUIDs map[k8stypes.UID]struct{}
	if capturePods {
		var err error
		oldPodUIDs, err = s.gatewayService.GetGatewayPodUIDs(ctx, config.K8sNamespace, config.InitialCrName)
		if err != nil {
			return fmt.Errorf("failed to capture gateway pods before fencing: %w", err)
		}
	}

	// Derive the fence RoutePatch from config.FenceYAML's fence fragment onto
	// config.Route; unfence's whole-route replace (deriveUnfenceRoutePatch) is
	// its exact inverse, restoring the route to its captured state.
	fenceRP, err := deriveFenceRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build fence route patch: %w", err)
	}

	applied, err := s.patchGatewayRoute(ctx, config, fenceRP, "fence")
	if err != nil {
		if errors.Is(err, gateway.ErrApplyUnverified) {
			// The patch itself succeeded and the fenced spec is live in the
			// cluster; only kcp's own read-back of the stored configId was
			// unconfirmed. That is exactly the state confirmFence's failures
			// leave behind, so — unlike a patch that never reached the cluster —
			// it earns the same restore.
			return fmt.Errorf("%w: failed to apply fenced gateway CR: %w", ErrFenceUnconfirmed, err)
		}
		return fmt.Errorf("failed to apply fenced gateway CR: %w", err)
	}
	slog.Debug("fenced gateway CR applied")
	s.reporter.Success("Fenced gateway CR applied")

	// The fenced spec is now live in the cluster, so from here on every failure
	// leaves it there — possibly holding client traffic on some or all pods.
	// Marking them all lets the orchestrator restore the initial CR rather than
	// exiting with the gateway in a state kcp created and did not resolve. The
	// pod-UID capture failure above is deliberately outside the mark: it runs
	// strictly before the apply, so nothing has reached the cluster yet to undo.
	if err := s.confirmFence(ctx, config, applied, oldPodUIDs, capturePods); err != nil {
		return fmt.Errorf("%w: %w", ErrFenceUnconfirmed, err)
	}

	slog.Debug("gateway fenced and ready")
	s.reporter.Success("Gateway fenced and ready")
	return nil
}

// confirmFence blocks until the applied fenced spec is confirmed in force, by
// whichever means the cluster supports. Split from FenceGateway so that every
// path that can leave the fenced spec live is inside one unit its caller can
// mark ErrFenceUnconfirmed — a failure added here cannot forget to be restorable.
func (s *MigrationActions) confirmFence(
	ctx context.Context,
	config *MigrationConfig,
	applied gateway.ApplyResult,
	oldPodUIDs map[k8stypes.UID]struct{},
	capturePods bool,
) error {
	// Gate the waits below on the operator having accepted the fenced spec.
	// The Deployment-based waits cannot tell "no restart needed" apart from
	// "operator refused the spec" — see waitForGatewayAccepted.
	if err := s.waitForGatewayAccepted(ctx, config, "fence"); err != nil {
		return err
	}

	switch {
	case applied.ConfigID != "":
		// Covers both mechanisms, and subsumes the pod-replacement wait below.
		return s.waitForGatewayConfigApplied(ctx, config, applied, "fence")
	case capturePods:
		// With detection on and no configId to verify, wait until the old
		// unfenced pods are gone rather than just until the new pod is Ready —
		// see the comment in FenceGateway.
		s.reporter.Detail("Waiting for gateway readiness...")
		slog.Debug("waiting for gateway pod replacement", "rolloutTimeout", s.rolloutTimeout,
			"baselineDeploymentGeneration", applied.BaselineDeploymentGeneration)
		if err := s.gatewayService.WaitForGatewayPods(ctx, config.K8sNamespace, config.InitialCrName, oldPodUIDs,
			applied.BaselineDeploymentGeneration, 5*time.Second, s.rolloutTimeout, s.printPodRolloutProgress); err != nil {
			return fmt.Errorf("failed waiting for gateway pod rollout: %w", err)
		}
		return nil
	default:
		return s.verifyGatewayTransition(ctx, config, applied, "fence")
	}
}

// parseGatewayYAML parses config.GatewayYAML — the whole gateway CR migplan
// pulled and cleaned of server-managed metadata (managedFields,
// resourceVersion, uid, creationTimestamp, generation, status — see
// migplan/gatewayfile.go's cleanGatewayDoc) — into a plain
// map[string]interface{} for the splice helpers below. Unlike the pre-migplan
// cleanInitialCR this does no cleaning of its own: migplan already did that
// once, centrally, when it captured GatewayYAML at init.
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
// apply and ResolveGatewayCapability's detection both derive from the same
// source, so they can never drift from each other.
//
// AAO's execute path is static-route-only by construction: a dynamic-resolved
// route is refused before any MigrationConfig is ever persisted (see
// MigrationConfig.Mode's own doc comment), so FenceYAML here is always the
// static {fence: {...}} fragment gateway.ReplaceRouteFenceObj expects.
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
// switched CR: SwitchGateway's apply and ResolveGatewayCapability's detection
// both derive from the same source, so they can never drift from each other —
// the same property deriveFencedCRYAML gives the fenced CR.
//
// Static-route-only by construction — see deriveFencedCRYAML's comment.
func deriveSwitchedCRYAML(config *MigrationConfig) ([]byte, error) {
	base, err := parseGatewayYAML(config.GatewayYAML)
	if err != nil {
		return nil, err
	}
	return gateway.ReplaceRouteStreamingDomainObj(base, config.Route, []byte(config.SwitchoverYAML))
}

// deriveFenceRoutePatch builds the RoutePatch that grafts config.FenceYAML's
// fence fragment onto config.Route. Consumed by FenceGateway's write path;
// ResolveGatewayCapability's probe still uses deriveFencedCRYAML/full CR bytes
// since capability detection needs a complete CR to apply, not a route patch.
func deriveFenceRoutePatch(config *MigrationConfig) (gateway.RoutePatch, error) {
	v, err := gateway.FragmentValue([]byte(config.FenceYAML), "fence")
	if err != nil {
		return gateway.RoutePatch{}, err
	}
	return gateway.RoutePatch{RouteName: config.Route, Field: "fence", Value: v}, nil
}

// deriveSwitchRoutePatch builds the RoutePatch that grafts
// config.SwitchoverYAML's streamingDomain fragment onto config.Route.
// Consumed by SwitchGateway's write path.
func deriveSwitchRoutePatch(config *MigrationConfig) (gateway.RoutePatch, error) {
	v, err := gateway.FragmentValue([]byte(config.SwitchoverYAML), "streamingDomain")
	if err != nil {
		return gateway.RoutePatch{}, err
	}
	return gateway.RoutePatch{RouteName: config.Route, Field: "streamingDomain", Value: v}, nil
}

// deriveUnfenceRoutePatch builds the RoutePatch that restores config.Route to
// its captured state in config.GatewayYAML — a whole-route replace (Field ==
// "") rather than a single-key mutation, since unfence removes the fence key
// entirely instead of overwriting it.
func deriveUnfenceRoutePatch(config *MigrationConfig) (gateway.RoutePatch, error) {
	route, err := gateway.RouteObject([]byte(config.GatewayYAML), config.Route)
	if err != nil {
		return gateway.RoutePatch{}, err
	}
	return gateway.RoutePatch{RouteName: config.Route, Value: route}, nil
}

// unfenceGateway patches config.Route back to its captured state in the
// gateway CR snapshot migplan captured (config.GatewayYAML) to restore normal
// traffic, then waits for the operator to report the gateway Ready at the
// restored spec — the same convergence check FenceGateway uses. Without the
// wait we would report traffic restored while pods are still cycling, and
// miss rollout failures entirely.
func (s *MigrationActions) unfenceGateway(ctx context.Context, config *MigrationConfig) error {
	// The rollback patch gets a fresh configId too. Without one it would carry
	// whatever revision the initial CR was captured with, and the verification
	// below would match against a value the pods already report — passing
	// instantly while the gateway is still fenced.
	unfenceRP, err := deriveUnfenceRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build unfence route patch: %w", err)
	}

	applied, err := s.patchGatewayRoute(ctx, config, unfenceRP, "unfence")
	if err != nil {
		return fmt.Errorf("failed to apply initial gateway CR: %w", err)
	}
	slog.Debug("initial gateway CR applied")
	s.reporter.Success("Initial gateway CR applied")

	// Rollback is the worst place to be blind to a rejected apply: without the
	// acceptance check this reports traffic restored while the gateway is still
	// fenced.
	if err := s.waitForGatewayAccepted(ctx, config, "unfence"); err != nil {
		return err
	}

	return s.verifyGatewayTransition(ctx, config, applied, "unfence")
}

// detectUnroutedProducers takes two source offset snapshots separated by the
// given duration. If any partition's offset increases between snapshots, it
// means a producer is writing directly to the source cluster (bypassing the
// fenced gateway) and the migration should not proceed.
func (s *MigrationActions) detectUnroutedProducers(ctx context.Context, topics []string, duration time.Duration) error {
	return offset.DetectUnroutedProducers(ctx, s.sourceOffset, s.reporter, topics, duration)
}

// PauseOffsetSync runs the pause_offset_sync stage: with the operator's
// --pause-consumer-offset-sync opt-in it pauses cluster-link consumer offset
// sync immediately after fencing; otherwise it passes through so the FSM
// still records offset_sync_paused. The already-flipped guard makes resumes
// (and legacy state files whose pause ran pre-FSM) idempotent.
func (s *MigrationActions) PauseOffsetSync(
	ctx context.Context,
	config *MigrationConfig,
	restAuth clusterlink.Authenticator,
	persist func() error,
) error {
	if !config.PauseConsumerOffsetSync {
		slog.Debug("⏭️ consumer offset sync pause not requested, skipping")
		s.reporter.Detail("Offset-sync pause not requested — skipping")
		return nil
	}
	if config.PauseConsumerOffsetSyncFlipped {
		slog.Info("⏭️ consumer.offset.sync.enable already flipped, skipping pause", "migrationId", config.MigrationId)
		s.reporter.Detail("consumer.offset.sync already paused — skipping")
		return nil
	}

	clCfg := BuildClusterLinkConfig(config, restAuth)

	s.reporter.section("⏸  Pausing consumer.offset.sync on cluster link...")

	// Per-call deadlines derived from the parent ctx so signal cancellation
	// still propagates, but a hung REST endpoint cannot block indefinitely.
	listCtx, listCancel := context.WithTimeout(ctx, bookendCallTimeout)
	currentConfigs, err := s.clusterLinkService.ListConfigs(listCtx, clCfg)
	listCancel()
	if err != nil {
		return fmt.Errorf("failed to query cluster link %q for drift detection: %w", config.ClusterLinkName, err)
	}
	observed, present := currentConfigs[offsetSyncEnableKey]
	switch {
	case !present:
		return fmt.Errorf("spec.clusterLink.pauseConsumerOffsetSync refused: cluster link %q has no %s key — cannot verify the pre-pause state", config.ClusterLinkName, offsetSyncEnableKey)
	case observed != "true":
		return fmt.Errorf("spec.clusterLink.pauseConsumerOffsetSync refused: %s on cluster link %q is not enabled — either a previous kcp run was interrupted mid-pause before recording it, or the config was changed externally; inspect the cluster link and the migration state file before re-running", offsetSyncEnableKey, config.ClusterLinkName)
	}

	// Optional drain window (--consumer-offset-sync-drain-duration): hold here
	// with sync still enabled before disabling it. The fence has frozen the
	// source consumer offsets — clients can no longer commit — so letting the
	// cluster link run one or more further sync cycles propagates those final
	// committed offsets to the destination, shrinking the set of messages that
	// would otherwise be reprocessed after switchover. Best-effort: offset sync
	// is asynchronous, so this reduces but does not guarantee zero duplicates. A
	// ctx cancellation here leaves sync still enabled (nothing flipped) and
	// cancels the transition, matching the drift-refusal path above. 0 (the
	// default) skips the wait entirely — the prior immediate-disable behaviour.
	if drain := config.ConsumerOffsetSyncDrainDuration; drain > 0 {
		s.reporter.Detail("Draining consumer offset sync for %s before pausing...", drain)
		slog.Debug("draining consumer offset sync before disable", "duration", drain, "clusterLinkName", config.ClusterLinkName)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(drain):
		}
	}

	alterCtx, alterCancel := context.WithTimeout(ctx, bookendCallTimeout)
	err = s.clusterLinkService.AlterConfigs(alterCtx, clCfg, []clusterlink.ConfigAlteration{
		{Name: offsetSyncEnableKey, Value: "false", Operation: clusterlink.OperationSet},
	})
	alterCancel()
	if err != nil {
		return fmt.Errorf("failed to disable %s on cluster link %q: %w", offsetSyncEnableKey, config.ClusterLinkName, err)
	}

	// Inline persist: the marker's crash window (AlterConfigs done, marker not
	// yet on disk) stays as small as the pre-FSM bookend kept it — the FSM's
	// own post-transition persist would widen it.
	config.PauseConsumerOffsetSyncFlipped = true
	if err := persist(); err != nil {
		return fmt.Errorf("disabled %s on cluster link %q but failed to persist marker: %w (recovery: re-enable on the cluster link or correct the migration state file before re-running)", offsetSyncEnableKey, config.ClusterLinkName, err)
	}

	s.reporter.Success("%s set to false on cluster link %s", offsetSyncEnableKey, config.ClusterLinkName)
	return nil
}

// restoreOffsetSyncAfterRollback restores the consumer.offset.* config the
// pause flipped, as the second half of the abort_fence rollback. Soft-fail:
// the unfence already succeeded and a restore error must not undo it — the
// flipped marker stays set so the restore remains owed. No-op when nothing
// was flipped (e.g. the pause failed before its AlterConfigs, or a drift
// refusal), which also keeps externally-set config untouched.
func (s *MigrationActions) restoreOffsetSyncAfterRollback(
	config *MigrationConfig,
	restAuth clusterlink.Authenticator,
	persist func() error,
) {
	clCfg := BuildClusterLinkConfig(config, restAuth)
	restoreOffsetSync(s.clusterLinkService, clCfg, config, persist, "Gateway unfenced but")
}

// VerifyFence verifies the fence held: source offsets must be stable, because
// an increasing offset after fencing indicates a producer bypassing the
// gateway. When detection is disabled (DetectUnroutedProducersDuration == 0)
// the step succeeds immediately so the FSM still records fence_verified.
//
// detectUnroutedProducers wraps ErrUnroutedProducers only for a real
// detection; a network/fetch error propagates as-is. Either way we just
// return it — restoring traffic (unfencing the gateway) is the state
// machine's job on the abort_fence rollback transition, which the
// orchestrator triggers only for ErrUnroutedProducers.
func (s *MigrationActions) VerifyFence(ctx context.Context, config *MigrationConfig) error {
	if config.DetectUnroutedProducersDuration <= 0 {
		slog.Debug("⏭️ unrouted producer detection disabled, skipping")
		s.reporter.Detail("Detection disabled (spec.defaultPolicies.detectUnroutedProducersDuration=0) — skipping check")
		return nil
	}

	if s.sourceOffset == nil {
		return fmt.Errorf("source offset service is required for unrouted producer detection")
	}

	if err := s.detectUnroutedProducers(ctx, config.Topics, config.DetectUnroutedProducersDuration); err != nil {
		return err
	}
	s.reporter.Success("Source offsets stable — no unrouted producers detected")
	return nil
}

// PromoteTopics polls offsets and promotes mirror topics that reach zero lag
func (s *MigrationActions) PromoteTopics(ctx context.Context, config *MigrationConfig, restAuth clusterlink.Authenticator) error {
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
	sweepFailures := 0

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
			slog.Warn("⚠️ offset sweep failed, retrying on next tick",
				"attempt", sweepFailures, "maxAttempts", maxConsecutiveSweepFailures, "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.promotePollInterval):
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
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.promotePollInterval):
				continue
			}
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

		promoteResponse, err := s.clusterLinkService.PromoteMirrorTopics(ctx, clusterLinkConfig, topicsToPromote)
		if err != nil {
			return fmt.Errorf("failed to promote mirror topics: %w", err)
		}

		for _, topic := range promoteResponse.Data {
			if topic.ErrorCode != 0 {
				retryCount[topic.MirrorTopicName]++
				s.reporter.line(fmt.Sprintf("   %s Topic %s promotion error (attempt %d/%d): %s",
					color.RedString("✗"), topic.MirrorTopicName, retryCount[topic.MirrorTopicName], maxPromoteRetries, topic.ErrorMessage))
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

		slog.Debug("waiting for promotion to complete before next check", "pollInterval", s.promotePollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.promotePollInterval):
		}
	}
}

// SwitchGateway derives the switch RoutePatch from config.SwitchoverYAML's
// streamingDomain fragment for config.Route (see deriveSwitchRoutePatch) —
// patches it in, confirms the operator accepted the new spec, then waits for
// it to report the gateway as Ready. The wait uses the same
// no-deadline-by-default behavior as FenceGateway.
//
// Because the initial CR is unfenced and its routes already carry pre-staged
// ("redundant") auth for the target domain (proved by migplan.Reconcile's
// redundant-auth check at init), this one patch yields unfenced +
// target-domain + target-auth with no secret or auth change at cutover —
// there is no separately-authored switchover CR to apply.
//
// The acceptance check is what stops this reporting a completed migration for a
// switchover the operator refused — the failure mode described on
// waitForGatewayAccepted, hit on 2026-07-27 while setting up the live-cluster
// e2e test infrastructure.
func (s *MigrationActions) SwitchGateway(ctx context.Context, config *MigrationConfig) error {
	slog.Debug("switching gateway", "gateway", config.InitialCrName, "namespace", config.K8sNamespace)

	// A no-op if FenceGateway already resolved capability earlier in this
	// process (the normal case); only load-bearing for a resume that jumps
	// straight to switch in a fresh process (e.g. every earlier step already
	// completed in a prior run).
	if err := s.ensureGatewayCapability(ctx, config); err != nil {
		return err
	}

	switchRP, err := deriveSwitchRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build switch route patch: %w", err)
	}

	applied, err := s.patchGatewayRoute(ctx, config, switchRP, "switchover")
	if err != nil {
		return fmt.Errorf("failed to apply switchover gateway CR: %w", err)
	}
	slog.Debug("switchover gateway CR applied")
	s.reporter.Success("Switchover gateway CR applied")

	if err := s.waitForGatewayAccepted(ctx, config, "switchover"); err != nil {
		return err
	}

	if err := s.verifyGatewayTransition(ctx, config, applied, "switchover"); err != nil {
		return err
	}

	slog.Debug("gateway switchover complete")
	s.reporter.Success("Gateway switchover complete")
	return nil
}

// printPodRolloutProgress renders WaitForGatewayPods progress. Mirrors
// gateway.TransitionVerifier's own printGatewayReadinessProgress but reports
// the pod-replacement view — how many new pods are Ready and how many old
// (pre-fence) pods still remain — since the fence wait now gates on the old
// pods being gone, not just new-pod readiness. Unique to migration: TBM's
// Fence has no equivalent pod-UID capture (see FenceGateway's doc comment).
func (s *MigrationActions) printPodRolloutProgress(p gateway.PodRolloutProgress) {
	if !p.RolloutDetected {
		s.reporter.Success("No pod restart required")
		return
	}
	s.reporter.Detail("%d/%d new pods ready, %d old pods remaining",
		p.NewPodsReady, p.InitialPodCount, p.OldPodsRemaining)
}
