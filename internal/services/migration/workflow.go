package migration

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

// Bounds for the per-pod gateway /config wait.
//
// The poll interval is deliberately tighter than the 5s rollout polls: the
// window this wait closes is small — pods were measured applying a hot reload
// ~7.2s after the apply, with a rejection published at ~5.8s — so a coarse tick
// would dominate the measurement it exists to make.
const (
	gatewayConfigPollInterval = 2 * time.Second

	// defaultGatewayConfigTimeout applies when no explicit rolloutTimeout is
	// set. See MigrationActions.gatewayConfigTimeout for why this wait is
	// bounded when the rollout waits are not.
	defaultGatewayConfigTimeout = 90 * time.Second
)

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
	// gatewayConfigPort and gatewayConfigTimeout tune the per-pod GET /config
	// verification. Zero values fall back — see resolveGatewayConfigPort and
	// resolveGatewayConfigTimeout — so an unconfigured MigrationActions behaves
	// exactly as it did before the flags existed.
	gatewayConfigPort    string
	gatewayConfigTimeout time.Duration
	reporter             *reporter // user-facing terminal output
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

// SetPromoteBatchSize caps how many mirror topics are promoted per batch during
// PromoteTopics. A value of 0 (the default) means unlimited — all zero-lag
// topics are promoted at once. When set (>0), each batch is promoted and fully
// confirmed STOPPED before the next batch is submitted.
func (s *MigrationActions) SetPromoteBatchSize(n int) {
	s.promoteBatchSize = n
}

// SetGatewayConfigPort sets the port serving the gateway's GET /config endpoint.
// An empty string keeps gateway.DefaultGatewayConfigPort.
func (s *MigrationActions) SetGatewayConfigPort(port string) {
	s.gatewayConfigPort = port
}

// SetGatewayConfigTimeout sets the deadline for the per-pod /config wait. A
// value of 0 falls back to the rollout timeout, then to a built-in default —
// this wait is always bounded, unlike the rollout waits.
func (s *MigrationActions) SetGatewayConfigTimeout(d time.Duration) {
	s.gatewayConfigTimeout = d
}

func (s *MigrationActions) Initialize(
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

	// Validate all three gateway CRs are consistent, and that the secrets they
	// reference exist. Report what was actually verified rather than a bare tick:
	// the live secret check can legitimately be skipped (no RBAC to read
	// secrets), and claiming a check ran when it did not is how a missing
	// secretRef reached a cutover in the first place.
	validation, err := s.gatewayService.ValidateGatewayCRs(ctx, config.K8sNamespace, config.InitialCrName, config.InitialCrYAML, config.FencedCrYAML, config.SwitchoverCrYAML)
	for _, warning := range validation.Warnings {
		s.reporter.warn("%s", warning)
	}
	if err != nil {
		return fmt.Errorf("gateway CR validation failed: %w", err)
	}
	slog.Debug("gateway CRs validated", "secretRefsChecked", validation.SecretRefsChecked, "secretCheckSkipped", validation.SecretCheckSkipped)

	switch {
	case validation.SecretCheckSkipped != "":
		// A check that could not run gets ⚠️ and a Warn in kcp.log, not a green
		// tick at Info. An operator scanning a wall of ticks minutes before
		// cutover must be able to see that the live check never happened — that
		// is the whole premise of this validation.
		s.reporter.warn("Gateway CRs validated, but secret references were NOT checked: %s", validation.SecretCheckSkipped)
	case validation.SecretRefsChecked > 0:
		s.reporter.success("Gateway CRs validated (%d secret reference(s) present in %s)", validation.SecretRefsChecked, config.K8sNamespace)
	default:
		s.reporter.success("Gateway CRs validated (no secret references)")
	}

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
	s.reporter.success("Cluster link validated (%d mirror topics active)", len(clusterLinkTopics))

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
		s.reporter.success("Cluster link %s=true (pause-on-execute intent recorded)", offsetSyncEnableKey)
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
func (s *MigrationActions) CheckLags(
	ctx context.Context,
	config *MigrationConfig,
	lagThreshold int64,
	clusterApiKey, clusterApiSecret string,
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
	var (
		wg                 sync.WaitGroup
		source, dest       map[string]map[int32]int64
		sourceErr, destErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		source, sourceErr = s.sourceOffset.GetMany(ctx, topics)
	}()
	go func() {
		defer wg.Done()
		dest, destErr = s.destinationOffset.GetMany(ctx, topics)
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

// waitForGatewayAccepted blocks until the Confluent operator has accepted the
// gateway CR just applied. Every tier's proof is downstream of that, so this
// runs after each apply and before any of them.
//
// The Deployment-based proofs only ever look at the apps/v1 Deployment. When the
// operator rejects a CR it never touches the Deployment, so the Deployment sits
// complete and healthy running the *previous* generation's pods — the readiness
// wait's detection window expires, reports "No pod restart required" and returns
// nil. That is how a switchover whose CR referenced a missing secret was
// reported as a completed migration while the gateway stayed fenced and every
// client stayed blocked (hit on 2026-07-27 while setting up the live-cluster e2e
// infrastructure). The per-pod /config proof gains less, since it would
// eventually time out on the same rejection, but gains the operator's own
// message instead of a bare "no pod reported the new configId".
//
// step names the phase for the error message ("fence", "switchover",
// "unfence"). A rejection is returned as-is: it already carries the operator's
// reason and message, and callers can errors.As it for
// *gateway.GatewayRejection.
func (s *MigrationActions) waitForGatewayAccepted(ctx context.Context, config *MigrationConfig, apply gatewayApply, step string) error {
	s.reporter.detail("Waiting for gateway reconcile...")
	slog.Debug("waiting for gateway acceptance", "step", step, "gateway", config.InitialCrName, "tier", string(apply.tier), "rolloutTimeout", s.rolloutTimeout)

	err := s.gatewayService.WaitForGatewayAccepted(ctx, config.K8sNamespace, config.InitialCrName, apply.conditionsBefore, 2*time.Second, s.rolloutTimeout)
	if err == nil {
		return nil
	}

	if s.reportGatewayRejection(config, err, step) {
		return err
	}
	return fmt.Errorf("failed waiting for gateway reconcile during %s: %w", step, err)
}

// reportGatewayRejection prints where to look when the operator refused a spec,
// reporting whether err was in fact a rejection.
//
// The rejection itself is rendered once upstream from the returned error; this
// only adds what the error cannot carry — how to go look. Both the acceptance
// wait and the per-pod /config wait can surface one, so the hint lives here
// rather than at either call site.
func (s *MigrationActions) reportGatewayRejection(config *MigrationConfig, err error, step string) bool {
	var rejected *gateway.GatewayRejection
	if !errors.As(err, &rejected) {
		return false
	}
	s.reporter.remediation("Confluent operator rejected the %s gateway spec. Inspect its view of the gateway:\n"+
		"   kubectl -n %s get gateway %s -o jsonpath='{.status.conditions}'", step, config.K8sNamespace, config.InitialCrName)
	return true
}

// FenceGateway applies the fenced gateway CR YAML to block traffic and then
// proves the fence is actually in effect.
//
// How it proves that depends on the gateway. With spec.hotReload.enabled the
// config applies in place: no pod restart, no rollout, no Deployment generation
// bump. A readiness wait on such a gateway reports "no pod restart required"
// and passes without the fence having reached a single pod, and
// observedGeneration goes true seconds before the pods serve the new config
// (measured ~1.2s after apply against a rejection CFK only published at +5.8s).
// So verification is selected by tier, not by whether unrouted-producer
// detection happens to be enabled — the latter was never the right axis.
//
// The wait runs without a deadline by default on the rollout path — the
// operator drives convergence and the user can Ctrl-C if a rollout wedges. An
// optional per-workflow rolloutTimeout caps it (via SetRolloutTimeout); the
// per-pod config wait is bounded either way, see gatewayConfigTimeout.
func (s *MigrationActions) FenceGateway(ctx context.Context, config *MigrationConfig) error {
	slog.Debug("fencing gateway", "gateway", config.InitialCrName, "namespace", config.K8sNamespace)

	apply, err := s.prepareGatewayApply(ctx, config, config.FencedCrYAML, &config.FenceConfigId)
	if err != nil {
		return fmt.Errorf("failed to prepare the fenced gateway CR: %w", err)
	}
	tier := apply.tier

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

	var oldPodUIDs map[k8stypes.UID]struct{}
	if detecting {
		var err error
		oldPodUIDs, err = s.gatewayService.GetGatewayPodUIDs(ctx, config.K8sNamespace, config.InitialCrName)
		if err != nil {
			return fmt.Errorf("failed to capture gateway pods before fencing: %w", err)
		}
	}

	if err := s.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.InitialCrName, apply.crYAML); err != nil {
		return fmt.Errorf("failed to apply fenced gateway CR: %w", err)
	}
	slog.Debug("fenced gateway CR applied")
	s.reporter.success("Fenced gateway CR applied")

	// Gate the Deployment-based proofs on the operator having accepted the fenced
	// spec. Neither can tell "nothing needed to change" apart from "the operator
	// refused the spec" on its own — see waitForGatewayAccepted.
	//
	// The per-pod proof is deliberately not gated on it. /config is a strictly
	// stronger statement about the same apply, it is bounded, and it fast-fails on
	// the operator's own rejection from inside its poll loop. The acceptance wait
	// is unbounded by default and cannot recognise a rejection that leaves a
	// condition's status unchanged, so putting it in front would let the weaker
	// signal hang the migration ahead of the only sound gate.
	if tier != gateway.TierPerPodConfigID {
		if err := s.waitForGatewayAccepted(ctx, config, apply, "fence"); err != nil {
			return err
		}
	}

	slog.Debug("waiting for the fence to take effect", "tier", string(tier), "rolloutTimeout", s.rolloutTimeout, "detecting", detecting)

	switch tier {
	case gateway.TierPerPodConfigID:
		s.reporter.detail("Verifying every gateway pod applied the fence...")
		if err := s.gatewayService.WaitForGatewayConfigApplied(ctx, config.K8sNamespace, config.InitialCrName,
			apply.configID, apply.conditionsBefore, s.resolveGatewayConfigPort(),
			gatewayConfigPollInterval, s.resolveGatewayConfigTimeout(), s.printConfigApplyProgress); err != nil {
			s.reportGatewayRejection(config, err, "fence")
			return fmt.Errorf("failed verifying gateway pods applied the fence: %w", err)
		}

	case gateway.TierHotReloadOnly:
		// Hot reload is on, so nothing below proves the pods applied anything,
		// and this CFK cannot give us a per-pod handle to ask with. The
		// acceptance gate above is the whole of the verification here, so say so
		// plainly rather than reporting a success that was never verified.
		s.reporter.warn("This gateway hot-reloads config changes but its CFK version does not support spec.configId, so kcp cannot confirm each pod applied the fence. Upgrade to CFK %s or later for per-pod verification.", gateway.MinCFKVersionForConfigID)

	default: // gateway.TierPodRollout
		s.reporter.detail("Waiting for gateway readiness...")
		// With detection on, wait until the old unfenced pods are gone, not
		// just until the new pod is Ready — see the comment above. The
		// acceptance gate above is what keeps this trustworthy: otherwise a slow
		// operator reconcile could let the detection window expire before the
		// rollout even starts, concluding "no restart required" while the old,
		// still-unfenced pod is live.
		if detecting {
			if err := s.gatewayService.WaitForGatewayPods(ctx, config.K8sNamespace, config.InitialCrName, oldPodUIDs, 5*time.Second, s.rolloutTimeout, s.printPodRolloutProgress); err != nil {
				return fmt.Errorf("failed waiting for gateway pod rollout: %w", err)
			}
		} else {
			if err := s.gatewayService.WaitForGatewayReady(ctx, config.K8sNamespace, config.InitialCrName, 5*time.Second, s.rolloutTimeout, s.printGatewayReadinessProgress); err != nil {
				return fmt.Errorf("failed waiting for gateway readiness: %w", err)
			}
		}
	}

	// On the hot-reload tiers the pod drain is not the primary gate, but it is
	// still worth running when detection was requested. A fenced CR that also
	// changes something non-hot-reloadable rolls the pods anyway, and a
	// terminating pod keeps serving traffic behind the Service while /config no
	// longer counts it — precisely the gap detection exists to close. When no
	// rollout happened this reports "no pod restart required" and costs only
	// its detection window.
	if detecting && tier != gateway.TierPodRollout {
		if err := s.gatewayService.WaitForGatewayPods(ctx, config.K8sNamespace, config.InitialCrName, oldPodUIDs, 5*time.Second, s.rolloutTimeout, s.printPodRolloutProgress); err != nil {
			return fmt.Errorf("failed waiting for gateway pod rollout: %w", err)
		}
	}

	slog.Debug("gateway fenced and ready")
	s.reporter.success("Gateway fenced and ready")
	return nil
}

// detectGatewayTier classifies how a config change to this gateway can be
// verified, degrading rather than aborting.
//
// A classification failure must not stop a fence: the fallback the detector
// returns is never worse than not knowing, and on the pod-rollout fallback it is
// exactly the behaviour that shipped before tier selection existed. But it is a
// real loss of assurance, so it is reported rather than swallowed.
func (s *MigrationActions) detectGatewayTier(ctx context.Context, config *MigrationConfig, candidateYAML []byte) gateway.VerificationTier {
	tier, err := s.gatewayService.DetectGatewayVerificationTier(ctx, config.K8sNamespace, config.InitialCrName, config.InitialCrYAML, candidateYAML)
	if err != nil {
		slog.Warn("⚠️ could not determine how to verify the gateway config change", "gateway", config.InitialCrName, "fallbackTier", string(tier), "error", err)
		s.reporter.warn("Could not determine how to verify this gateway's config change (%v); falling back to %s verification.", err, tier)
		return tier
	}
	slog.Debug("selected gateway verification tier", "gateway", config.InitialCrName, "tier", string(tier))
	return tier
}

// stampConfigID injects a fresh spec.configId when the tier supports per-pod
// verification, returning the CR to apply and the id to verify against.
//
// On every other tier the CR is returned untouched and the id is empty. That is
// not merely an optimisation: with hot reload off, CFK folds configId into the
// pod-template config-revision-hash, so stamping one rolls every gateway pod —
// measured as a full rolling restart, which would turn an idempotent re-apply
// into a client-visible outage.
//
// existingID is the revision a previous run of this same stage recorded, if any.
// Reusing it makes a resume re-apply byte-identical bytes, which CFK treats as a
// no-op; that is only sound because the gateway CRs are captured at init and
// never re-read, so an id can never be paired with changed content.
func (s *MigrationActions) stampConfigID(tier gateway.VerificationTier, crYAML []byte, existingID string) ([]byte, string, error) {
	if !tier.InjectsConfigID() {
		return crYAML, "", nil
	}

	configID := existingID
	if configID == "" {
		configID = gateway.GenerateConfigID()
	} else {
		slog.Debug("reusing the configId recorded by an earlier run", "configId", configID)
	}

	stamped, err := gateway.InjectConfigID(crYAML, configID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to stamp configId %q: %w", configID, err)
	}

	slog.Debug("stamped configId on gateway CR", "configId", configID)
	return stamped, configID, nil
}

// snapshotGatewayConditions captures the gateway's condition transition times
// before an apply, so that a failure condition seen afterwards can be told from
// one that was already there.
//
// Every tier needs this, not just the per-pod one: the acceptance gate reads
// conditions on all of them. A failure is not fatal — without a baseline the
// waits disable their rejection checks and fall back to /config and the timeout,
// which costs latency on a rejected apply but never correctness.
func (s *MigrationActions) snapshotGatewayConditions(ctx context.Context, config *MigrationConfig) gateway.ConditionSnapshot {
	snapshot, err := s.gatewayService.SnapshotGatewayConditions(ctx, config.K8sNamespace, config.InitialCrName)
	if err != nil {
		slog.Warn("⚠️ could not snapshot gateway conditions; kcp cannot recognise a rejected config change, so the waits fall back to bounded timeouts rather than the operator's own error", "gateway", config.InitialCrName, "error", err)
		return nil
	}
	return snapshot
}

// resolveGatewayConfigTimeout bounds the per-pod /config wait, most specific
// setting first: an explicit --gateway-config-timeout, then --rollout-timeout,
// then a built-in default.
//
// Note this wait is bounded even when the rollout waits are not. Unlike a pod
// rollout, a hot reload that has not landed is broken rather than slow:
// convergence was measured at ~7s across pods, and the licence-gate failure mode
// (CFK promotes the shared ConfigMap but the gateway's file watcher never starts)
// never lands at all. An unbounded wait there would hang the migration with no
// signal at all.
func (s *MigrationActions) resolveGatewayConfigTimeout() time.Duration {
	if s.gatewayConfigTimeout > 0 {
		return s.gatewayConfigTimeout
	}
	if s.rolloutTimeout > 0 {
		return s.rolloutTimeout
	}
	return defaultGatewayConfigTimeout
}

// resolveGatewayConfigPort returns the port serving the gateway's /config
// endpoint. Configurable because the contract does not fix it, and because it is
// not a declared containerPort — nothing in Kubernetes can be asked what it is.
func (s *MigrationActions) resolveGatewayConfigPort() string {
	if s.gatewayConfigPort != "" {
		return s.gatewayConfigPort
	}
	return gateway.DefaultGatewayConfigPort
}

// gatewayApply carries what a tier-aware apply needs from preparation through
// to verification: the tier that was detected, the CR bytes to send (which may
// differ from the caller's if a configId was stamped), the id to verify against,
// and the condition baseline captured before the apply.
type gatewayApply struct {
	tier             gateway.VerificationTier
	crYAML           []byte
	configID         string
	conditionsBefore gateway.ConditionSnapshot
}

// prepareGatewayApply classifies the gateway, stamps a configId when the tier
// supports one, and captures the pre-apply condition baseline.
//
// The tier is re-detected per apply rather than resolved once per migration.
// That costs one dry-run round trip (~120ms measured) and buys a verdict on the
// exact document about to be sent — which matters because the fenced, switchover
// and initial CRs are three different documents, and only the one being applied
// can tell us whether this cluster will keep a configId on it.
//
// persistedID points at the MigrationConfig field recording this stage's config
// revision. It is read before stamping, so a resume re-verifies the revision the
// previous run applied, and written after, so the record survives to the next
// run. Passing the field by pointer keeps the stage-to-field mapping at the call
// site, where it is obvious which apply is being prepared.
func (s *MigrationActions) prepareGatewayApply(ctx context.Context, config *MigrationConfig, crYAML []byte, persistedID *string) (gatewayApply, error) {
	tier := s.detectGatewayTier(ctx, config, crYAML)

	stamped, configID, err := s.stampConfigID(tier, crYAML, *persistedID)
	if err != nil {
		return gatewayApply{}, err
	}

	// Never clear an existing record. A tier that stopped supporting configId
	// mid-migration (a CFK downgrade) yields an empty id here, and erasing what
	// an earlier run verified would lose support-facing history for no gain.
	if configID != "" {
		*persistedID = configID
	}

	return gatewayApply{
		tier:             tier,
		crYAML:           stamped,
		configID:         configID,
		conditionsBefore: s.snapshotGatewayConditions(ctx, config),
	}, nil
}

// verifyGatewayApply blocks until an applied gateway CR is proven to be in
// effect, choosing the proof by tier.
//
// rollsPods says whether this particular change replaces the pods regardless of
// hot reload — true for the switchover CR, which changes streamingDomains,
// secretStores and sometimes podTemplate, none of them hot-reloadable. When it
// is true the readiness wait runs first so the pod set has settled before the
// per-pod check asks the pods what they are serving. That ordering is safe
// because configId lives in the shared ConfigMap and survives a restart: a
// replaced pod comes up already reporting the new id, which makes /config a
// superset that covers the roll path as well as the in-place one.
//
// stage names the apply for error messages ("switchover", "unfence"). All three
// gateway applies could otherwise fail with the same bare
// "failed waiting for gateway readiness", which says nothing about which one.
func (s *MigrationActions) verifyGatewayApply(ctx context.Context, config *MigrationConfig, apply gatewayApply, rollsPods bool, stage string) error {
	slog.Debug("verifying gateway apply", "stage", stage, "tier", string(apply.tier), "rollsPods", rollsPods, "rolloutTimeout", s.rolloutTimeout)

	// The Deployment-based proofs are meaningless until the operator has taken the
	// spec — see waitForGatewayAccepted. The per-pod proof supersedes it and is
	// deliberately not gated on it; see the same reasoning in FenceGateway.
	if apply.tier != gateway.TierPerPodConfigID {
		if err := s.waitForGatewayAccepted(ctx, config, apply, stage); err != nil {
			return err
		}
	}

	if rollsPods || apply.tier == gateway.TierPodRollout {
		s.reporter.detail("Waiting for gateway readiness...")
		if err := s.gatewayService.WaitForGatewayReady(ctx, config.K8sNamespace, config.InitialCrName, 5*time.Second, s.rolloutTimeout, s.printGatewayReadinessProgress); err != nil {
			return fmt.Errorf("failed waiting for gateway readiness after %s: %w", stage, err)
		}
	}

	switch apply.tier {
	case gateway.TierPerPodConfigID:
		s.reporter.detail("Verifying every gateway pod applied the new config...")
		if err := s.gatewayService.WaitForGatewayConfigApplied(ctx, config.K8sNamespace, config.InitialCrName,
			apply.configID, apply.conditionsBefore, s.resolveGatewayConfigPort(),
			gatewayConfigPollInterval, s.resolveGatewayConfigTimeout(), s.printConfigApplyProgress); err != nil {
			s.reportGatewayRejection(config, err, stage)
			return fmt.Errorf("failed verifying gateway pods applied the %s: %w", stage, err)
		}

	case gateway.TierHotReloadOnly:
		// The acceptance gate above is as far as verification reaches on this
		// tier: the config applied in place, and this CFK offers no per-pod handle
		// to ask any pod what it is actually serving.
		s.reporter.warn("This gateway hot-reloads config changes but its CFK version does not support spec.configId, so kcp cannot confirm each pod applied the %s. Upgrade to CFK %s or later for per-pod verification.", stage, gateway.MinCFKVersionForConfigID)
	}

	return nil
}

// strippedInitialCR removes the server-managed metadata that server-side apply
// refuses, so the CR fetched from the cluster can be re-applied.
//
// This is not cosmetic: client-go's dynamic Apply rejects an object carrying
// managedFields client-side, before any request leaves the process, so an
// unstripped CR fails deterministically — for the tier probe's dry run exactly
// as much as for the real apply.
func strippedInitialCR(initialCrYAML []byte) ([]byte, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(initialCrYAML, &obj); err != nil {
		return nil, fmt.Errorf("failed to parse initial CR YAML: %w", err)
	}

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
		return nil, fmt.Errorf("failed to marshal cleaned initial CR YAML: %w", err)
	}
	return cleanYAML, nil
}

// unfenceGateway reapplies the initial gateway CR to restore normal traffic,
// then proves the restore actually reached the pods. Without the verification we
// would report traffic restored while pods are still cycling — or, on a
// hot-reload gateway, while no pod has applied anything at all — and miss
// failures entirely. This is the rollback path, so a false success here hides
// the fact that a migration was not actually rolled back.
//
// Removing the fence from an existing route is a hot-reloadable change (the
// exact inverse of the fence), so no pod roll is expected.
func (s *MigrationActions) unfenceGateway(ctx context.Context, config *MigrationConfig) error {
	// Strip before preparing, not after: the tier probe dry-runs this document,
	// and an unstripped CR is refused client-side. See strippedInitialCR.
	cleanYAML, err := strippedInitialCR(config.InitialCrYAML)
	if err != nil {
		return err
	}

	apply, err := s.prepareGatewayApply(ctx, config, cleanYAML, &config.UnfenceConfigId)
	if err != nil {
		return fmt.Errorf("failed to prepare the initial gateway CR: %w", err)
	}

	if err := s.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.InitialCrName, apply.crYAML); err != nil {
		return fmt.Errorf("failed to apply initial gateway CR: %w", err)
	}
	slog.Debug("initial gateway CR applied")
	s.reporter.success("Initial gateway CR applied")

	return s.verifyGatewayApply(ctx, config, apply, false, "unfence")
}

// detectUnroutedProducers takes two source offset snapshots separated by the
// given duration. If any partition's offset increases between snapshots, it
// means a producer is writing directly to the source cluster (bypassing the
// fenced gateway) and the migration should not proceed.
func (s *MigrationActions) detectUnroutedProducers(ctx context.Context, topics []string, duration time.Duration) error {
	// Snapshot 1 — one batched sweep across all topics.
	slog.Debug("taking first source offset snapshot", "topicCount", len(topics))
	snapshot1, err := s.sourceOffset.GetMany(ctx, topics)
	if err != nil {
		return fmt.Errorf("failed to get source offsets: %w", err)
	}

	// Wait, then snapshot 2
	s.reporter.detail("Monitoring source offsets for %s...", duration)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(duration):
	}

	slog.Debug("taking second source offset snapshot")
	snapshot2, err := s.sourceOffset.GetMany(ctx, topics)
	if err != nil {
		return fmt.Errorf("failed to get source offsets: %w", err)
	}

	var violations []string
	for _, topic := range topics {
		for p, o2 := range snapshot2[topic] {
			// A partition absent from the first snapshot (e.g. created during
			// the window) starts at offset 0, so any data on it was written
			// after fencing — the zero-value baseline flags it.
			o1 := snapshot1[topic][p]
			if o2 > o1 {
				delta := o2 - o1
				rate := float64(delta) / duration.Seconds()
				violations = append(violations, fmt.Sprintf(
					"topic %s partition %d: offset %d → %d (+%d, ~%.0f msg/s)",
					topic, p, o1, o2, delta, rate))
			}
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		return fmt.Errorf("%w:\n  %s\n\nThese producers are bypassing the gateway and writing directly to the source cluster.\nReconfigure them to produce through the migration gateway, then re-run 'kcp migration execute' to resume",
			ErrUnroutedProducers, strings.Join(violations, "\n  "))
	}

	return nil
}

// PauseOffsetSync runs the pause_offset_sync stage: with the operator's
// --pause-consumer-offset-sync opt-in it pauses cluster-link consumer offset
// sync immediately after fencing; otherwise it passes through so the FSM
// still records offset_sync_paused. The already-flipped guard makes resumes
// (and legacy state files whose pause ran pre-FSM) idempotent.
func (s *MigrationActions) PauseOffsetSync(
	ctx context.Context,
	config *MigrationConfig,
	clusterApiKey, clusterApiSecret string,
	persist func() error,
) error {
	if !config.PauseConsumerOffsetSync {
		slog.Debug("⏭️ consumer offset sync pause not requested, skipping")
		s.reporter.detail("Offset-sync pause not requested — skipping")
		return nil
	}
	if config.PauseConsumerOffsetSyncFlipped {
		slog.Info("⏭️ consumer.offset.sync.enable already flipped, skipping pause", "migrationId", config.MigrationId)
		s.reporter.detail("consumer.offset.sync already paused — skipping")
		return nil
	}

	clCfg := BuildClusterLinkConfig(config, clusterApiKey, clusterApiSecret)

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
		return fmt.Errorf("--pause-consumer-offset-sync refused: cluster link %q has no %s key — cannot verify the pre-pause state", config.ClusterLinkName, offsetSyncEnableKey)
	case observed != "true":
		return fmt.Errorf("--pause-consumer-offset-sync refused: %s on cluster link %q is not enabled — either a previous kcp run was interrupted mid-pause before recording it, or the config was changed externally; inspect the cluster link and the migration state file before re-running", offsetSyncEnableKey, config.ClusterLinkName)
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
		s.reporter.detail("Draining consumer offset sync for %s before pausing...", drain)
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

	s.reporter.success("%s set to false on cluster link %s", offsetSyncEnableKey, config.ClusterLinkName)
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
	clusterApiKey, clusterApiSecret string,
	persist func() error,
) {
	clCfg := BuildClusterLinkConfig(config, clusterApiKey, clusterApiSecret)
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
		s.reporter.detail("Detection disabled (--detect-unrouted-producers-duration=0) — skipping check")
		return nil
	}

	if s.sourceOffset == nil {
		return fmt.Errorf("source offset service is required for unrouted producer detection")
	}

	if err := s.detectUnroutedProducers(ctx, config.Topics, config.DetectUnroutedProducersDuration); err != nil {
		return err
	}
	s.reporter.success("Source offsets stable — no unrouted producers detected")
	return nil
}

// PromoteTopics polls offsets and promotes mirror topics that reach zero lag
func (s *MigrationActions) PromoteTopics(ctx context.Context, config *MigrationConfig, clusterApiKey, clusterApiSecret string) error {
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
					s.reporter.success("%s stopped", topic)
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
			s.reporter.detail("Waiting for current batch of %d topic(s) to reach STOPPED...",
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
				s.reporter.detail("Waiting for %d promoted topic(s) to reach STOPPED...",
					len(awaitingStop))
				slog.Debug("waiting for accepted promotions to reach STOPPED",
					"awaitingStop", len(awaitingStop), "pollInterval", s.promotePollInterval)
			} else {
				s.reporter.detail("Waiting for lag to reach zero (%d topics remaining)...",
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
		s.reporter.success("%s confirmed at zero lag",
			color.WhiteString("%d/%d topics", len(topicsToPromote), len(remaining)))
		for _, topic := range topicsToPromote {
			s.reporter.line(fmt.Sprintf("   %s %s  %s %s",
				color.GreenString("↳"),
				color.WhiteString(topic),
				color.CyanString("lag:"),
				color.GreenString("0")))
		}
		s.reporter.detail("Promoting %d mirror topics...", len(topicsToPromote))
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

// SwitchGateway applies the switchover gateway CR YAML to point to Confluent
// Cloud, then proves the switchover reached the pods.
//
// Unlike the fence, this CR changes streamingDomains, secretStores and sometimes
// podTemplate — none of which CFK can hot-reload — so the pods roll even on a
// gateway with spec.hotReload.enabled. A single migration run therefore needs
// both strategies: the readiness wait settles the new pod set, and on a gateway
// that supports spec.configId the per-pod check then confirms what that settled
// set is actually serving. The readiness wait keeps the same
// no-deadline-by-default behaviour as FenceGateway.
func (s *MigrationActions) SwitchGateway(ctx context.Context, config *MigrationConfig) error {
	slog.Debug("switching gateway", "gateway", config.InitialCrName, "namespace", config.K8sNamespace)

	apply, err := s.prepareGatewayApply(ctx, config, config.SwitchoverCrYAML, &config.SwitchoverConfigId)
	if err != nil {
		return fmt.Errorf("failed to prepare the switchover gateway CR: %w", err)
	}

	if err := s.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.InitialCrName, apply.crYAML); err != nil {
		return fmt.Errorf("failed to apply switchover gateway CR: %w", err)
	}
	slog.Debug("switchover gateway CR applied")
	s.reporter.success("Switchover gateway CR applied")

	if err := s.verifyGatewayApply(ctx, config, apply, true, "switchover"); err != nil {
		return err
	}

	slog.Debug("gateway switchover complete")
	s.reporter.success("Gateway switchover complete")
	return nil
}

// printGatewayReadinessProgress renders one line per poll tick combining the
// operator-reported readiness with elapsed time and a pod-readiness snapshot.
// A no-op signal (RolloutDetected=false) is preserved from the previous
// implementation so users see "no pod restart required" when an apply did not
// trigger a rollout.
func (s *MigrationActions) printGatewayReadinessProgress(p gateway.GatewayReadinessProgress) {
	if !p.RolloutDetected {
		s.reporter.success("No pod restart required")
		return
	}
	if p.InitialPodCount > 0 {
		s.reporter.detail("%d/%d pods ready (elapsed %s)", p.PodsReady, p.InitialPodCount, formatElapsed(p.Elapsed))
	} else {
		s.reporter.detail("gateway reconciling (elapsed %s)", formatElapsed(p.Elapsed))
	}
}

// printPodRolloutProgress renders WaitForGatewayPods progress. Mirrors
// printGatewayReadinessProgress but reports the pod-replacement view — how many
// new pods are Ready and how many old (pre-fence) pods still remain — since the
// fence wait now gates on the old pods being gone, not just new-pod readiness.
func (s *MigrationActions) printPodRolloutProgress(p gateway.PodRolloutProgress) {
	if !p.RolloutDetected {
		s.reporter.success("No pod restart required")
		return
	}
	s.reporter.detail("%d/%d new pods ready, %d old pods remaining",
		p.NewPodsReady, p.InitialPodCount, p.OldPodsRemaining)
}

// printConfigApplyProgress renders WaitForGatewayConfigApplied progress — the
// per-pod view of which pods are actually serving the new config.
//
// Deliberately reports counts and never pod names: these lines reach the
// terminal, and a gateway with many replicas would turn a progress line into a
// wall of identifiers.
func (s *MigrationActions) printConfigApplyProgress(p gateway.ConfigApplyProgress) {
	if p.Converged {
		s.reporter.success("All %d gateway pods applied the new config", p.PodsTotal)
		return
	}
	if p.Reason != "" {
		s.reporter.detail("%s (elapsed %s)", p.Reason, formatElapsed(p.Elapsed))
		return
	}
	s.reporter.detail("%d/%d gateway pods applied the new config (elapsed %s)",
		p.PodsApplied, p.PodsTotal, formatElapsed(p.Elapsed))
}

// formatElapsed rounds the elapsed duration to whole seconds so the progress
// line is stable across poll ticks (sub-second jitter would churn the
// rendered string each tick).
func formatElapsed(d time.Duration) string {
	return d.Round(time.Second).String()
}
