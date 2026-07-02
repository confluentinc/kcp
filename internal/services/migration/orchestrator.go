package migration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/fatih/color"
	"github.com/looplab/fsm"
)

// ErrUnroutedProducers is returned when the post-fence check detects producers
// bypassing the gateway. The orchestrator catches this to trigger an
// EventAbortFence transition back to initialized state.
var ErrUnroutedProducers = errors.New("unrouted producers detected")

// WorkflowStep defines a single step in the migration workflow
type WorkflowStep struct {
	Event       string
	Description string
	FromState   string
	ToState     string
	UserMessage string // Emoji-prefixed message shown to the user
}

// canonicalWorkflow is the single source of truth for the migration workflow sequence
var canonicalWorkflow = []WorkflowStep{
	{EventInitialize, "initializing migration", StateUninitialized, StateInitialized, "🔍 Initializing migration..."},
	{EventWaitForLags, "checking replication lags", StateInitialized, StateLagsOk, "⏳ Checking replication lags..."},
	{EventFence, "fencing gateway", StateLagsOk, StateFenced, "🚧 Fencing gateway..."},
	{EventPromote, "promoting topics", StateFenced, StatePromoted, ""},
	{EventSwitch, "switching gateway config", StatePromoted, StateSwitched, "🔄 Switching gateway to Confluent Cloud..."},
}

// ExecutionParams holds runtime parameters needed during migration execution
type ExecutionParams struct {
	LagThreshold     int64
	ClusterApiKey    string
	ClusterApiSecret string
}

// MigrationOrchestrator manages the FSM lifecycle and coordinates workflow execution
type MigrationOrchestrator struct {
	config         *MigrationConfig
	fsm            *fsm.FSM
	workflow       *MigrationWorkflow
	migrationState *MigrationState
	stateFilePath  string
	execParams     ExecutionParams // Runtime execution parameters
}

// NewMigrationOrchestrator creates a new migration orchestrator with injected dependencies
func NewMigrationOrchestrator(
	config *MigrationConfig,
	workflow *MigrationWorkflow,
	migrationState *MigrationState,
	stateFilePath string,
) *MigrationOrchestrator {
	orchestrator := &MigrationOrchestrator{
		config:         config,
		workflow:       workflow,
		migrationState: migrationState,
		stateFilePath:  stateFilePath,
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

	// Bootstrap FSM from persisted state to enable resumability (e.g. "initialized" skips init, resumes at lag check)
	orchestrator.fsm = fsm.NewFSM(
		config.CurrentState,
		events,
		fsm.Callbacks{
			"before_event":                orchestrator.beforeEventCallback,
			"after_event":                 orchestrator.afterEventCallback,
			"enter_state":                 orchestrator.enterStateCallback,
			"leave_state":                 orchestrator.leaveStateCallback,
			"leave_" + StateUninitialized: orchestrator.leaveUninitializedCallback,
			"leave_" + StateInitialized:   orchestrator.leaveInitializedCallback,
			"leave_" + StateLagsOk:        orchestrator.leaveLagsOkCallback,
			"leave_" + StateFenced:        orchestrator.leaveFencedCallback,
			"leave_" + StatePromoted:      orchestrator.leavePromotedCallback,
		},
	)

	return orchestrator
}

// Initialize triggers the initialization event
func (o *MigrationOrchestrator) Initialize(ctx context.Context, clusterApiKey, clusterApiSecret string) error {
	// Store API credentials for use by callback
	o.execParams.ClusterApiKey = clusterApiKey
	o.execParams.ClusterApiSecret = clusterApiSecret

	if err := o.fsm.Event(ctx, EventInitialize); err != nil {
		return err
	}
	return o.persistState()
}

// Execute runs the full migration workflow from the current state
func (o *MigrationOrchestrator) Execute(ctx context.Context, lagThreshold int64, clusterApiKey, clusterApiSecret string) error {
	// Store runtime parameters once for use by all callbacks
	o.execParams.LagThreshold = lagThreshold
	o.execParams.ClusterApiKey = clusterApiKey
	o.execParams.ClusterApiSecret = clusterApiSecret

	// Drive execution from canonical workflow - single source of truth
	for _, step := range canonicalWorkflow {
		if !o.canTransition(step.Event) {
			slog.Debug("skipping already-completed step", "step", step.Description, "event", step.Event)
			continue // Skip already-completed steps (enables resumability)
		}

		if step.UserMessage != "" {
			fmt.Printf("\n%s\n", color.CyanString(step.UserMessage))
		}
		slog.Debug("executing migration step", "step", step.Description)
		if err := o.fsm.Event(ctx, step.Event); err != nil {
			// If unrouted producers were detected during promotion, roll the FSM
			// back to initialized so re-running rechecks lags and re-fences. The
			// abort_fence transition unfences the gateway (see leaveFencedCallback).
			if errors.Is(err, ErrUnroutedProducers) {
				o.rollbackFence(ctx)
			}
			return fmt.Errorf("failed during %s: %w", step.Description, err)
		}
		if err := o.persistState(); err != nil {
			return fmt.Errorf("failed during %s: %w", step.Description, err)
		}
		fmt.Printf("%s\n", color.GreenString("✅ Done"))
	}

	fmt.Printf("\n%s\n", color.GreenString("✅ Migration complete!"))
	return nil
}

// rollbackFence transitions the FSM back to initialized via abort_fence after
// unrouted producers are detected during promotion. The abort_fence transition
// unfences the gateway (see leaveFencedCallback). Failures are logged rather
// than returned — Execute already surfaces the originating detection error, and
// a cancelled abort_fence (e.g. unfence failed) correctly leaves the FSM fenced.
func (o *MigrationOrchestrator) rollbackFence(ctx context.Context) {
	if err := o.fsm.Event(ctx, EventAbortFence); err != nil {
		slog.Error("❌ failed to roll back to initialized after unrouted producer detection", "error", err)
		return
	}
	if err := o.persistState(); err != nil {
		slog.Error("❌ failed to persist state after abort_fence transition", "error", err)
	}
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

// persistState saves the current migration state to disk. Called after each successful FSM transition.
func (o *MigrationOrchestrator) persistState() error {
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

// leaveUninitializedCallback delegates to workflow service Initialize
func (o *MigrationOrchestrator) leaveUninitializedCallback(ctx context.Context, e *fsm.Event) {
	if err := o.workflow.Initialize(ctx, o.config, o.execParams.ClusterApiKey, o.execParams.ClusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveInitializedCallback delegates to workflow service CheckLags
func (o *MigrationOrchestrator) leaveInitializedCallback(ctx context.Context, e *fsm.Event) {
	if err := o.workflow.CheckLags(ctx, o.config, o.execParams.LagThreshold, o.execParams.ClusterApiKey, o.execParams.ClusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveLagsOkCallback delegates to workflow service FenceGateway
func (o *MigrationOrchestrator) leaveLagsOkCallback(ctx context.Context, e *fsm.Event) {
	if err := o.workflow.FenceGateway(ctx, o.config); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveFencedCallback handles transitions out of the fenced state. The forward
// promote transition delegates to PromoteTopics. The abort_fence rollback
// (triggered after unrouted producers are detected) unfences the gateway to
// restore traffic to its pre-migration state — the compensating action for
// fencing lives on the transition that reverses it.
func (o *MigrationOrchestrator) leaveFencedCallback(ctx context.Context, e *fsm.Event) {
	if e.Event == EventAbortFence {
		fmt.Printf("   %s Unrouted producers detected — removing fence to restore traffic\n",
			color.YellowString("⚠️"))
		if err := o.workflow.unfenceGateway(ctx, o.config); err != nil {
			// The gateway is still fenced, so refuse the rollback: cancelling
			// keeps the FSM in fenced, which honestly reflects reality.
			// Re-running execute will retry the unfence.
			slog.Error("❌ failed to unfence gateway after detecting unrouted producers", "error", err)
			e.Cancel(fmt.Errorf("failed to unfence gateway: %w", err))
			return
		}
		fmt.Printf("   %s Gateway unfenced — traffic restored to pre-migration state\n",
			color.GreenString("✔"))
		return
	}
	if err := o.workflow.PromoteTopics(ctx, o.config, o.execParams.ClusterApiKey, o.execParams.ClusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leavePromotedCallback delegates to workflow service SwitchGateway
func (o *MigrationOrchestrator) leavePromotedCallback(ctx context.Context, e *fsm.Event) {
	if err := o.workflow.SwitchGateway(ctx, o.config); err != nil {
		e.Cancel(err)
		return
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
