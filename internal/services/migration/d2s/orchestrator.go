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
	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/looplab/fsm"
	"go.opentelemetry.io/otel/attribute"
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
func (o *D2SOrchestrator) Execute(ctx context.Context, res *migplan.Result, policy Policy) (err error) {
	runAttrs := []attribute.KeyValue{
		attribute.String("kcp.migration.id", o.config.MigrationId),
		attribute.String("kcp.strategy", "d2s"),
		attribute.Int("kcp.workers", policy.Workers),
		attribute.Int64("kcp.detect_unrouted_commits_ms", policy.DetectUnroutedCommitsDuration.Milliseconds()),
	}
	ctx, runSpan := tracing.Start(ctx, tracerName, "migration.run", runAttrs...)
	defer func() { tracing.End(runSpan, err) }()
	tracing.Mark(ctx, tracerName, "migration.started", runAttrs...)
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
		stepCtx, stepSpan := tracing.Start(ctx, tracerName, "step."+step.Event,
			attribute.String("kcp.step.from", step.FromState),
			attribute.String("kcp.step.to", step.ToState),
		)
		// Test-only failure hook: fails this step in place of its action when killpoint.FailEnvVar names it.
		stepErr := killpoint.FailAt(step.Event)
		if stepErr == nil {
			stepErr = o.fsm.Event(stepCtx, step.Event, params)
		}
		tracing.End(stepSpan, stepErr)
		if stepErr != nil {
			return o.handleStepFailure(ctx, step, stepErr)
		}
		if step.Event == EventInitialize {
			runSpan.SetAttributes(migration.RunAttributes(o.config)...)
		}
		o.reporter.stepDone()

		if killpoint.ShouldCancelAfter(o.fsm.Current()) {
			slog.Warn("⚠️ test kill-point reached — cancelling run to simulate an abrupt exit", "afterState", o.fsm.Current())
			cancel()
			return ctx.Err()
		}

		// Test-only hold seam: pause after the configured checkpoint until the
		// release file appears. No-op in production.
		if err := killpoint.HoldAfter(ctx, o.fsm.Current()); err != nil {
			return err
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
//     (ErrSwitchUnconfirmed) leaves the CR static while the pods may still serve the fenced dynamic route. A
//     re-run's reconcile checks the pods first: it refuses while they don't serve the CR, and finishes once
//     they do;
//   - any other failure while kcp's fence is up rolls back: abort_fence when this run's fence has landed, or a
//     direct unfence when an earlier run's fence was up at the start (FencedAtStart) and this run failed before
//     its own fence step;
//   - for those rollbacks, a cancelled context leaves the fenced world for the next run, since the unfence IO
//     cannot run. The unconfirmed-fence removal above does not check the context (as TBM's does not): on a
//     cancelled context its patch fails fast and is reported alongside the fence's own failure.
//
// A failed compensation is folded into the returned error, which main logs; it is only slog.Debug'd here so
// the operator does not see it twice.
func (o *D2SOrchestrator) handleStepFailure(ctx context.Context, step WorkflowStep, stepErr error) error {
	stepFailure := fmt.Errorf("failed during %s: %w", step.Description, stepErr)

	// The CR is no longer the one kcp last knew, so no rollback either: every compensation below writes to the
	// gateway.
	if errors.Is(stepErr, gateway.ErrGatewayChanged) {
		return o.stopOnGatewayChange(stepFailure, stepErr, stoppedAt(o.fsm.Current(), o.config.FencedAtStart))
	}

	if errors.Is(stepErr, migration.ErrFenceUnconfirmed) {
		if !o.config.RollbackAllowed {
			o.reporter.warn("Fence could not be confirmed on every gateway pod — keeping the fence: %s", rollbackForbiddenReason)
			return stepFailure
		}
		return o.removeUnconfirmedFence(ctx, stepFailure)
	}

	if step.Event == EventSwitch {
		if errors.Is(stepErr, ErrSwitchUnconfirmed) {
			o.reporter.warn("Switch could not be confirmed — the Gateway CR already holds the static route, and the gateway pods may still serve the fenced dynamic route. " +
				"A re-run checks the gateway pods before doing anything")
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

	path := migration.RollbackPathAbortFence
	if !thisRunsFence {
		path = migration.RollbackPathEarlierRunFence
	}
	ctx, rollback := tracing.Start(ctx, tracerName, "migration.rollback", attribute.String("kcp.rollback.path", path))
	var rollbackErr error
	defer func() { tracing.End(rollback, rollbackErr) }()

	if !thisRunsFence {
		// This run never reached fenced, so there is no abort_fence edge: only the gateway needs putting right.
		if err := o.actions.unfenceGateway(ctx, o.config); err != nil {
			rollbackErr = err
			if errors.Is(err, gateway.ErrGatewayChanged) {
				return o.stopOnGatewayChange(stepFailure, err, stoppedAt(o.fsm.Current(), o.config.FencedAtStart))
			}
			slog.Debug("failed to unfence gateway during rollback", "error", err)
			return fmt.Errorf("%w; additionally, removing the fence failed: %w; the route may still block every topic, so inspect the gateway before re-running", stepFailure, err)
		}
		o.reporter.Success("Gateway unfenced — traffic restored to pre-fence state")
		return stepFailure
	}
	abortCtx, abortSpan := tracing.Start(ctx, tracerName, "step."+EventAbortFence,
		attribute.String("kcp.step.from", o.fsm.Current()),
		attribute.String("kcp.step.to", StateInitialized),
	)
	abortErr := o.fsm.Event(abortCtx, EventAbortFence)
	tracing.End(abortSpan, abortErr)
	if errors.Is(abortErr, gateway.ErrGatewayChanged) {
		rollbackErr = abortErr
		return o.stopOnGatewayChange(stepFailure, abortErr, stoppedAt(o.fsm.Current(), o.config.FencedAtStart))
	}
	if abortErr != nil {
		rollbackErr = abortErr
		slog.Debug("failed to roll back to initialized", "error", abortErr)
		return fmt.Errorf("%w; additionally, removing the fence failed: %w; the route may still block every topic, so inspect the gateway before re-running", stepFailure, abortErr)
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
	ctx, rollback := tracing.Start(ctx, tracerName, "migration.rollback",
		attribute.String("kcp.rollback.path", migration.RollbackPathUnconfirmedFence))
	var rollbackErr error
	defer func() { tracing.End(rollback, rollbackErr) }()

	var rejected *gateway.GatewayRejectedError
	if errors.As(stepFailure, &rejected) {
		o.reporter.warn("Confluent operator rejected the fenced gateway spec (reason: %s) — it never took effect; removing it", rejected.Reason)
	} else {
		o.reporter.warn("Fence could not be confirmed on every gateway pod — removing it")
	}
	if err := o.actions.unfenceGateway(ctx, o.config); err != nil {
		rollbackErr = err
		if errors.Is(err, gateway.ErrGatewayChanged) {
			return o.stopOnGatewayChange(stepFailure, err, unconfirmedFenceStop)
		}
		slog.Debug("failed to remove an unconfirmed fence", "error", err)
		return fmt.Errorf("%w; additionally, removing the fence failed: %w; the gateway may still hold the fenced rules on some pods, so inspect it before re-running", stepFailure, err)
	}
	o.reporter.Success("Fence removed — traffic restored to pre-fence state")
	return stepFailure
}

// unconfirmedFenceStop is where a run stops when the CR moved before the removal of an unconfirmed fence could
// restore it.
const unconfirmedFenceStop = "fence applied but not confirmed on every gateway pod"

// stoppedAt says where a conversion stopped, for a stop on a changed gateway CR: how far the gateway got, read
// from the FSM state the run halted at.
func stoppedAt(state string, fencedAtStart bool) string {
	switch state {
	case StateUninitialized, StateInitialized:
		if fencedAtStart {
			return "an earlier run's fence applied"
		}
		return "no fence applied"
	case StateFenced:
		return "fence applied, not verified"
	case StateFenceVerified:
		return "fence applied, offsets not synced"
	case StateOffsetsSynced:
		return "offsets synced, route not switched"
	case StateSwitched:
		return "route switched"
	default:
		return "run stopped at state " + state
	}
}

// stopOnGatewayChange ends a run whose gateway CR is no longer the one kcp last knew (err Is
// gateway.ErrGatewayChanged). kcp makes no further gateway write and commits no offsets: it reports what it
// observed and where it stopped, and leaves the gateway for the operator to inspect. err is either the step's
// own error or a compensation's, in which case it is joined onto stepFailure so both survive.
func (o *D2SOrchestrator) stopOnGatewayChange(stepFailure, err error, stopped string) error {
	var gce *gateway.GenerationChangedError
	if errors.As(err, &gce) {
		o.reporter.warn("%s", gce.Summary(stopped))
	}
	o.reporter.Remediation("Inspect the gateway:\n   %s", gateway.InspectCommands(o.config.K8sNamespace, o.config.InitialCrName))
	slog.Debug("stopping on a changed gateway CR", "stopped", stopped, "error", err)
	if errors.Is(stepFailure, err) {
		return stepFailure
	}
	return fmt.Errorf("%w; kcp made no further gateway write: %w", stepFailure, err)
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
		// A changed CR is reported by handleStepFailure's stopOnGatewayChange.
		if !errors.Is(err, gateway.ErrGatewayChanged) {
			slog.Debug("failed to unfence gateway during rollback", "error", err)
		}
		e.Cancel(fmt.Errorf("failed to unfence gateway: %w", err))
		return
	}
	o.reporter.Success("Gateway unfenced — traffic restored to pre-fence state")
}
