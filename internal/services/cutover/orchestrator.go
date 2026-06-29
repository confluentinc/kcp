package cutover

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/fatih/color"
	"github.com/looplab/fsm"
)

// WorkflowStep defines a single step in the cutover workflow
type WorkflowStep struct {
	Event       string
	Description string
	FromState   string
	ToState     string
	UserMessage string // Emoji-prefixed message shown to the user
}

// canonicalWorkflow is the single source of truth for the cutover workflow sequence
var canonicalWorkflow = []WorkflowStep{
	{EventInitialize, "initializing cutover", StateUninitialized, StateInitialized, "🔍 Initializing cutover..."},
	{EventWaitForLags, "checking replication lags", StateInitialized, StateLagsOk, "⏳ Checking replication lags..."},
	{EventFence, "fencing gateway", StateLagsOk, StateFenced, "🚧 Fencing gateway..."},
	{EventPromote, "promoting topics", StateFenced, StatePromoted, "📤 Promoting mirror topics..."},
	{EventSwitch, "switching gateway config", StatePromoted, StateSwitched, "🔄 Switching gateway to Confluent Cloud..."},
}

// ExecutionParams holds runtime parameters needed during cutover execution
type ExecutionParams struct {
	LagThreshold     int64
	ClusterApiKey    string
	ClusterApiSecret string
}

// CutoverOrchestrator manages the FSM lifecycle and coordinates workflow execution
type CutoverOrchestrator struct {
	config        *CutoverConfig
	fsm           *fsm.FSM
	workflow      *CutoverWorkflow
	cutoverState  *CutoverState
	stateFilePath string
	execParams    ExecutionParams // Runtime execution parameters
}

// NewCutoverOrchestrator creates a new cutover orchestrator with injected dependencies
func NewCutoverOrchestrator(
	config *CutoverConfig,
	workflow *CutoverWorkflow,
	cutoverState *CutoverState,
	stateFilePath string,
) *CutoverOrchestrator {
	orchestrator := &CutoverOrchestrator{
		config:        config,
		workflow:      workflow,
		cutoverState:  cutoverState,
		stateFilePath: stateFilePath,
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
func (o *CutoverOrchestrator) Initialize(ctx context.Context, clusterApiKey, clusterApiSecret string) error {
	// Store API credentials for use by callback
	o.execParams.ClusterApiKey = clusterApiKey
	o.execParams.ClusterApiSecret = clusterApiSecret

	if err := o.fsm.Event(ctx, EventInitialize); err != nil {
		return err
	}
	return o.persistState()
}

// Execute runs the full cutover workflow from the current state
func (o *CutoverOrchestrator) Execute(ctx context.Context, lagThreshold int64, clusterApiKey, clusterApiSecret string) error {
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

		fmt.Printf("\n%s\n", color.CyanString(step.UserMessage))
		slog.Debug("executing cutover step", "step", step.Description)
		if err := o.fsm.Event(ctx, step.Event); err != nil {
			return fmt.Errorf("failed during %s: %w", step.Description, err)
		}
		if err := o.persistState(); err != nil {
			return fmt.Errorf("failed during %s: %w", step.Description, err)
		}
		fmt.Printf("%s\n", color.GreenString("✅ Done"))
	}

	fmt.Printf("\n%s\n", color.GreenString("✅ Cutover complete!"))
	return nil
}

// beforeEventCallback is called before any event transition
func (o *CutoverOrchestrator) beforeEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("FSM: before event", "event", e.Event, "src", e.Src, "dst", e.Dst)
}

// afterEventCallback is called after any event transition
func (o *CutoverOrchestrator) afterEventCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("FSM: after event", "event", e.Event, "src", e.Src, "dst", e.Dst)
	o.config.CurrentState = e.Dst
}

// persistState saves the current cutover state to disk. Called after each successful FSM transition.
func (o *CutoverOrchestrator) persistState() error {
	if err := o.saveState(); err != nil {
		return fmt.Errorf("failed to persist state after transition to %s: %w", o.config.CurrentState, err)
	}
	return nil
}

// enterStateCallback is called when entering any state
func (o *CutoverOrchestrator) enterStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("FSM: entering state", "state", e.Dst)
}

// leaveStateCallback is called when leaving any state
func (o *CutoverOrchestrator) leaveStateCallback(ctx context.Context, e *fsm.Event) {
	slog.Debug("FSM: leaving state", "state", e.Src)
}

// leaveUninitializedCallback delegates to workflow service Initialize
func (o *CutoverOrchestrator) leaveUninitializedCallback(ctx context.Context, e *fsm.Event) {
	if err := o.workflow.Initialize(ctx, o.config, o.execParams.ClusterApiKey, o.execParams.ClusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveInitializedCallback delegates to workflow service CheckLags
func (o *CutoverOrchestrator) leaveInitializedCallback(ctx context.Context, e *fsm.Event) {
	if err := o.workflow.CheckLags(ctx, o.config, o.execParams.LagThreshold, o.execParams.ClusterApiKey, o.execParams.ClusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveLagsOkCallback delegates to workflow service FenceGateway
func (o *CutoverOrchestrator) leaveLagsOkCallback(ctx context.Context, e *fsm.Event) {
	if err := o.workflow.FenceGateway(ctx, o.config); err != nil {
		e.Cancel(err)
		return
	}
}

// leaveFencedCallback delegates to workflow service PromoteTopics
func (o *CutoverOrchestrator) leaveFencedCallback(ctx context.Context, e *fsm.Event) {
	if err := o.workflow.PromoteTopics(ctx, o.config, o.execParams.ClusterApiKey, o.execParams.ClusterApiSecret); err != nil {
		e.Cancel(err)
		return
	}
}

// leavePromotedCallback delegates to workflow service SwitchGateway
func (o *CutoverOrchestrator) leavePromotedCallback(ctx context.Context, e *fsm.Event) {
	if err := o.workflow.SwitchGateway(ctx, o.config); err != nil {
		e.Cancel(err)
		return
	}
}

// saveState persists the current cutover config to the state file
func (o *CutoverOrchestrator) saveState() error {
	o.cutoverState.UpsertCutover(*o.config)

	if err := o.cutoverState.WriteToFile(o.stateFilePath); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	return nil
}

// canTransition checks if the given event can be triggered from the current state
func (o *CutoverOrchestrator) canTransition(event string) bool {
	return o.fsm.Can(event)
}
