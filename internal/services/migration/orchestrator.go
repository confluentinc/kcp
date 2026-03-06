package migration

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/looplab/fsm"
)

// WorkflowStep defines a single step in the migration workflow
type WorkflowStep struct {
	Event       string
	Description string
	FromState   string
	ToState     string
}

// canonicalWorkflow is the single source of truth for the migration workflow sequence
var canonicalWorkflow = []WorkflowStep{
	{types.EventInitialize, "initializing migration", types.StateUninitialized, types.StateInitialized},
	{types.EventWaitForLags, "checking replication lags", types.StateInitialized, types.StateLagsOk},
	{types.EventFence, "fencing gateway", types.StateLagsOk, types.StateFenced},
	{types.EventPromote, "promoting topics", types.StateFenced, types.StatePromoted},
	{types.EventSwitch, "switching gateway config", types.StatePromoted, types.StateSwitched},
}

// ExecutionParams holds runtime parameters needed during migration execution
type ExecutionParams struct {
	LagThreshold     int64
	ClusterApiKey    string
	ClusterApiSecret string
}

// MigrationOrchestrator manages the FSM lifecycle and coordinates workflow execution
type MigrationOrchestrator struct {
	config         *types.MigrationConfig
	fsm            *fsm.FSM
	workflow       *MigrationWorkflow
	migrationState *types.MigrationState
	stateFilePath  string
	execParams     ExecutionParams // Runtime execution parameters
}

// NewMigrationOrchestrator creates a new migration orchestrator with injected dependencies
func NewMigrationOrchestrator(
	config *types.MigrationConfig,
	workflow *MigrationWorkflow,
	migrationState *types.MigrationState,
	stateFilePath string,
) *MigrationOrchestrator {
	orchestrator := &MigrationOrchestrator{
		config:         config,
		workflow:       workflow,
		migrationState: migrationState,
		stateFilePath:  stateFilePath,
	}

	// Build FSM events from canonical workflow
	events := make(fsm.Events, 0, len(canonicalWorkflow))
	for _, step := range canonicalWorkflow {
		events = append(events, fsm.EventDesc{
			Name: step.Event,
			Src:  []string{step.FromState},
			Dst:  step.ToState,
		})
	}

	// Bootstrap FSM from persisted state to enable resumability (e.g. "initialized" skips init, resumes at lag check)
	orchestrator.fsm = fsm.NewFSM(
		config.CurrentState,
		events,
		fsm.Callbacks{
			"before_event":                      orchestrator.beforeEventCallback,
			"after_event":                       orchestrator.afterEventCallback,
			"enter_state":                       orchestrator.enterStateCallback,
			"leave_state":                       orchestrator.leaveStateCallback,
			"leave_" + types.StateUninitialized: orchestrator.leaveUninitializedCallback,
			"leave_" + types.StateInitialized:   orchestrator.leaveInitializedCallback,
			"leave_" + types.StateLagsOk:        orchestrator.leaveLagsOkCallback,
			"leave_" + types.StateFenced:        orchestrator.leaveFencedCallback,
			"leave_" + types.StatePromoted:      orchestrator.leavePromotedCallback,
		},
	)

	return orchestrator
}

// Initialize triggers the initialization event
func (o *MigrationOrchestrator) Initialize(ctx context.Context, clusterApiKey, clusterApiSecret string) error {
	// Store API credentials for use by callback
	o.execParams.ClusterApiKey = clusterApiKey
	o.execParams.ClusterApiSecret = clusterApiSecret

	return o.fsm.Event(ctx, types.EventInitialize)
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

		slog.Info("executing migration step", "step", step.Description)
		if err := o.fsm.Event(ctx, step.Event); err != nil {
			return fmt.Errorf("failed during %s: %w", step.Description, err)
		}
	}

	return nil
}

// beforeEventCallback is called before any event transition
func (o *MigrationOrchestrator) beforeEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: BEFORE EVENT", "event", e.Event, "src", e.Src, "dst", e.Dst)
}

// afterEventCallback is called after any event transition
func (o *MigrationOrchestrator) afterEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: AFTER EVENT", "event", e.Event, "src", e.Src, "dst", e.Dst)

	// Update config state and save
	o.config.CurrentState = e.Dst
	if err := o.saveState(); err != nil {
		// State persistence failure is critical - panic to avoid state drift
		panic(fmt.Sprintf("FATAL: Failed to save state after transition to %s: %v", e.Dst, err))
	}
}

// enterStateCallback is called when entering any state
func (o *MigrationOrchestrator) enterStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: ENTERING STATE", "state", e.Dst)
}

// leaveStateCallback is called when leaving any state
func (o *MigrationOrchestrator) leaveStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", e.Src)
}

// leaveUninitializedCallback delegates to workflow service Initialize
func (o *MigrationOrchestrator) leaveUninitializedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StateUninitialized)

	// Delegate to workflow service using stored parameters
	if err := o.workflow.Initialize(ctx, o.config, o.execParams.ClusterApiKey, o.execParams.ClusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveInitializedCallback delegates to workflow service CheckLags
func (o *MigrationOrchestrator) leaveInitializedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StateInitialized)

	// Delegate to workflow service using stored parameters
	if err := o.workflow.CheckLags(ctx, o.config, o.execParams.LagThreshold, o.execParams.ClusterApiKey, o.execParams.ClusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveLagsOkCallback delegates to workflow service FenceGateway
func (o *MigrationOrchestrator) leaveLagsOkCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StateLagsOk)

	// No runtime params needed for fencing
	if err := o.workflow.FenceGateway(ctx, o.config); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveFencedCallback delegates to workflow service PromoteTopics
func (o *MigrationOrchestrator) leaveFencedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StateFenced)

	// Delegate to workflow service using stored parameters
	if err := o.workflow.PromoteTopics(ctx, o.config, o.execParams.ClusterApiKey, o.execParams.ClusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leavePromotedCallback delegates to workflow service SwitchGateway
func (o *MigrationOrchestrator) leavePromotedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StatePromoted)

	// No runtime params needed for switching gateway
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
