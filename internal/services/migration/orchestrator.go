package migration

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/services/persistence"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/looplab/fsm"
)

// Orchestrator manages the FSM lifecycle and coordinates workflow execution
type Orchestrator struct {
	config             *types.MigrationConfig
	fsm                *fsm.FSM
	workflowService    WorkflowService
	persistenceService persistence.MigrationService
	stateFilePath      string
}

// NewOrchestrator creates a new migration orchestrator with injected dependencies
func NewOrchestrator(
	config *types.MigrationConfig,
	workflowService WorkflowService,
	persistenceService persistence.MigrationService,
	stateFilePath string,
) *Orchestrator {
	o := &Orchestrator{
		config:             config,
		workflowService:    workflowService,
		persistenceService: persistenceService,
		stateFilePath:      stateFilePath,
	}
	o.initializeFSM()
	return o
}

// initializeFSM sets up the finite state machine with events and callbacks
func (o *Orchestrator) initializeFSM() {
	o.fsm = fsm.NewFSM(
		o.config.CurrentState,
		fsm.Events{
			{Name: types.EventInitialize, Src: []string{types.StateUninitialized}, Dst: types.StateInitialized},
			{Name: types.EventWaitForLags, Src: []string{types.StateInitialized}, Dst: types.StateLagsOk},
			{Name: types.EventFence, Src: []string{types.StateLagsOk}, Dst: types.StateFenced},
			{Name: types.EventPromote, Src: []string{types.StateFenced}, Dst: types.StatePromoting},
			{Name: types.EventWaitForPromotionCompletion, Src: []string{types.StatePromoting}, Dst: types.StatePromoted},
			{Name: types.EventSwitch, Src: []string{types.StatePromoted}, Dst: types.StateSwitched},
		},
		fsm.Callbacks{
			"before_event":                           o.beforeEventCallback,
			"after_event":                            o.afterEventCallback,
			"enter_state":                            o.enterStateCallback,
			"leave_state":                            o.leaveStateCallback,
			"leave_" + types.StateUninitialized:      o.leaveUninitializedCallback,
			"leave_" + types.StateInitialized:        o.leaveInitializedCallback,
			"leave_" + types.StateLagsOk:             o.leaveLagsOkCallback,
			"leave_" + types.StateFenced:             o.leaveFencedCallback,
			"leave_" + types.StatePromoting:          o.leavePromotingCallback,
			"leave_" + types.StatePromoted:           o.leavePromotedCallback,
		},
	)
}

// beforeEventCallback is called before any event transition
func (o *Orchestrator) beforeEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: BEFORE EVENT", "event", e.Event, "src", e.Src, "dst", e.Dst)
}

// afterEventCallback is called after any event transition
func (o *Orchestrator) afterEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: AFTER EVENT", "event", e.Event, "src", e.Src, "dst", e.Dst)

	// Update config state and save
	o.config.CurrentState = e.Dst
	if err := o.saveState(); err != nil {
		// State persistence failure is critical - panic to avoid state drift
		panic(fmt.Sprintf("FATAL: Failed to save state after transition to %s: %v", e.Dst, err))
	}
}

// enterStateCallback is called when entering any state
func (o *Orchestrator) enterStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: ENTERING STATE", "state", e.Dst)
}

// leaveStateCallback is called when leaving any state
func (o *Orchestrator) leaveStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", e.Src)
}

// leaveUninitializedCallback delegates to workflow service Initialize
func (o *Orchestrator) leaveUninitializedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StateUninitialized)

	// Extract args: clusterApiKey, clusterApiSecret
	var clusterApiKey, clusterApiSecret string
	if len(e.Args) > 0 {
		if key, ok := e.Args[0].(string); ok {
			clusterApiKey = key
		}
	}
	if len(e.Args) > 1 {
		if secret, ok := e.Args[1].(string); ok {
			clusterApiSecret = secret
		}
	}

	// Delegate to workflow service
	if err := o.workflowService.Initialize(ctx, o.config, clusterApiKey, clusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveInitializedCallback delegates to workflow service CheckLags
func (o *Orchestrator) leaveInitializedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StateInitialized)

	// Extract args: threshold, maxWaitTime, clusterApiKey, clusterApiSecret
	var threshold, maxWaitTime int64
	var clusterApiKey, clusterApiSecret string

	if len(e.Args) > 0 {
		if t, ok := e.Args[0].(int64); ok {
			threshold = t
		}
	}
	if len(e.Args) > 1 {
		if mw, ok := e.Args[1].(int64); ok {
			maxWaitTime = mw
		}
	}
	if len(e.Args) > 2 {
		if key, ok := e.Args[2].(string); ok {
			clusterApiKey = key
		}
	}
	if len(e.Args) > 3 {
		if secret, ok := e.Args[3].(string); ok {
			clusterApiSecret = secret
		}
	}

	// Delegate to workflow service
	if err := o.workflowService.CheckLags(ctx, o.config, threshold, maxWaitTime, clusterApiKey, clusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveLagsOkCallback delegates to workflow service FenceGateway
func (o *Orchestrator) leaveLagsOkCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StateLagsOk)

	// No args needed for fencing
	if err := o.workflowService.FenceGateway(ctx, o.config); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveFencedCallback delegates to workflow service PromoteTopics
func (o *Orchestrator) leaveFencedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StateFenced)

	// Extract args: clusterApiKey, clusterApiSecret
	var clusterApiKey, clusterApiSecret string
	if len(e.Args) > 0 {
		if key, ok := e.Args[0].(string); ok {
			clusterApiKey = key
		}
	}
	if len(e.Args) > 1 {
		if secret, ok := e.Args[1].(string); ok {
			clusterApiSecret = secret
		}
	}

	// Delegate to workflow service
	if err := o.workflowService.PromoteTopics(ctx, o.config, clusterApiKey, clusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leavePromotingCallback delegates to workflow service CheckPromotionCompletion
func (o *Orchestrator) leavePromotingCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StatePromoting)

	// No args needed for checking promotion completion
	if err := o.workflowService.CheckPromotionCompletion(ctx, o.config); err != nil {
		e.Cancel(err)
		return
	}
}

// leavePromotedCallback delegates to workflow service SwitchGateway
func (o *Orchestrator) leavePromotedCallback(ctx context.Context, e *fsm.Event) {
	slog.Info("FSM: LEAVING STATE", "state", types.StatePromoted)

	// No args needed for switching gateway
	if err := o.workflowService.SwitchGateway(ctx, o.config); err != nil {
		e.Cancel(err)
		return
	}
}

// saveState persists the current migration config to the state file
func (o *Orchestrator) saveState() error {
	// Load the current state file
	state, err := o.persistenceService.LoadMigrationState(o.stateFilePath)
	if err != nil {
		return fmt.Errorf("failed to load state for update: %w", err)
	}

	// Update the migration config in the state
	state.UpsertMigration(*o.config)

	// Save the updated state
	if err := o.persistenceService.SaveMigrationState(o.stateFilePath, state); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	return nil
}

// Initialize triggers the initialization event
func (o *Orchestrator) Initialize(ctx context.Context, clusterApiKey, clusterApiSecret string) error {
	return o.fsm.Event(ctx, types.EventInitialize, clusterApiKey, clusterApiSecret)
}

// Execute runs the full migration workflow from the current state
func (o *Orchestrator) Execute(ctx context.Context, threshold, maxWaitTime int64, clusterApiKey, clusterApiSecret string) error {
	// Execute remaining steps based on current state
	steps := []struct {
		event       string
		description string
		args        []any
	}{
		{types.EventWaitForLags, "checking lags", []any{threshold, maxWaitTime, clusterApiKey, clusterApiSecret}},
		{types.EventFence, "fencing gateway", []any{}},
		{types.EventPromote, "promoting topics", []any{clusterApiKey, clusterApiSecret}},
		{types.EventWaitForPromotionCompletion, "waiting for promotion completion", []any{}},
		{types.EventSwitch, "switching gateway config", []any{}},
	}

	for _, step := range steps {
		if !o.canTransition(step.event) {
			continue
		}

		if err := o.fsm.Event(ctx, step.event, step.args...); err != nil {
			return fmt.Errorf("failed during %s: %w", step.description, err)
		}
	}

	return nil
}

// GetCurrentState returns the current FSM state
func (o *Orchestrator) GetCurrentState() string {
	return o.fsm.Current()
}

// GetConfig returns the migration configuration
func (o *Orchestrator) GetConfig() *types.MigrationConfig {
	return o.config
}

// canTransition checks if the given event can be triggered from the current state
func (o *Orchestrator) canTransition(event string) bool {
	return o.fsm.Can(event)
}
