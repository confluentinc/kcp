package tbm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/looplab/fsm"
)

// ErrUnroutedProducers is returned when verify_fence detects a producer
// bypassing the gateway. The orchestrator catches this to trigger an
// EventAbortFence transition back to initialized. Mirrors migration.ErrUnroutedProducers.
var ErrUnroutedProducers = errors.New("unrouted producers detected")

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
	config        *TBMConfig
	fsm           *fsm.FSM
	actions       *TBMActions
	tbmState      *TBMState
	stateFilePath string
	reporter      *reporter
}

// NewTBMOrchestrator creates a new TBM orchestrator with injected dependencies.
func NewTBMOrchestrator(
	config *TBMConfig,
	actions *TBMActions,
	tbmState *TBMState,
	stateFilePath string,
) *TBMOrchestrator {
	orchestrator := &TBMOrchestrator{
		config:        config,
		actions:       actions,
		tbmState:      tbmState,
		stateFilePath: stateFilePath,
		reporter:      newReporter(),
	}

	events := make(fsm.Events, 0, len(canonicalWorkflow)+3)
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
	events = append(events, fsm.EventDesc{
		Name: EventExpireVerification,
		Src:  []string{StateFenceVerified},
		Dst:  StateFenced,
	})
	events = append(events, fsm.EventDesc{
		Name: EventExpireFence,
		Src:  []string{StateFenced},
		Dst:  StateInitialized,
	})

	orchestrator.fsm = fsm.NewFSM(
		config.CurrentState,
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

	// Key both demotions off the state the config was loaded in, captured
	// once here — not off orchestrator.fsm.Is after the fact. The two are
	// independent, single-level demotions (fence_verified -> fenced,
	// fenced -> initialized), not a cascade: re-checking fsm.Is(StateFenced)
	// after the first demotion has already landed the FSM on fenced would
	// fire the second unconditionally on every fence_verified resume too,
	// demoting all the way to initialized instead of stopping at fenced.
	bootstrapState := config.CurrentState

	// fence_verified is a point-in-time attestation and never survives a
	// restart — see EventExpireVerification.
	if bootstrapState == StateFenceVerified {
		if err := orchestrator.fsm.Event(context.Background(), EventExpireVerification); err != nil {
			slog.Error("❌ failed to expire tbm fence verification at bootstrap", "error", err)
		}
	}
	// The fence posture is a point-in-time fact for the same reason — see
	// EventExpireFence.
	if bootstrapState == StateFenced {
		if err := orchestrator.fsm.Event(context.Background(), EventExpireFence); err != nil {
			slog.Error("❌ failed to expire tbm fence posture at bootstrap", "error", err)
		}
	}

	return orchestrator
}

// Execute runs the full TBM workflow from the current state, skipping any
// already-completed steps so a re-run resumes. res is the reconcile plan the
// caller already computed live for this manifest; onInitialize consumes it.
// lagThreshold is the total replication lag tolerated before wait_for_lags
// proceeds; onWaitForLags consumes it. detectUnroutedProducersDuration is the
// monitoring window verify_fence uses to detect a producer bypassing the
// gateway (0 disables the check); onVerifyFence consumes it. restAuth
// authenticates the destination cluster-link REST surface; onPromote
// consumes it.
func (o *TBMOrchestrator) Execute(ctx context.Context, res *migplan.Result, lagThreshold int64, detectUnroutedProducersDuration time.Duration, restAuth clusterlink.Authenticator) error {
	if !isKnownState(o.config.CurrentState) {
		return fmt.Errorf("unrecognized tbm migration state %q in state file — refusing to execute (corrupted file, or written by a newer kcp version?)", o.config.CurrentState)
	}

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
		if err := o.PersistState(); err != nil {
			return fmt.Errorf("failed during %s: %w", step.Description, err)
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

	if err := o.PersistState(); err != nil {
		return fmt.Errorf("%w; additionally, the rollback completed — the gateway was unfenced — but persisting the rolled-back state failed: %w; the state file may still show the pre-rollback state, and re-running execute-tbm will re-assert the fence and resume from it", stepFailure, err)
	}
	return stepFailure
}

func (o *TBMOrchestrator) beforeEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("TBM FSM: before event", "event", e.Event, "src", e.Src, "dst", e.Dst)
}

// afterEventCallback advances CurrentState and logs every committed
// transition as a single Info line, mirroring migration.afterEventCallback.
func (o *TBMOrchestrator) afterEventCallback(ctx context.Context, e *fsm.Event) {
	o.config.CurrentState = e.Dst
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
// rollback so the FSM stays at fenced — the bootstrap expire_fence demotion
// re-asserts the fenced CR on the next run before anything trusts it. Mirrors
// migration.onAbortFence, minus the reason branch (TBM's abort_fence has only
// one source state, fenced — every rollback is an unrouted-producer
// detection) and the sync-config restore (TBM has no pause_offset_sync stage).
func (o *TBMOrchestrator) onAbortFence(ctx context.Context, e *fsm.Event) {
	o.reporter.warn("Unrouted producers detected — removing fence to restore traffic")
	if err := o.actions.unfenceGateway(ctx, o.config); err != nil {
		slog.Error("❌ failed to unfence gateway during rollback", "error", err)
		e.Cancel(fmt.Errorf("failed to unfence gateway: %w", err))
		return
	}
	o.reporter.success("Gateway unfenced — traffic restored to pre-fence state")
}

// PersistState saves the current TBM config to the state file.
func (o *TBMOrchestrator) PersistState() error {
	if err := o.saveState(); err != nil {
		return fmt.Errorf("failed to persist state after transition to %s: %w", o.config.CurrentState, err)
	}
	slog.Debug("persisted tbm state", "migration_id", o.config.MigrationId, "state", o.config.CurrentState, "path", o.stateFilePath)
	return nil
}

func (o *TBMOrchestrator) saveState() error {
	o.tbmState.UpsertMigration(*o.config)
	if err := o.tbmState.WriteToFile(o.stateFilePath); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}
	return nil
}

func (o *TBMOrchestrator) canTransition(event string) bool {
	return o.fsm.Can(event)
}

// HasPendingWork reports whether any canonical workflow step remains to run.
func (o *TBMOrchestrator) HasPendingWork() bool {
	if !isKnownState(o.config.CurrentState) {
		return true
	}
	for _, step := range canonicalWorkflow {
		if o.canTransition(step.Event) {
			return true
		}
	}
	return false
}
