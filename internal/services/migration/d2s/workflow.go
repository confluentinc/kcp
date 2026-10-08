package d2s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
)

// convertMode is migplan.Result.Mode for a route conversion.
const convertMode = "convert"

// Policy is the conversion's two execute-time policies, already resolved to their effective values
// (manifest.DefaultPolicies.EffectiveDetectUnroutedCommitsDuration and EffectiveOffsetSyncConcurrency).
type Policy struct {
	// DetectUnroutedCommitsDuration is the wait between verify_fence's two committed-offset snapshots. It must
	// be positive: the direct-commit check can never be skipped.
	DetectUnroutedCommitsDuration time.Duration
	// Workers is the number of offset-fetch and offset-commit workers, each with its own client.
	Workers int
}

// FactsGatherer reads a conversion's facts after the fence: migplan.GatherConvertFacts over this run's
// providers.
type FactsGatherer func(ctx context.Context) (*migplan.ConvertFacts, error)

// GroupStateLister lists the destination's consumer groups with their states, strictly (every broker).
type GroupStateLister func(ctx context.Context) ([]types.ConsumerGroupListing, error)

// Dependencies is everything verify_fence and sync_offsets read or write besides the gateway. The command
// layer builds it from the manifest; tests pass fakes.
type Dependencies struct {
	// Input is the reconcile input (spec.route) the route preconditions are re-run against after the fence.
	Input reconcile.ReconcileInput
	// Gather reads the post-fence facts (verify_fence step 1).
	Gather FactsGatherer
	// SourceGroups lists every source group, strictly, for both snapshots.
	SourceGroups groupoffsets.GroupLister
	// DestinationGroups lists the destination's groups for the second split-brain check.
	DestinationGroups GroupStateLister
	// Fetchers builds one source offset fetcher per worker.
	Fetchers groupoffsets.FetcherFactory
	// Wait waits out the detection window; groupoffsets.Sleep when nil.
	Wait groupoffsets.WaitFunc
	// DestinationTopics lists the destination's non-internal topics afresh, before the high-water-mark sweep.
	DestinationTopics groupoffsets.TopicLister
	// HighWaterMarks sweeps the destination's high-water marks. Its client must never auto-create a topic.
	HighWaterMarks offset.Provider
	// Committers builds one destination committer per worker.
	Committers groupoffsets.CommitterFactory
	// Out receives a verify_fence refusal, rendered as --dry-run renders a report; os.Stdout when nil.
	Out io.Writer
}

// validate reports the dependencies a conversion cannot run without.
func (d Dependencies) validate() error {
	var missing []string
	if d.Input.Route == "" {
		missing = append(missing, "Input.Route")
	}
	if d.Gather == nil {
		missing = append(missing, "Gather")
	}
	if d.SourceGroups == nil {
		missing = append(missing, "SourceGroups")
	}
	if d.DestinationGroups == nil {
		missing = append(missing, "DestinationGroups")
	}
	if d.Fetchers == nil {
		missing = append(missing, "Fetchers")
	}
	if d.DestinationTopics == nil {
		missing = append(missing, "DestinationTopics")
	}
	if d.HighWaterMarks == nil {
		missing = append(missing, "HighWaterMarks")
	}
	if d.Committers == nil {
		missing = append(missing, "Committers")
	}
	if len(missing) > 0 {
		return fmt.Errorf("the route conversion is missing dependencies: %s", strings.Join(missing, ", "))
	}
	return nil
}

// D2SActions holds the business logic behind each transition.
type D2SActions struct {
	reporter *reporter
	deps     Dependencies

	gatewayService gateway.Service
	// gatewayCapability is how gateway transitions are verified for this run; the zero value (VerifyRollout,
	// no configId) is the safe default until ensureGatewayCapability resolves it.
	gatewayCapability gateway.Capability
	// capabilityResolved is true once gatewayCapability was resolved against the live cluster in this
	// process.
	capabilityResolved bool
	// rolloutTimeout bounds the gateway-readiness waits; 0 means no deadline.
	rolloutTimeout time.Duration
	// hotReloadTimeout bounds per-pod configId verification; 0 uses gateway.DefaultHotReloadTimeout.
	hotReloadTimeout time.Duration

	// handoff is verify_fence's second snapshot, restricted to the in-scope groups, for sync_offsets in the
	// same run. nil until verify_fence passes in this process; sync_offsets never reads the source itself.
	handoff groupoffsets.Snapshot
}

// NewD2SActions creates the conversion's actions over gatewayService and deps.
func NewD2SActions(gatewayService gateway.Service, deps Dependencies) *D2SActions {
	if deps.Wait == nil {
		deps.Wait = groupoffsets.Sleep
	}
	if deps.Out == nil {
		deps.Out = os.Stdout
	}
	return &D2SActions{reporter: newReporter(), deps: deps, gatewayService: gatewayService}
}

// SetRolloutTimeout sets the deadline applied to gateway-readiness waits. 0 means no deadline.
func (a *D2SActions) SetRolloutTimeout(d time.Duration) {
	a.rolloutTimeout = d
}

// SetHotReloadTimeout sets the deadline for per-pod configId verification. 0 uses the gateway default.
func (a *D2SActions) SetHotReloadTimeout(d time.Duration) {
	a.hotReloadTimeout = d
}

// Initialize runs the initialize transition: it checks the reconcile result is a feasible conversion with all
// its artifacts and copies them onto config. A refused or malformed result cancels the transition before
// config is touched. A conversion promotes no topics, so Topics, MigrateTopics and AwaitStopped are cleared.
func (a *D2SActions) Initialize(ctx context.Context, config *migration.MigrationConfig, res *migplan.Result) error {
	if res == nil {
		return errors.New("no reconcile result to initialize the route conversion from")
	}
	if res.Refused {
		return fmt.Errorf("reconcile plan refused:\n%s", strings.Join(res.Reasons, "\n"))
	}
	if res.Mode != convertMode {
		return fmt.Errorf("the d2s state machine runs a route conversion only; reconcile planned mode %q", res.Mode)
	}
	var missing []string
	for name, v := range map[string]string{
		"route name": res.Route,
		"gateway CR": res.GatewayYAML,
		"fence":      res.FenceYAML,
		"switch":     res.SwitchoverYAML,
		"rollback":   res.RollbackFenceYAML,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("reconcile planned a route conversion without its %s artifact(s)", strings.Join(missing, ", "))
	}

	config.Topics, config.MigrateTopics, config.AwaitStopped = nil, nil, nil
	config.Route = res.Route
	config.GatewayYAML = res.GatewayYAML
	config.FenceYAML = res.FenceYAML
	config.SwitchoverYAML = res.SwitchoverYAML
	config.RollbackFenceYAML = res.RollbackFenceYAML
	config.RollbackAllowed = res.RollbackAllowed
	config.FencedAtStart = res.FencedAtStart

	a.reporter.Success("Route conversion initialized for route %s", res.Route)
	return nil
}

// Fence runs the fence transition: it sets the route's rules to reconcile's fence artifact (the route's rules
// plus kcp's wildcard convert fence, topicPatterns ['.*']) and confirms the change on every gateway pod. It is
// applied on every run, as TBM's fence is: when an earlier run's fence is already up the patch sets the same
// rules, a no-op in effect. Once the patch has reached the cluster every failure is marked
// migration.ErrFenceUnconfirmed: the fenced rules are live and may hold client traffic, so the orchestrator
// removes them.
func (a *D2SActions) Fence(ctx context.Context, config *migration.MigrationConfig) error {
	if err := a.ensureGatewayCapability(ctx, config); err != nil {
		return fmt.Errorf("failed to resolve gateway capability: %w", err)
	}
	fenceRP, err := deriveFenceRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build fence route patch: %w", err)
	}
	applied, err := a.patchGatewayRoute(ctx, config, fenceRP, "fence")
	if err != nil {
		if errors.Is(err, gateway.ErrApplyUnverified) {
			return fmt.Errorf("%w: failed to apply fenced gateway CR: %w", migration.ErrFenceUnconfirmed, err)
		}
		return fmt.Errorf("failed to apply fenced gateway CR: %w", err)
	}
	a.reporter.Success("Fenced gateway CR applied (every topic on the route blocked)")

	if err := a.waitForGatewayAccepted(ctx, config, "fence"); err != nil {
		return fmt.Errorf("%w: %w", migration.ErrFenceUnconfirmed, err)
	}
	if err := a.verifyGatewayTransition(ctx, config, applied, "fence"); err != nil {
		return fmt.Errorf("%w: %w", migration.ErrFenceUnconfirmed, err)
	}
	a.reporter.Success("Gateway fenced and ready")
	return nil
}

// unfenceGateway sets the route's rules to reconcile's rollback target (the start-of-run rules without kcp's
// convert fence) and confirms it. Called by onAbortFence and by handleStepFailure's direct paths. It resolves
// the gateway capability first, like Fence: a run can reach a rollback without this process having fenced.
func (a *D2SActions) unfenceGateway(ctx context.Context, config *migration.MigrationConfig) error {
	if err := a.ensureGatewayCapability(ctx, config); err != nil {
		return fmt.Errorf("failed to resolve gateway capability: %w", err)
	}
	unfenceRP, err := deriveUnfenceRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build unfence route patch: %w", err)
	}
	applied, err := a.patchGatewayRoute(ctx, config, unfenceRP, "unfence")
	if err != nil {
		return fmt.Errorf("failed to apply unfenced gateway CR: %w", err)
	}
	a.reporter.Success("Unfenced gateway CR applied")
	if err := a.waitForGatewayAccepted(ctx, config, "unfence"); err != nil {
		return err
	}
	return a.verifyGatewayTransition(ctx, config, applied, "unfence")
}

// ErrSwitchUnconfirmed marks a switch whose patch reached the Gateway CR but could not be confirmed (the
// operator rejected or never accepted it, the stored configId could not be read back, or the pods never
// reported it). The CR already holds the static route, so a re-run's reconcile reports "nothing to do" while
// the pods may still run the fenced dynamic route: the operator must fix the Gateway, not re-run.
var ErrSwitchUnconfirmed = errors.New("the static route reached the Gateway CR but could not be confirmed")

// Switch runs the switch transition: one whole-route replace with reconcile's converted route (singular
// streamingDomain bound to the target, no rules tree, so no fence), confirmed on every gateway pod. It is the
// only point of no return; a failure keeps the fence (see handleStepFailure). Once the patch has reached the
// cluster every failure is marked ErrSwitchUnconfirmed, mirroring Fence's migration.ErrFenceUnconfirmed.
func (a *D2SActions) Switch(ctx context.Context, config *migration.MigrationConfig) error {
	if err := a.ensureGatewayCapability(ctx, config); err != nil {
		return fmt.Errorf("failed to resolve gateway capability: %w", err)
	}
	switchRP, err := deriveSwitchRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build switch route patch: %w", err)
	}
	applied, err := a.patchGatewayRoute(ctx, config, switchRP, "switchover")
	if err != nil {
		if errors.Is(err, gateway.ErrApplyUnverified) {
			return fmt.Errorf("%w: failed to apply switchover gateway CR: %w", ErrSwitchUnconfirmed, err)
		}
		return fmt.Errorf("failed to apply switchover gateway CR: %w", err)
	}
	a.reporter.Success("Static route applied")
	if err := a.waitForGatewayAccepted(ctx, config, "switchover"); err != nil {
		return fmt.Errorf("%w: %w", ErrSwitchUnconfirmed, err)
	}
	if err := a.verifyGatewayTransition(ctx, config, applied, "switchover"); err != nil {
		return fmt.Errorf("%w: %w", ErrSwitchUnconfirmed, err)
	}
	a.reporter.Success("Gateway switchover complete — the route is static on its target domain")
	return nil
}
