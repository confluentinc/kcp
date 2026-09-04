package tbm

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/looplab/fsm"
)

// WorkflowStep defines a single step in the TBM workflow: pure FSM topology
// plus an ops-facing Description. Mirrors migration.WorkflowStep.
type WorkflowStep struct {
	Event       string
	Description string
	FromState   string
	ToState     string
}

// canonicalWorkflow is the single ordered source of truth for the TBM
// workflow — the forward transitions the FSM walks on Execute. There is
// deliberately no abort_fence rollback edge: nothing yet exists to detect the
// "unrouted producers" condition that would trigger it. Add it later
// alongside the real detection logic.
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

	events := make(fsm.Events, 0, len(canonicalWorkflow))
	for _, step := range canonicalWorkflow {
		events = append(events, fsm.EventDesc{
			Name: step.Event,
			Src:  []string{step.FromState},
			Dst:  step.ToState,
		})
	}

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
		},
	)

	return orchestrator
}

// Initialize triggers the initialize event and persists the result.
func (o *TBMOrchestrator) Initialize(ctx context.Context) error {
	if err := o.fsm.Event(ctx, EventInitialize); err != nil {
		return err
	}
	return o.PersistState()
}

// Execute runs the full TBM workflow from the current state, skipping any
// already-completed steps so a re-run resumes.
func (o *TBMOrchestrator) Execute(ctx context.Context) error {
	if !isKnownState(o.config.CurrentState) {
		return fmt.Errorf("unrecognized tbm migration state %q in state file — refusing to execute (corrupted file, or written by a newer kcp version?)", o.config.CurrentState)
	}

	for _, step := range canonicalWorkflow {
		if !o.canTransition(step.Event) {
			slog.Debug("skipping already-completed tbm step", "step", step.Description, "event", step.Event)
			continue
		}

		if header, ok := stepHeaders[step.Event]; ok {
			o.reporter.section(header)
		}
		slog.Debug("executing tbm step", "step", step.Description)
		if err := o.fsm.Event(ctx, step.Event); err != nil {
			return fmt.Errorf("failed during %s: %w", step.Description, err)
		}
		if err := o.PersistState(); err != nil {
			return fmt.Errorf("failed during %s: %w", step.Description, err)
		}
		o.reporter.stepDone()
	}

	o.reporter.complete("✅ TBM migration complete!")
	return nil
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
	if err := o.actions.Initialize(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

func (o *TBMOrchestrator) onWaitForLags(ctx context.Context, e *fsm.Event) {
	if err := o.actions.WaitForLags(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

func (o *TBMOrchestrator) onFence(ctx context.Context, e *fsm.Event) {
	if err := o.actions.Fence(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

func (o *TBMOrchestrator) onVerifyFence(ctx context.Context, e *fsm.Event) {
	if err := o.actions.VerifyFence(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

func (o *TBMOrchestrator) onPromote(ctx context.Context, e *fsm.Event) {
	if err := o.actions.Promote(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

func (o *TBMOrchestrator) onSwitch(ctx context.Context, e *fsm.Event) {
	if err := o.actions.Switch(ctx, o.config); err != nil {
		e.Cancel(err)
	}
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
