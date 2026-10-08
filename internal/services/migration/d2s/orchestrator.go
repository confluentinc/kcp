package d2s

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/looplab/fsm"
)

// WorkflowStep is one forward step: FSM topology plus an ops-facing Description. Mirrors tbm.WorkflowStep.
type WorkflowStep struct {
	Event       string
	Description string
	FromState   string
	ToState     string
}

// canonicalWorkflow is the ordered list of forward transitions Execute walks. abort_fence is a compensating
// rollback, not a forward step (see EventAbortFence and handleStepFailure).
var canonicalWorkflow = []WorkflowStep{
	{EventInitialize, "initializing route conversion", StateUninitialized, StateInitialized},
	{EventFence, "fencing route", StateInitialized, StateFenced},
	{EventVerifyFence, "verifying fence", StateFenced, StateFenceVerified},
	{EventSyncOffsets, "syncing committed offsets", StateFenceVerified, StateOffsetsSynced},
	{EventSwitch, "switching route to static", StateOffsetsSynced, StateSwitched},
}

// stepHeaders is the banner Execute prints as it starts each step.
var stepHeaders = map[string]string{
	EventInitialize:  "🔍 Initializing route conversion...",
	EventFence:       "🔍 Fencing route...",
	EventVerifyFence: "🔍 Verifying fence...",
	EventSyncOffsets: "🔍 Syncing committed offsets...",
	EventSwitch:      "🔍 Switching route to static...",
}

// ExecutionParams is passed to fsm.Event and read back by the callbacks.
type ExecutionParams struct {
	// ReconcileResult is the plan migplan.Reconcile computed live before Execute; onInitialize consumes it.
	ReconcileResult *migplan.Result
	// Policy is the conversion's effective policies; verify_fence and sync_offsets read it.
	Policy Policy
}

func execParamsFromEvent(e *fsm.Event) ExecutionParams {
	if len(e.Args) > 0 {
		if p, ok := e.Args[0].(ExecutionParams); ok {
			return p
		}
	}
	return ExecutionParams{}
}

// D2SOrchestrator manages the FSM and coordinates the conversion. Mirrors tbm.TBMOrchestrator.
type D2SOrchestrator struct {
	config   *migration.MigrationConfig
	fsm      *fsm.FSM
	actions  *D2SActions
	reporter *reporter
}

// NewD2SOrchestrator creates the orchestrator. The FSM always starts at uninitialized: there is nothing to
// resume from, and every step re-derives its precondition from live state.
func NewD2SOrchestrator(config *migration.MigrationConfig, actions *D2SActions) *D2SOrchestrator {
	o := &D2SOrchestrator{config: config, actions: actions, reporter: actions.reporter}

	events := make(fsm.Events, 0, len(canonicalWorkflow)+1)
	for _, step := range canonicalWorkflow {
		events = append(events, fsm.EventDesc{Name: step.Event, Src: []string{step.FromState}, Dst: step.ToState})
	}
	events = append(events, fsm.EventDesc{
		Name: EventAbortFence,
		Src:  []string{StateFenced, StateFenceVerified, StateOffsetsSynced},
		Dst:  StateInitialized,
	})

	o.fsm = fsm.NewFSM(StateUninitialized, events, fsm.Callbacks{
		"before_event":               o.beforeEventCallback,
		"after_event":                o.afterEventCallback,
		"enter_state":                o.enterStateCallback,
		"leave_state":                o.leaveStateCallback,
		"before_" + EventInitialize:  o.onInitialize,
		"before_" + EventFence:       o.onFence,
		"before_" + EventVerifyFence: o.onVerifyFence,
		"before_" + EventSyncOffsets: o.onSyncOffsets,
		"before_" + EventSwitch:      o.onSwitch,
		"before_" + EventAbortFence:  o.onAbortFence,
	})
	return o
}

// Execute runs the whole conversion from uninitialized. res is the reconcile result the caller computed live
// for this manifest; policy is the conversion's effective policies.
func (o *D2SOrchestrator) Execute(ctx context.Context, res *migplan.Result, policy Policy) error {
	if err := o.actions.deps.validate(); err != nil {
		return err
	}
	// A cancellable child context, so the test-only kill point can interrupt the run after a chosen
	// checkpoint with a real cancellation (inert unless killpoint.EnvVar is set).
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	params := ExecutionParams{ReconcileResult: res, Policy: policy}
	for _, step := range canonicalWorkflow {
		if header, ok := stepHeaders[step.Event]; ok {
			o.reporter.section(header)
		}
		slog.Debug("executing d2s step", "step", step.Description)
		// Test-only failure hook: fails this step in place of its action when killpoint.FailEnvVar names it.
		err := killpoint.FailAt(step.Event)
		if err == nil {
			err = o.fsm.Event(ctx, step.Event, params)
		}
		if err != nil {
			return o.handleStepFailure(ctx, step, err)
		}
		o.reporter.stepDone()

		if killpoint.ShouldCancelAfter(o.fsm.Current()) {
			slog.Warn("⚠️ test kill-point reached — cancelling run to simulate an abrupt exit", "afterState", o.fsm.Current())
			cancel()
			return ctx.Err()
		}
	}

	o.reporter.complete("✅ Route conversion complete!")
	return nil
}

// handleStepFailure maps a failed step to its compensation:
//   - a fence that reached the cluster but could not be confirmed is removed outside the FSM;
//   - a switch failure keeps the fence (decision 25): at the switch the route is still fenced or already
//     static, and neither can be safely undone. A switch patch that never reached the CR leaves the route
//     fenced, and a re-run finishes the switch; one that reached the CR but was not confirmed
//     (ErrSwitchUnconfirmed) leaves the CR static, so the operator is sent to the Gateway, not to a re-run;
//   - any other failure while kcp's fence is up rolls back: abort_fence when this run's fence has landed, or a
//     direct unfence when an earlier run's fence was up at the start (FencedAtStart) and this run failed before
//     its own fence step;
//   - a cancelled context leaves the fenced world for the next run, since the unfence IO cannot run.
func (o *D2SOrchestrator) handleStepFailure(ctx context.Context, step WorkflowStep, stepErr error) error {
	stepFailure := fmt.Errorf("failed during %s: %w", step.Description, stepErr)

	if errors.Is(stepErr, migration.ErrFenceUnconfirmed) {
		if !o.config.RollbackAllowed {
			o.reporter.warn("Fence could not be confirmed on every gateway pod — keeping the fence: %s", rollbackForbiddenReason)
			return stepFailure
		}
		return o.removeUnconfirmedFence(ctx, stepFailure)
	}

	if step.Event == EventSwitch {
		if errors.Is(stepErr, ErrSwitchUnconfirmed) {
			o.reporter.warn("Switch could not be confirmed — the Gateway CR already holds the static route, so a re-run will report \"nothing to do\". " +
				"The gateway pods may still be running the fenced dynamic route: check the Gateway's status (operator acceptance, pods) and fix the Gateway rather than re-running")
			return stepFailure
		}
		o.reporter.warn("Switch failed — keeping the fence: the static route never reached the Gateway CR, so the route is still fenced; resolve the failure, then re-run to finish the switch")
		return stepFailure
	}

	thisRunsFence := o.fsm.Can(EventAbortFence)
	earlierRunsFence := o.config.FencedAtStart && beforeFenceStep(o.fsm.Current())
	if (!thisRunsFence && !earlierRunsFence) || ctx.Err() != nil {
		return stepFailure
	}

	reason := rollbackReason(step, stepErr)
	if !o.config.RollbackAllowed {
		o.reporter.warn("%s — keeping the fence: %s", reason, rollbackForbiddenReason)
		return stepFailure
	}
	o.reporter.warn("%s — removing fence", reason)

	if !thisRunsFence {
		// This run never reached fenced, so there is no abort_fence edge: only the gateway needs putting right.
		if err := o.actions.unfenceGateway(ctx, o.config); err != nil {
			slog.Error("❌ failed to unfence gateway during rollback", "error", err)
			return fmt.Errorf("%w; additionally, removing the fence failed: %w; the route may still block every topic, so inspect the gateway before re-running", stepFailure, err)
		}
		o.reporter.Success("Gateway unfenced — traffic restored to pre-fence state")
		return stepFailure
	}
	if err := o.fsm.Event(ctx, EventAbortFence); err != nil {
		slog.Error("❌ failed to roll back to initialized", "error", err)
		return fmt.Errorf("%w; additionally, removing the fence failed: %w; the route may still block every topic, so inspect the gateway before re-running", stepFailure, err)
	}
	return stepFailure
}

// rollbackForbiddenReason is why a failure keeps the fence when reconcile forbade a rollback. A conversion's
// reconcile always allows one; this guards a result that says otherwise.
const rollbackForbiddenReason = "reconcile did not allow a rollback for this run. Resolve the failure, then re-run"

// rollbackReason names a rollback's cause for the operator.
func rollbackReason(step WorkflowStep, err error) string {
	switch {
	case errors.Is(err, groupoffsets.ErrRogueCommits):
		return "Direct commits detected"
	case errors.Is(err, ErrVerifyRefused):
		return "Conversion check failed after the fence"
	}
	return strings.ToUpper(step.Description[:1]) + step.Description[1:] + " failed"
}

// removeUnconfirmedFence sets the route's rules to reconcile's rollback target after a fence that reached the
// cluster but could not be confirmed. A failed removal is reported with the fence's own failure.
func (o *D2SOrchestrator) removeUnconfirmedFence(ctx context.Context, stepFailure error) error {
	var rejected *gateway.GatewayRejectedError
	if errors.As(stepFailure, &rejected) {
		o.reporter.warn("Confluent operator rejected the fenced gateway spec (reason: %s) — it never took effect; removing it", rejected.Reason)
	} else {
		o.reporter.warn("Fence could not be confirmed on every gateway pod — removing it")
	}
	if err := o.actions.unfenceGateway(ctx, o.config); err != nil {
		slog.Error("❌ failed to remove an unconfirmed fence", "error", err)
		return fmt.Errorf("%w; additionally, removing the fence failed: %w; the gateway may still hold the fenced rules on some pods, so inspect it before re-running", stepFailure, err)
	}
	o.reporter.Success("Fence removed — traffic restored to pre-fence state")
	return stepFailure
}

// beforeFenceStep reports whether state comes before this run's fence lands.
func beforeFenceStep(state string) bool {
	return state == StateUninitialized || state == StateInitialized
}

func (o *D2SOrchestrator) beforeEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("d2s FSM: before event", "event", e.Event, "src", e.Src, "dst", e.Dst)
}

func (o *D2SOrchestrator) afterEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("route conversion state advanced", "event", e.Event, "from", e.Src, "to", e.Dst, "migration_id", o.config.MigrationId)
}

func (o *D2SOrchestrator) enterStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("d2s FSM: entering state", "state", e.Dst)
}

func (o *D2SOrchestrator) leaveStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("d2s FSM: leaving state", "state", e.Src)
}

func (o *D2SOrchestrator) onInitialize(ctx context.Context, e *fsm.Event) {
	if err := o.actions.Initialize(ctx, o.config, execParamsFromEvent(e).ReconcileResult); err != nil {
		e.Cancel(err)
	}
}

func (o *D2SOrchestrator) onFence(ctx context.Context, e *fsm.Event) {
	if err := o.actions.Fence(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

func (o *D2SOrchestrator) onVerifyFence(ctx context.Context, e *fsm.Event) {
	if err := o.actions.VerifyFence(ctx, o.config, execParamsFromEvent(e).Policy); err != nil {
		e.Cancel(err)
	}
}

func (o *D2SOrchestrator) onSyncOffsets(ctx context.Context, e *fsm.Event) {
	if err := o.actions.SyncOffsets(ctx, o.config, execParamsFromEvent(e).Policy); err != nil {
		e.Cancel(err)
	}
}

func (o *D2SOrchestrator) onSwitch(ctx context.Context, e *fsm.Event) {
	if err := o.actions.Switch(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

// onAbortFence unfences the gateway. If that fails the transition is cancelled, so the FSM stays fenced and
// the next run's walk re-applies the fence before anything trusts it. The reason was announced by
// handleStepFailure. The hand-off is dropped: a rolled-back run syncs nothing.
func (o *D2SOrchestrator) onAbortFence(ctx context.Context, e *fsm.Event) {
	o.actions.handoff = nil
	if err := o.actions.unfenceGateway(ctx, o.config); err != nil {
		slog.Error("❌ failed to unfence gateway during rollback", "error", err)
		e.Cancel(fmt.Errorf("failed to unfence gateway: %w", err))
		return
	}
	o.reporter.Success("Gateway unfenced — traffic restored to pre-fence state")
}
