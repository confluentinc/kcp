package tbm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/looplab/fsm"
)

// ErrUnroutedProducers is returned when verify_fence detects a producer
// bypassing the gateway. The orchestrator catches this to trigger an
// EventAbortFence transition back to initialized. Re-exported from
// offset.ErrUnroutedProducers (the package that actually detects and wraps
// it) so every existing errors.Is call site in this package keeps working
// unchanged.
var ErrUnroutedProducers = offset.ErrUnroutedProducers

// WorkflowStep defines a single step in the TBM workflow: pure FSM topology
// plus an ops-facing Description. Mirrors migration.WorkflowStep.
type WorkflowStep struct {
	Event       string
	Description string
	FromState   string
	ToState     string
}

// canonicalWorkflow is the single ordered source of truth for the TBM
// workflow — the forward transitions the FSM walks on Execute. abort_fence
// (fenced → initialized) is a compensating rollback, not a forward step, so
// it is not listed here — see EventAbortFence and handleStepFailure.
var canonicalWorkflow = []WorkflowStep{
	{EventInitialize, "initializing TBM migration", StateUninitialized, StateInitialized},
	{EventWaitForLags, "checking replication lags", StateInitialized, StateLagsOk},
	{EventFence, "fencing batch", StateLagsOk, StateFenced},
	{EventVerifyFence, "verifying fence", StateFenced, StateFenceVerified},
	{EventPromote, "promoting batch", StateFenceVerified, StatePromoted},
	{EventSwitch, "switching batch", StatePromoted, StateSwitched},
}

// stepHeaders maps a workflow event to the banner the Execute loop prints as
// it walks canonicalWorkflow. Mirrors migration.stepHeaders.
var stepHeaders = map[string]string{
	EventInitialize:  "🔍 Initializing TBM migration...",
	EventWaitForLags: "🔍 Checking replication lags...",
	EventFence:       "🔍 Fencing batch...",
	EventVerifyFence: "🔍 Verifying fence...",
	EventPromote:     "🔍 Promoting batch...",
	EventSwitch:      "🔍 Switching batch...",
}

// ExecutionParams holds the per-run runtime parameters a transition may need.
// It is passed to fsm.Event as the sole argument and read back by callbacks
// via execParamsFromEvent, mirroring migration.ExecutionParams /
// execParamsFromEvent in internal/services/migration/orchestrator.go.
type ExecutionParams struct {
	// ReconcileResult is the plan migplan.Reconcile already computed live,
	// before Execute was invoked. onInitialize validates and copies it onto
	// config; every other callback ignores it today.
	ReconcileResult *migplan.Result
	// LagThreshold is the total replication lag (sum of all partition lags)
	// tolerated before wait_for_lags proceeds. Read by onWaitForLags only.
	LagThreshold int64
	// DetectUnroutedProducersDuration is the monitoring window verify_fence
	// uses to detect a producer bypassing the gateway. 0 disables the check.
	// Read by onVerifyFence only.
	DetectUnroutedProducersDuration time.Duration
	// RestAuth authenticates the destination cluster-link REST surface.
	// Read by onPromote only.
	RestAuth clusterlink.Authenticator
}

// execParamsFromEvent returns the ExecutionParams passed to fsm.Event.
func execParamsFromEvent(e *fsm.Event) ExecutionParams {
	if len(e.Args) > 0 {
		if p, ok := e.Args[0].(ExecutionParams); ok {
			return p
		}
	}
	return ExecutionParams{}
}

// TBMOrchestrator manages the FSM lifecycle and coordinates workflow
// execution. Mirrors migration.MigrationOrchestrator.
type TBMOrchestrator struct {
	config   *migration.MigrationConfig
	fsm      *fsm.FSM
	actions  *TBMActions
	reporter *reporter
}

// NewTBMOrchestrator creates a new TBM orchestrator with injected dependencies.
func NewTBMOrchestrator(
	config *migration.MigrationConfig,
	actions *TBMActions,
) *TBMOrchestrator {
	orchestrator := &TBMOrchestrator{
		config:   config,
		actions:  actions,
		reporter: newReporter(),
	}

	events := make(fsm.Events, 0, len(canonicalWorkflow)+1)
	for _, step := range canonicalWorkflow {
		events = append(events, fsm.EventDesc{
			Name: step.Event,
			Src:  []string{step.FromState},
			Dst:  step.ToState,
		})
	}
	events = append(events, fsm.EventDesc{
		Name: EventAbortFence,
		Src:  []string{StateFenced},
		Dst:  StateInitialized,
	})

	// The FSM always starts at uninitialized on construction. There is no
	// resume position: the command layer calls migplan.Reconcile live on
	// every invocation and hands its fresh *migplan.Result to Execute, which
	// walks canonicalWorkflow from the top and re-applies each step's
	// artifact idempotently (Task 1's FenceYAML/SwitchoverYAML no-op guards
	// make an already-complete migration a side-effect-free walk-through).
	// This is why the old expire_* demotions — which used to re-derive a safe
	// resume point from a point-in-time fence/verification fact after a
	// restart — no longer exist: there is nothing to demote from when every
	// run starts at zero.
	orchestrator.fsm = fsm.NewFSM(
		StateUninitialized,
		events,
		fsm.Callbacks{
			"before_event":               orchestrator.beforeEventCallback,
			"after_event":                orchestrator.afterEventCallback,
			"enter_state":                orchestrator.enterStateCallback,
			"leave_state":                orchestrator.leaveStateCallback,
			"before_" + EventInitialize:  orchestrator.onInitialize,
			"before_" + EventWaitForLags: orchestrator.onWaitForLags,
			"before_" + EventFence:       orchestrator.onFence,
			"before_" + EventVerifyFence: orchestrator.onVerifyFence,
			"before_" + EventPromote:     orchestrator.onPromote,
			"before_" + EventSwitch:      orchestrator.onSwitch,
			"before_" + EventAbortFence:  orchestrator.onAbortFence,
		},
	)

	return orchestrator
}

// Execute runs the full TBM workflow, always from StateUninitialized (see
// NewTBMOrchestrator). res is the reconcile plan the caller already computed
// live for this manifest, on every invocation; onInitialize consumes it.
// lagThreshold is the total replication lag tolerated before wait_for_lags
// proceeds; onWaitForLags consumes it. detectUnroutedProducersDuration is the
// monitoring window verify_fence uses to detect a producer bypassing the
// gateway (0 disables the check); onVerifyFence consumes it. restAuth
// authenticates the destination cluster-link REST surface; onPromote
// consumes it.
func (o *TBMOrchestrator) Execute(ctx context.Context, res *migplan.Result, lagThreshold int64, detectUnroutedProducersDuration time.Duration, restAuth clusterlink.Authenticator) error {
	params := ExecutionParams{ReconcileResult: res, LagThreshold: lagThreshold, DetectUnroutedProducersDuration: detectUnroutedProducersDuration, RestAuth: restAuth}

	for _, step := range canonicalWorkflow {
		if !o.canTransition(step.Event) {
			slog.Debug("skipping already-completed tbm step", "step", step.Description, "event", step.Event)
			continue
		}

		if header, ok := stepHeaders[step.Event]; ok {
			o.reporter.section(header)
		}
		slog.Debug("executing tbm step", "step", step.Description)
		if err := o.fsm.Event(ctx, step.Event, params); err != nil {
			return o.handleStepFailure(ctx, step, err)
		}
		o.reporter.stepDone()
	}

	o.reporter.complete("✅ TBM migration complete!")
	return nil
}

// handleStepFailure maps a failed workflow step to its compensating rollback,
// if any: only a verify_fence failure classified as ErrUnroutedProducers
// triggers abort_fence; every other step failure just returns the wrapped
// error and leaves the FSM at its last good state. Mirrors
// migration.handleStepFailure, minus the ErrFenceUnconfirmed and
// pause_offset_sync branches TBM has no equivalent of.
func (o *TBMOrchestrator) handleStepFailure(ctx context.Context, step WorkflowStep, stepErr error) error {
	stepFailure := fmt.Errorf("failed during %s: %w", step.Description, stepErr)

	if !errors.Is(stepErr, ErrUnroutedProducers) {
		return stepFailure
	}

	if err := o.fsm.Event(ctx, EventAbortFence); err != nil {
		slog.Error("❌ failed to roll back to initialized", "error", err)
		return stepFailure
	}

	return stepFailure
}

func (o *TBMOrchestrator) beforeEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("TBM FSM: before event", "event", e.Event, "src", e.Src, "dst", e.Dst)
}

// afterEventCallback logs every committed transition as a single Info line,
// mirroring migration.afterEventCallback. The FSM's own state (o.fsm.Current(),
// which e.Dst mirrors) is authoritative in-process — there is no config field
// to keep in sync.
func (o *TBMOrchestrator) afterEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("tbm migration state advanced", "event", e.Event, "from", e.Src, "to", e.Dst, "migration_id", o.config.MigrationId)
}

func (o *TBMOrchestrator) enterStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("TBM FSM: entering state", "state", e.Dst)
}

func (o *TBMOrchestrator) leaveStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("TBM FSM: leaving state", "state", e.Src)
}

func (o *TBMOrchestrator) onInitialize(ctx context.Context, e *fsm.Event) {
	p := execParamsFromEvent(e)
	if err := o.actions.Initialize(ctx, o.config, p.ReconcileResult); err != nil {
		e.Cancel(err)
	}
}

func (o *TBMOrchestrator) onWaitForLags(ctx context.Context, e *fsm.Event) {
	p := execParamsFromEvent(e)
	if err := o.actions.WaitForLags(ctx, o.config, p.LagThreshold); err != nil {
		e.Cancel(err)
	}
}

func (o *TBMOrchestrator) onFence(ctx context.Context, e *fsm.Event) {
	if err := o.actions.Fence(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

func (o *TBMOrchestrator) onVerifyFence(ctx context.Context, e *fsm.Event) {
	p := execParamsFromEvent(e)
	if err := o.actions.VerifyFence(ctx, o.config, p.DetectUnroutedProducersDuration); err != nil {
		e.Cancel(err)
	}
}

func (o *TBMOrchestrator) onPromote(ctx context.Context, e *fsm.Event) {
	p := execParamsFromEvent(e)
	if err := o.actions.Promote(ctx, o.config, p.RestAuth); err != nil {
		e.Cancel(err)
	}
}

func (o *TBMOrchestrator) onSwitch(ctx context.Context, e *fsm.Event) {
	if err := o.actions.Switch(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

// onAbortFence runs the abort_fence rollback: it unfences the gateway to
// restore traffic to its pre-migration state. If unfencing fails, cancel the
// rollback so the FSM stays at fenced — the next run's from-zero walk
// re-applies the fence step (a no-op rollout if the gateway never diverged)
// before anything downstream trusts it. Mirrors migration.onAbortFence,
// minus the reason branch (TBM's abort_fence has only one source state,
// fenced — every rollback is an unrouted-producer detection) and the
// sync-config restore (TBM has no pause_offset_sync stage).
func (o *TBMOrchestrator) onAbortFence(ctx context.Context, e *fsm.Event) {
	o.reporter.warn("Unrouted producers detected — removing fence to restore traffic")
	if err := o.actions.unfenceGateway(ctx, o.config); err != nil {
		slog.Error("❌ failed to unfence gateway during rollback", "error", err)
		e.Cancel(fmt.Errorf("failed to unfence gateway: %w", err))
		return
	}
	o.reporter.Success("Gateway unfenced — traffic restored to pre-fence state")
}

func (o *TBMOrchestrator) canTransition(event string) bool {
	return o.fsm.Can(event)
}
