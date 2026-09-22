package migration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/looplab/fsm"
)

// ErrUnroutedProducers is returned when the post-fence check detects producers
// bypassing the gateway. The orchestrator catches this to trigger an
// EventAbortFence transition back to initialized state. Re-exported from
// offset.ErrUnroutedProducers (the package that actually detects and wraps
// it) so every existing errors.Is call site in this package keeps working
// unchanged.
var ErrUnroutedProducers = offset.ErrUnroutedProducers

// ErrFenceUnconfirmed marks a fence whose CR reached the cluster but whose
// effect on the serving pods was never confirmed. The orchestrator catches it to
// restore the initial CR — see restoreAfterUnconfirmedFence for why that is the
// right compensation even when the fence appears not to have landed at all.
var ErrFenceUnconfirmed = errors.New("fence could not be confirmed on every gateway pod")

// WorkflowStep defines a single step in the migration workflow. It is pure FSM
// topology plus an ops-facing Description; user-facing presentation lives in
// stepHeaders, keyed by event, so the edge definitions stay presentation-free.
type WorkflowStep struct {
	Event       string
	Description string
	FromState   string
	ToState     string
}

// canonicalWorkflow is the single source of truth for the migration workflow
// sequence — the ordered forward transitions the FSM walks on execute.
//
// Two deliberate modelling choices are intentionally NOT represented here:
//   - The offset-sync RESTORE bookend runs OUTSIDE the FSM, after execute, by
//     the command layer (see offset_sync_bookend.go), because the restore must
//     run even when the run aborts or ctx is cancelled. The pause half lives
//     inside the FSM as the pause_offset_sync stage — a pass-through when the
//     operator did not opt in — so it fires at the latest safe moment (right
//     after fencing) instead of stretching the paused window across the run.
//   - There is no terminal/failed state. A step failure cancels its transition
//     (e.Cancel) and leaves the FSM at the last good state within the SAME
//     run; there is no cross-run resume position — every run's FSM starts
//     fresh at uninitialized (see NewMigrationOrchestrator) and walks forward
//     again, re-applying each step's artifact idempotently. abort_fence
//     ({fenced, offset_sync_paused} → initialized) is the only mid-run
//     rollback.
var canonicalWorkflow = []WorkflowStep{
	{EventInitialize, "initializing migration", StateUninitialized, StateInitialized},
	{EventWaitForLags, "checking replication lags", StateInitialized, StateLagsOk},
	{EventFence, "fencing gateway", StateLagsOk, StateFenced},
	{EventPauseOffsetSync, "pausing consumer offset sync", StateFenced, StateOffsetSyncPaused},
	{EventVerifyFence, "verifying gateway fence", StateOffsetSyncPaused, StateFenceVerified},
	{EventPromote, "promoting topics", StateFenceVerified, StatePromoted},
	{EventSwitch, "switching gateway config", StatePromoted, StateSwitched},
}

// stepHeaders maps a forward workflow event to the banner the Execute loop
// prints as it walks canonicalWorkflow. Kept separate from canonicalWorkflow so
// the FSM edge definitions carry no presentation. abort_fence is deliberately
// absent: it is a compensation fired from handleStepFailure, not a forward loop
// step, and its messaging is owned by onAbortFence. pause_offset_sync is also
// absent: a fixed "Pausing..." banner would mislead on the pass-through path,
// so PauseOffsetSync owns its own banner-or-skip-line output.
var stepHeaders = map[string]string{
	EventInitialize:  "🔍 Initializing migration...",
	EventWaitForLags: "⏳ Checking replication lags...",
	EventFence:       "🚧 Fencing gateway...",
	EventVerifyFence: "🔍 Checking for unrouted producers...",
	EventPromote:     "📤 Promoting mirror topics...",
	EventSwitch:      "🔄 Switching gateway to Confluent Cloud...",
}

// ExecutionParams holds the per-run runtime parameters a transition may need.
// It is passed to fsm.Event as the sole argument and read back by callbacks via
// execParamsFromEvent, rather than stashed on the orchestrator, so the values
// flow with the event instead of living as mutable orchestrator state.
type ExecutionParams struct {
	LagThreshold int64
	// RestAuth authenticates the destination cluster-link REST surface — the
	// full resolved Authenticator (basic, bearer, or mtls), not only an
	// api_key/api_secret pair.
	RestAuth clusterlink.Authenticator
	// ReconcileResult is the migplan.Result the init command already computed
	// live, moments before triggering this transition — mirrors TBM's own
	// ExecutionParams shape. onInitialize consumes it directly instead of
	// running any validation of its own; migplan.Reconcile already did that.
	ReconcileResult *migplan.Result
}

// execParamsFromEvent returns the ExecutionParams passed to fsm.Event. Forward
// transitions are fired with them. The abort_fence rollback is fired without
// arguments — its sync-config restore runs in handleStepFailure, which holds
// the run's params directly — and this returns the zero value.
func execParamsFromEvent(e *fsm.Event) ExecutionParams {
	if len(e.Args) > 0 {
		if p, ok := e.Args[0].(ExecutionParams); ok {
			return p
		}
	}
	return ExecutionParams{}
}

// MigrationOrchestrator manages the FSM lifecycle and coordinates workflow execution
type MigrationOrchestrator struct {
	config    *MigrationConfig
	fsm       *fsm.FSM
	actions   *MigrationActions
	reporter  *reporter          // user-facing terminal output
	runReport *RunReportRecorder // per-stage timings; nil when not requested
}

// NewMigrationOrchestrator creates a new migration orchestrator with injected dependencies
func NewMigrationOrchestrator(
	config *MigrationConfig,
	actions *MigrationActions,
) *MigrationOrchestrator {
	orchestrator := &MigrationOrchestrator{
		config:   config,
		actions:  actions,
		reporter: newReporter(),
	}

	// Build FSM events from canonical workflow
	events := make(fsm.Events, 0, len(canonicalWorkflow)+1)
	for _, step := range canonicalWorkflow {
		events = append(events, fsm.EventDesc{
			Name: step.Event,
			Src:  []string{step.FromState},
			Dst:  step.ToState,
		})
	}
	// Backward transition: unfence and roll back to initialized. Covers both
	// states where the fence is up: fenced (pause step failed) and
	// offset_sync_paused (verify_fence detected unrouted producers).
	events = append(events, fsm.EventDesc{
		Name: EventAbortFence,
		Src:  []string{StateFenced, StateOffsetSyncPaused},
		Dst:  StateInitialized,
	})

	// The FSM always starts at uninitialized on construction — there is no
	// resume position: the command layer calls migplan.Reconcile live on
	// every invocation and hands its fresh *migplan.Result to Execute, which
	// walks canonicalWorkflow from the top and re-applies each step's artifact
	// idempotently (Task 1's len(config.Topics)==0 no-op guards make an
	// already-complete migration a side-effect-free walk-through). This is why
	// the old expire_* demotions — which used to re-derive a safe resume point
	// from a point-in-time fence/verification fact after a restart — no longer
	// exist: there is nothing to demote from when every run starts at zero.
	//
	// Action callbacks are registered per-event (before_<EVENT>), not per-state
	// (leave_<STATE>), so each is single-purpose. This matters for the fenced
	// state, which two events leave — verify_fence (forward) and abort_fence
	// (rollback) — each with its own callback and no event-sniffing guard.
	orchestrator.fsm = fsm.NewFSM(
		StateUninitialized,
		events,
		fsm.Callbacks{
			"before_event":                   orchestrator.beforeEventCallback,
			"after_event":                    orchestrator.afterEventCallback,
			"enter_state":                    orchestrator.enterStateCallback,
			"leave_state":                    orchestrator.leaveStateCallback,
			"before_" + EventInitialize:      orchestrator.onInitialize,
			"before_" + EventWaitForLags:     orchestrator.onWaitForLags,
			"before_" + EventFence:           orchestrator.onFence,
			"before_" + EventPauseOffsetSync: orchestrator.onPauseOffsetSync,
			"before_" + EventVerifyFence:     orchestrator.onVerifyFence,
			"before_" + EventPromote:         orchestrator.onPromote,
			"before_" + EventAbortFence:      orchestrator.onAbortFence,
			"before_" + EventSwitch:          orchestrator.onSwitch,
		},
	)

	return orchestrator
}

// SetRunReportRecorder attaches a run-report recorder, which records per-stage
// timings as Execute walks the workflow. A nil recorder (the default) disables
// reporting; it is a setter rather than a constructor argument so the init path,
// which builds an orchestrator for a single transition, is untouched.
func (o *MigrationOrchestrator) SetRunReportRecorder(r *RunReportRecorder) {
	o.runReport = r
}

// CurrentState returns the FSM's current state — the single source of truth
// for where this run's machine sits, now that no config field mirrors it.
func (o *MigrationOrchestrator) CurrentState() string {
	return o.fsm.Current()
}

// Execute runs the full migration workflow, always from StateUninitialized
// (see NewMigrationOrchestrator). res is the migplan.Result the command layer
// (cmd/migration/execute) computes live via migplan.Reconcile on every
// invocation — not only the first — and is now non-nil on every call;
// onInitialize consumes it directly.
func (o *MigrationOrchestrator) Execute(ctx context.Context, lagThreshold int64, restAuth clusterlink.Authenticator, res *migplan.Result) error {
	// Own a cancellable child context so the test-only kill-point seam can
	// interrupt the run after a chosen checkpoint via a real cancellation
	// (inert unless killpoint.EnvVar is set — never fires in production).
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	params := ExecutionParams{
		LagThreshold:    lagThreshold,
		RestAuth:        restAuth,
		ReconcileResult: res,
	}

	// Drive execution from canonical workflow - single source of truth
	//
	// Stage timings are taken here rather than on the FSM's before/after
	// callbacks: looplab runs the named before_<EVENT> callback — which is where
	// the step's actual work happens — BEFORE the general before_event one, so
	// before_event fires after the work is already done and cannot mark a start.
	for _, step := range canonicalWorkflow {
		if !o.canTransition(step.Event) {
			slog.Debug("skipping already-completed step", "step", step.Description, "event", step.Event)
			o.runReport.StageSkipped(step.Event)
			continue // Skip already-completed steps (enables resumability)
		}

		if header, ok := stepHeaders[step.Event]; ok {
			o.reporter.section(header)
		}
		slog.Debug("executing migration step", "step", step.Description)
		o.runReport.StageStarted(step.Event, step.FromState, step.ToState)
		if err := o.fsm.Event(ctx, step.Event, params); err != nil {
			o.runReport.StageFailed(err)
			return o.handleStepFailure(ctx, step, err, params)
		}
		o.runReport.StageEnded(o.fsm.Current())
		o.reporter.stepDone()

		// Test-only interruption seam: if configured to stop after this
		// checkpoint, cancel the run now (real context cancellation, the same
		// path a Ctrl-C takes) so the live resume suite is left with a genuine
		// partial world after this step's mutation. No-op in production.
		if killpoint.ShouldCancelAfter(o.fsm.Current()) {
			slog.Warn("⚠️ test kill-point reached — cancelling run to simulate an abrupt exit", "afterState", o.fsm.Current())
			cancel()
			return ctx.Err()
		}
	}

	o.reporter.complete("✅ Migration complete!")
	return nil
}

// handleStepFailure is the single place that maps a failed workflow step to its
// compensating rollback (if any) and returns the wrapped error. Two failures
// compensate, both via abort_fence (which unfences the gateway — see
// onAbortFence — followed here by the sync-config restore):
//   - any pause_offset_sync failure, keyed by step identity: clients must not
//     be held fenced over a config problem, so the run rolls back and a re-run
//     rechecks lags, re-fences, and retries the pause;
//   - fence verification detecting unrouted producers (ErrUnroutedProducers,
//     keyed by error class because the verify step's fetch errors must NOT
//     roll back).
//
// The rollback event is fired here — never from inside a callback, where
// looplab's non-reentrant eventMu would deadlock. The sync-config restore also
// runs here, after the completed transition, rather than in onAbortFence: a
// before_-callback runs ahead of the transition, and client traffic (the
// unfence) must land before config tidiness is attempted.
//
// A cancelled abort_fence (e.g. the unfence itself failed) is logged, not
// returned: the originating step error is what surfaces, and the FSM correctly
// stays at the rollback's source.
func (o *MigrationOrchestrator) handleStepFailure(ctx context.Context, step WorkflowStep, stepErr error, params ExecutionParams) error {
	stepFailure := fmt.Errorf("failed during %s: %w", step.Description, stepErr)

	// A fence that reached the cluster but was never confirmed is compensated
	// outside the FSM: the fence transition was cancelled, so the machine never
	// left its pre-fence state and there is no edge to travel back along. Only
	// the cluster needs putting right.
	if errors.Is(stepErr, ErrFenceUnconfirmed) {
		return o.restoreAfterUnconfirmedFence(ctx, stepFailure)
	}

	// A compensating rollback fires only for a pause_offset_sync failure or a
	// verify_fence unrouted-producers detection; every other step failure just
	// leaves the FSM at its last good state. Record which branch was taken — the
	// propagated error names the failing step, but not the rollback decision.
	willRollback := step.Event == EventPauseOffsetSync || errors.Is(stepErr, ErrUnroutedProducers)
	slog.Debug("handling migration step failure", "step", step.Event, "will_rollback", willRollback)
	if !willRollback {
		return stepFailure
	}

	if err := o.fsm.Event(ctx, EventAbortFence); err != nil {
		slog.Error("❌ failed to roll back to initialized", "error", err)
		return stepFailure
	}

	// Restore the paused sync config now that the rollback landed.
	o.actions.restoreOffsetSyncAfterRollback(o.config, params.RestAuth)

	return stepFailure
}

// restoreAfterUnconfirmedFence reapplies the initial gateway CR after a fence
// that reached the cluster but was never confirmed on the serving pods.
//
// The compensation runs for every unconfirmed fence, not only for one seen to be
// partially applied, because the two readings a timeout can produce are not
// equally legible and neither is safe to leave alone:
//
//   - Some pods at the fenced revision. CFK promotes a hot-reloadable change
//     only after its canary pod passes, so a partial reading means the change
//     WAS promoted and propagation then stalled. Client traffic is being blocked
//     on part of the fleet with nothing driving it to completion.
//   - No pod at the fenced revision. This is the ambiguous one: a canary
//     rejection (the shared config was never updated, so nothing is fenced) is
//     indistinguishable from a promotion still in flight, and stays that way
//     forever — there is no later signal that resolves it. Doing nothing bets on
//     the rejection and loses the other way, with the fence landing after kcp
//     has exited and nobody watching.
//
// Reapplying the initial CR settles both without having to tell them apart. It
// carries a fresh configId, so it supersedes any promotion still in flight
// rather than racing it, and against a gateway that was never fenced it restores
// the spec already in force — a no-op in effect, one hot-reload cycle in cost.
//
// The FSM is deliberately untouched: the fence transition was cancelled, so the
// machine still sits at its pre-fence state, which is already the truth.
func (o *MigrationOrchestrator) restoreAfterUnconfirmedFence(ctx context.Context, stepFailure error) error {
	// A definite rejection is not the ambiguous timeout the rest of this path is
	// written for: CFK explicitly refused the fenced spec, so it never took effect
	// on any pod. Restoring is still right — the refused spec is live in etcd and
	// would fence if the operator's objection later clears — but the operator must
	// hear the definite story and CFK's own reason, not be sent hunting for a
	// partial application that cannot exist.
	var rejected *gateway.GatewayRejectedError
	definiteRejection := errors.As(stepFailure, &rejected)

	if definiteRejection {
		o.reporter.warn("Confluent operator rejected the fenced gateway spec (reason: %s) — it never took effect; restoring the initial gateway CR", rejected.Reason)
	} else {
		o.reporter.warn("Fence could not be confirmed on every gateway pod — restoring the initial gateway CR")
	}

	if err := o.actions.unfenceGateway(ctx, o.config); err != nil {
		slog.Error("❌ failed to restore the gateway after an unconfirmed fence", "error", err)
		return fmt.Errorf("%w; additionally, restoring the initial gateway CR failed: %w; the gateway may still be holding the fenced config on some pods, so inspect it before re-running", stepFailure, err)
	}

	if definiteRejection {
		o.reporter.Success("Initial gateway CR restored — the rejected fenced spec has been superseded")
	} else {
		o.reporter.Success("Initial gateway CR restored — the fenced config cannot take effect later")
	}
	return stepFailure
}

// beforeEventCallback is called before any event transition
func (o *MigrationOrchestrator) beforeEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("FSM: before event", "event", e.Event, "src", e.Src, "dst", e.Dst)
}

// afterEventCallback is called after any event transition. This is the
// migration's diagnostic backbone: every committed state change — forward step
// or abort_fence rollback — lands here as a single Info line, so kcp.log
// carries the full state timeline of a run. Deep FSM mechanics stay on the
// before/enter/leave Debug callbacks. The FSM's own state (o.fsm.Current(),
// which e.Dst mirrors) is authoritative in-process — there is no config field
// to keep in sync.
func (o *MigrationOrchestrator) afterEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("migration state advanced", "event", e.Event, "from", e.Src, "to", e.Dst, "migration_id", o.config.MigrationId)
}

// enterStateCallback is called when entering any state
func (o *MigrationOrchestrator) enterStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("FSM: entering state", "state", e.Dst)
}

// leaveStateCallback is called when leaving any state
func (o *MigrationOrchestrator) leaveStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("FSM: leaving state", "state", e.Src)
}

// onInitialize runs the initialize transition: delegates to workflow Initialize.
func (o *MigrationOrchestrator) onInitialize(ctx context.Context, e *fsm.Event) {
	p := execParamsFromEvent(e)
	if err := o.actions.Initialize(ctx, o.config, p.RestAuth, p.ReconcileResult); err != nil {
		e.Cancel(err)
	}
}

// onWaitForLags runs the wait_for_lags transition: delegates to workflow CheckLags.
func (o *MigrationOrchestrator) onWaitForLags(ctx context.Context, e *fsm.Event) {
	p := execParamsFromEvent(e)
	if err := o.actions.CheckLags(ctx, o.config, p.LagThreshold, p.RestAuth); err != nil {
		e.Cancel(err)
	}
}

// onFence runs the fence transition: delegates to workflow FenceGateway.
func (o *MigrationOrchestrator) onFence(ctx context.Context, e *fsm.Event) {
	if err := o.actions.FenceGateway(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

// onPauseOffsetSync runs the pause_offset_sync transition: delegates to
// PauseOffsetSync, which pauses cluster-link consumer offset sync when the
// operator opted in and passes through otherwise. The transition fires either
// way — promotion's path runs through offset_sync_paused unconditionally.
func (o *MigrationOrchestrator) onPauseOffsetSync(ctx context.Context, e *fsm.Event) {
	p := execParamsFromEvent(e)
	if err := o.actions.PauseOffsetSync(ctx, o.config, p.RestAuth); err != nil {
		e.Cancel(err)
	}
}

// onVerifyFence runs the verify_fence transition: delegates to VerifyFence.
// Registered on before_verify_fence (not leave_fenced), so it fires only for
// the verify_fence event — never for the abort_fence rollback that also
// leaves fenced. On ErrUnroutedProducers the transition is cancelled, leaving
// the FSM at fenced, and handleStepFailure fires the abort_fence rollback.
func (o *MigrationOrchestrator) onVerifyFence(ctx context.Context, e *fsm.Event) {
	if err := o.actions.VerifyFence(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

// onPromote runs the forward promote transition: delegates to PromoteTopics.
func (o *MigrationOrchestrator) onPromote(ctx context.Context, e *fsm.Event) {
	p := execParamsFromEvent(e)
	if err := o.actions.PromoteTopics(ctx, o.config, p.RestAuth); err != nil {
		e.Cancel(err)
	}
}

// onAbortFence runs the abort_fence rollback's gateway half: it unfences the
// gateway to restore traffic to its pre-migration state — the compensating
// action for fencing lives on the transition that reverses it. If unfencing
// fails, cancel the rollback so the FSM stays at its source state: honest when
// the apply itself failed, and when the apply landed but readiness never
// confirmed, the next run's from-zero walk re-applies the fence step (a no-op
// rollout if the gateway never diverged) before anything downstream trusts it.
// The sync-config restore is
// deliberately NOT here — it persists state, and a before_-callback runs ahead
// of the transition, so handleStepFailure runs it after the completed
// transition has been persisted. It stays ordered after readiness confirms:
// client traffic beats config tidiness, and a restore error must not undo a
// completed unfence.
func (o *MigrationOrchestrator) onAbortFence(ctx context.Context, e *fsm.Event) {
	// The reason is unambiguous from the source state: only the pause step
	// fails at fenced, and only rogue detection fails at offset_sync_paused.
	reason := "Pausing consumer offset sync failed"
	if e.Src == StateOffsetSyncPaused {
		reason = "Unrouted producers detected"
	}
	o.reporter.warn("%s — removing fence to restore traffic", reason)
	if err := o.actions.unfenceGateway(ctx, o.config); err != nil {
		slog.Error("❌ failed to unfence gateway during rollback", "error", err)
		e.Cancel(fmt.Errorf("failed to unfence gateway: %w", err))
		return
	}
	o.reporter.Success("Gateway unfenced — traffic restored to pre-migration state")
}

// onSwitch runs the switch transition: delegates to workflow SwitchGateway.
func (o *MigrationOrchestrator) onSwitch(ctx context.Context, e *fsm.Event) {
	if err := o.actions.SwitchGateway(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

// canTransition checks if the given event can be triggered from the current state
func (o *MigrationOrchestrator) canTransition(event string) bool {
	return o.fsm.Can(event)
}
