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

// Policy is the conversion's execute-time policies; Workers is already resolved to its default.
type Policy struct {
	// DetectUnroutedCommitsDuration is the gap between verify_fence's two snapshots; 0 skips the check.
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

// missing names the unset Dependencies fields a conversion cannot run without.
func (d Dependencies) missing() []string {
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
	return missing
}

// missingDependenciesError is nil for an empty list, else the conversion's missing-dependencies error.
func missingDependenciesError(missing []string) error {
	if len(missing) > 0 {
		return fmt.Errorf("the route conversion is missing dependencies: %s", strings.Join(missing, ", "))
	}
	return nil
}

// validate reports the Dependencies and the gateway client a conversion cannot run without.
func (a *D2SActions) validate() error {
	missing := a.deps.missing()
	if a.gw == nil || !a.gw.HasClient() {
		missing = append(missing, "GatewayService")
	}
	return missingDependenciesError(missing)
}

// D2SActions holds the business logic behind each transition.
type D2SActions struct {
	reporter *reporter
	deps     Dependencies

	// gw applies and confirms every gateway patch, and owns the capability, expected generation and
	// start-of-run pod bookkeeping those need.
	gw *gateway.Transitioner

	// handoff is verify_fence's last snapshot (2, or 1 when the direct-commit check is skipped),
	// restricted to the in-scope groups, for sync_offsets in the same run. nil until verify_fence
	// passes in this process; sync_offsets never reads the source itself.
	handoff groupoffsets.Snapshot
}

// NewD2SActions creates the conversion's actions over gatewayService and deps.
func NewD2SActions(gatewayService gateway.TransitionClient, deps Dependencies, opts ...gateway.TransitionerOption) *D2SActions {
	if deps.Wait == nil {
		deps.Wait = groupoffsets.Sleep
	}
	if deps.Out == nil {
		deps.Out = os.Stdout
	}
	a := &D2SActions{reporter: newReporter(), deps: deps}
	// The reporter is read at call time: tests replace a.reporter after construction.
	a.gw = gateway.NewTransitioner(gatewayService, func() gateway.Reporter { return a.reporter }, opts...)
	return a
}

// target names the gateway this run drives. Probe derives the fenced and switched CRs the capability detector
// needs, since it must see the spec.hotReload either one would put in force.
func (a *D2SActions) target(config *migration.MigrationConfig) gateway.Target {
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
	if res.Mode != reconcile.ModeConvert {
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
	a.gw.SetExpectedGeneration(res.GatewayGeneration)
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
	fenceRP, err := deriveFenceRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build fence route patch: %w", err)
	}
	res := a.gw.Apply(ctx, a.target(config), gateway.Transition{
		Step:    "fence",
		Patch:   fenceRP,
		What:    "fenced gateway CR",
		Applied: "Fenced gateway CR applied (every topic on the route blocked)",
	})
	if res.Landed && res.Err != nil {
		return fmt.Errorf("%w: %w", migration.ErrFenceUnconfirmed, res.Err)
	}
	if res.Err != nil {
		return res.Err
	}
	a.reporter.Success("Gateway fenced and ready")
	return nil
}

// unfenceGateway sets the route's rules to reconcile's rollback target (the start-of-run rules without kcp's
// convert fence) and confirms it. Called by onAbortFence and by handleStepFailure's direct paths. It resolves
// the gateway capability first, like Fence: a run can reach a rollback without this process having fenced.
func (a *D2SActions) unfenceGateway(ctx context.Context, config *migration.MigrationConfig) error {
	unfenceRP, err := deriveUnfenceRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build unfence route patch: %w", err)
	}
	return a.gw.Apply(ctx, a.target(config), gateway.Transition{
		Step:          "unfence",
		Patch:         unfenceRP,
		RestoresStart: true,
		What:          "unfenced gateway CR",
		Applied:       "Unfenced gateway CR applied",
	}).Err
}

// ErrSwitchUnconfirmed marks a switch whose patch reached the Gateway CR but could not be confirmed (the
// operator rejected or never accepted it, the stored configId could not be read back, or the pods never
// reported it). The CR already holds the static route while the pods may still serve the fenced dynamic route.
// A re-run's reconcile checks the pods before doing anything: it refuses while they don't serve the CR, and
// finishes once they do. It is migration.ErrSwitchUnconfirmed, the sentinel every FSM marks its switch with.
var ErrSwitchUnconfirmed = migration.ErrSwitchUnconfirmed

// Switch runs the switch transition: one whole-route replace with reconcile's converted route (singular
// streamingDomain bound to the target, no rules tree, so no fence), confirmed on every gateway pod. It is the
// only point of no return; a failure keeps the fence (see handleStepFailure). Once the patch has reached the
// cluster every failure is marked ErrSwitchUnconfirmed, mirroring Fence's migration.ErrFenceUnconfirmed.
func (a *D2SActions) Switch(ctx context.Context, config *migration.MigrationConfig) error {
	switchRP, err := deriveSwitchRoutePatch(config)
	if err != nil {
		return fmt.Errorf("failed to build switch route patch: %w", err)
	}
	res := a.gw.Apply(ctx, a.target(config), gateway.Transition{
		Step:    "switchover",
		Patch:   switchRP,
		What:    "switchover gateway CR",
		Applied: "Static route applied",
	})
	if res.Landed && res.Err != nil {
		return fmt.Errorf("%w: %w", ErrSwitchUnconfirmed, res.Err)
	}
	if res.Err != nil {
		return res.Err
	}
	a.reporter.Success("Gateway switchover complete — the route is static on its target domain")
	return nil
}
