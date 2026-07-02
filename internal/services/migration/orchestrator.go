package migration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/looplab/fsm"
)

// ErrUnroutedProducers is returned when the post-fence check detects producers
// bypassing the gateway. The orchestrator catches this to trigger an
// EventAbortFence transition back to initialized state.
var ErrUnroutedProducers = errors.New("unrouted producers detected")

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
//   - The offset-sync pause/restore bookends run OUTSIDE the FSM, wrapped around
//     the whole execute by the command layer (see offset_sync_bookend.go),
//     because the restore must run even when the run aborts or ctx is cancelled.
//   - There is no terminal/failed state. A step failure cancels its transition
//     (e.Cancel) and leaves the FSM at the last good state; re-running execute
//     resumes from there. The sole backward edge, abort_fence (fenced →
//     initialized on unrouted-producer detection), is the only rollback.
var canonicalWorkflow = []WorkflowStep{
	{EventInitialize, "initializing migration", StateUninitialized, StateInitialized},
	{EventWaitForLags, "checking replication lags", StateInitialized, StateLagsOk},
	{EventFence, "fencing gateway", StateLagsOk, StateFenced},
	{EventPromote, "promoting topics", StateFenced, StatePromoted},
	{EventSwitch, "switching gateway config", StatePromoted, StateSwitched},
}

// stepHeaders maps a workflow event to the banner shown to the user when the
// step starts. Kept separate from canonicalWorkflow so the FSM edge definitions
// carry no presentation.
var stepHeaders = map[string]string{
	EventInitialize:  "🔍 Initializing migration...",
	EventWaitForLags: "⏳ Checking replication lags...",
	EventFence:       "🚧 Fencing gateway...",
	EventAbortFence:  "⚠️ Unrouted producers detected — removing fence to restore traffic...",
	EventPromote:     "🚀 Promoting topics...",
	EventSwitch:      "🔄 Switching gateway to Confluent Cloud...",
}

// ExecutionParams holds the per-run runtime parameters a transition may need.
// It is passed to fsm.Event as the sole argument and read back by callbacks via
// execParamsFromEvent, rather than stashed on the orchestrator, so the values
// flow with the event instead of living as mutable orchestrator state.
type ExecutionParams struct {
	LagThreshold     int64
	ClusterApiKey    string
	ClusterApiSecret string
}

// execParamsFromEvent returns the ExecutionParams passed to fsm.Event. Forward
// transitions are fired with them; the abort_fence rollback is fired without
// (onAbortFence needs no runtime params) and this returns the zero value.
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
	config         *MigrationConfig
	fsm            *fsm.FSM
	actions        *MigrationActions
	migrationState *MigrationState
	stateFilePath  string
	reporter       *reporter // user-facing terminal output
}

// NewMigrationOrchestrator creates a new migration orchestrator with injected dependencies
func NewMigrationOrchestrator(
	config *MigrationConfig,
	actions *MigrationActions,
	migrationState *MigrationState,
	stateFilePath string,
) *MigrationOrchestrator {
	orchestrator := &MigrationOrchestrator{
		config:         config,
		actions:        actions,
		migrationState: migrationState,
		stateFilePath:  stateFilePath,
		reporter:       newReporter(),
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
	// Backward transition: unfenced after detecting unrouted producers
	events = append(events, fsm.EventDesc{
		Name: EventAbortFence,
		Src:  []string{StateFenced},
		Dst:  StateInitialized,
	})

	// Bootstrap FSM from persisted state to enable resumability (e.g. "initialized" skips init, resumes at lag check).
	//
	// Action callbacks are registered per-event (before_<EVENT>), not per-state
	// (leave_<STATE>), so each is single-purpose. This matters for the fenced
	// state, which two events leave — promote (forward) and abort_fence
	// (rollback) — each with its own callback and no event-sniffing guard.
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
			"before_" + EventPromote:     orchestrator.onPromote,
			"before_" + EventAbortFence:  orchestrator.onAbortFence,
			"before_" + EventSwitch:      orchestrator.onSwitch,
		},
	)

	return orchestrator
}

// Initialize triggers the initialization event
func (o *MigrationOrchestrator) Initialize(ctx context.Context, clusterApiKey, clusterApiSecret string) error {
	params := ExecutionParams{ClusterApiKey: clusterApiKey, ClusterApiSecret: clusterApiSecret}
	if err := o.fsm.Event(ctx, EventInitialize, params); err != nil {
		return err
	}
	return o.PersistState()
}

// Execute runs the full migration workflow from the current state
func (o *MigrationOrchestrator) Execute(ctx context.Context, lagThreshold int64, clusterApiKey, clusterApiSecret string) error {
	params := ExecutionParams{
		LagThreshold:     lagThreshold,
		ClusterApiKey:    clusterApiKey,
		ClusterApiSecret: clusterApiSecret,
	}

	// Drive execution from canonical workflow - single source of truth
	for _, step := range canonicalWorkflow {
		if !o.canTransition(step.Event) {
			slog.Debug("skipping already-completed step", "step", step.Description, "event", step.Event)
			continue // Skip already-completed steps (enables resumability)
		}

		if header, ok := stepHeaders[step.Event]; ok {
			o.reporter.section(header)
		}
		slog.Debug("executing migration step", "step", step.Description)
		if err := o.fsm.Event(ctx, step.Event, params); err != nil {
			return o.handleStepFailure(ctx, step, err)
		}
		if err := o.PersistState(); err != nil {
			return fmt.Errorf("failed during %s: %w", step.Description, err)
		}
		o.reporter.stepDone()
	}

	o.reporter.complete("✅ Migration complete!")
	return nil
}

// handleStepFailure is the single place that maps a failed workflow step to its
// compensating rollback (if any) and returns the wrapped error. The migration
// defines exactly one compensation: when promotion is aborted because unrouted
// producers were detected (ErrUnroutedProducers, signalled up from the workflow
// layer), roll the fence back to initialized via abort_fence — which unfences
// the gateway (see onAbortFence) — so a re-run rechecks lags and re-fences.
//
// Rollback failures are logged, not returned: the originating step error is
// always what surfaces to the caller, and a cancelled abort_fence (e.g. the
// unfence itself failed) correctly leaves the FSM at fenced.
func (o *MigrationOrchestrator) handleStepFailure(ctx context.Context, step WorkflowStep, stepErr error) error {
	if errors.Is(stepErr, ErrUnroutedProducers) {
		if err := o.fsm.Event(ctx, EventAbortFence); err != nil {
			slog.Error("❌ failed to roll back to initialized after unrouted producer detection", "error", err)
		} else if err := o.PersistState(); err != nil {
			slog.Error("❌ failed to persist state after abort_fence transition", "error", err)
		}
	}
	return fmt.Errorf("failed during %s: %w", step.Description, stepErr)
}

// beforeEventCallback is called before any event transition
func (o *MigrationOrchestrator) beforeEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("FSM: before event", "event", e.Event, "src", e.Src, "dst", e.Dst)
}

// afterEventCallback is called after any event transition
func (o *MigrationOrchestrator) afterEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("FSM: after event", "event", e.Event, "src", e.Src, "dst", e.Dst)
	o.config.CurrentState = e.Dst
}

// PersistState saves the current migration config to the state file. It is the
// single writer for migration state: the orchestrator calls it after each
// successful FSM transition, and the offset-sync bookends (which run outside the
// FSM) are handed this method so they persist through the same path rather than
// duplicating the write.
func (o *MigrationOrchestrator) PersistState() error {
	if err := o.saveState(); err != nil {
		return fmt.Errorf("failed to persist state after transition to %s: %w", o.config.CurrentState, err)
	}
	return nil
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
	if err := o.actions.Initialize(ctx, o.config, p.ClusterApiKey, p.ClusterApiSecret); err != nil {
		e.Cancel(err)
	}
}

// onWaitForLags runs the wait_for_lags transition: delegates to workflow CheckLags.
func (o *MigrationOrchestrator) onWaitForLags(ctx context.Context, e *fsm.Event) {
	p := execParamsFromEvent(e)
	if err := o.actions.CheckLags(ctx, o.config, p.LagThreshold, p.ClusterApiKey, p.ClusterApiSecret); err != nil {
		e.Cancel(err)
	}
}

// onFence runs the fence transition: delegates to workflow FenceGateway.
func (o *MigrationOrchestrator) onFence(ctx context.Context, e *fsm.Event) {
	if err := o.actions.FenceGateway(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

// onPromote runs the forward promote transition: delegates to PromoteTopics.
// Registered on before_promote (not leave_fenced), so it fires only for the
// promote event — never for the abort_fence rollback that also leaves fenced.
func (o *MigrationOrchestrator) onPromote(ctx context.Context, e *fsm.Event) {
	p := execParamsFromEvent(e)
	if err := o.actions.PromoteTopics(ctx, o.config, p.ClusterApiKey, p.ClusterApiSecret); err != nil {
		e.Cancel(err)
	}
}

// onAbortFence runs the abort_fence rollback: unfences the gateway to restore
// traffic to its pre-migration state — the compensating action for fencing
// lives on the transition that reverses it. If unfencing fails, cancel the
// rollback so the FSM stays fenced, which honestly reflects reality; re-running
// execute will retry the unfence.
func (o *MigrationOrchestrator) onAbortFence(ctx context.Context, e *fsm.Event) {
	o.reporter.warn("Unrouted producers detected — removing fence to restore traffic")
	if err := o.actions.unfenceGateway(ctx, o.config); err != nil {
		slog.Error("❌ failed to unfence gateway after detecting unrouted producers", "error", err)
		e.Cancel(fmt.Errorf("failed to unfence gateway: %w", err))
		return
	}
	o.reporter.success("Gateway unfenced — traffic restored to pre-migration state")
}

// onSwitch runs the switch transition: delegates to workflow SwitchGateway.
func (o *MigrationOrchestrator) onSwitch(ctx context.Context, e *fsm.Event) {
	if err := o.actions.SwitchGateway(ctx, o.config); err != nil {
		e.Cancel(err)
	}
}

// saveState persists the current migration config to the state file
func (o *MigrationOrchestrator) saveState() error {
	o.migrationState.UpsertMigration(*o.config)

	if err := o.migrationState.WriteToFile(o.stateFilePath); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	return nil
}

// canTransition checks if the given event can be triggered from the current state
func (o *MigrationOrchestrator) canTransition(event string) bool {
	return o.fsm.Can(event)
}
