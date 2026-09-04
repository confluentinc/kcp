package tbm

import (
	"context"
	"time"
)

// TransitionSimulatedDelay is how long each noop action sleeps to simulate
// real execution timing, until real per-batch migration logic replaces it. A
// package variable, not a const, so tests can shrink it — see
// setFastTransitions in workflow_test.go.
var TransitionSimulatedDelay = 7 * time.Second

// TBMActions holds the (currently noop) business logic behind each FSM
// transition. Every method sleeps TransitionSimulatedDelay — cancellable via
// ctx, mirroring the wait pattern in migration.MigrationActions.CheckLags —
// then reports completion. No method fails except via ctx cancellation.
type TBMActions struct {
	reporter *reporter
}

// NewTBMActions creates a new TBMActions.
func NewTBMActions() *TBMActions {
	return &TBMActions{reporter: newReporter()}
}

// simulateTransition is the shared noop body every action method calls.
func (a *TBMActions) simulateTransition(ctx context.Context, doneMsg string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(TransitionSimulatedDelay):
	}
	a.reporter.success("%s", doneMsg)
	return nil
}

// Initialize runs the initialize transition.
func (a *TBMActions) Initialize(ctx context.Context, config *TBMConfig) error {
	return a.simulateTransition(ctx, "TBM migration initialized")
}

// WaitForLags runs the wait_for_lags transition.
func (a *TBMActions) WaitForLags(ctx context.Context, config *TBMConfig) error {
	return a.simulateTransition(ctx, "Replication lags OK")
}

// Fence runs the fence transition.
func (a *TBMActions) Fence(ctx context.Context, config *TBMConfig) error {
	return a.simulateTransition(ctx, "Batch fenced")
}

// VerifyFence runs the verify_fence transition.
func (a *TBMActions) VerifyFence(ctx context.Context, config *TBMConfig) error {
	return a.simulateTransition(ctx, "Fence verified")
}

// Promote runs the promote transition.
func (a *TBMActions) Promote(ctx context.Context, config *TBMConfig) error {
	return a.simulateTransition(ctx, "Batch promoted")
}

// Switch runs the switch transition.
func (a *TBMActions) Switch(ctx context.Context, config *TBMConfig) error {
	return a.simulateTransition(ctx, "Batch switched")
}
