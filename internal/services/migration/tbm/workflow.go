package tbm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/services/migplan"
)

// TransitionSimulatedDelay is how long each noop action sleeps to simulate
// real execution timing, until real per-batch migration logic replaces it. A
// package variable, not a const, so tests can shrink it — see
// setFastTransitions in workflow_test.go.
var TransitionSimulatedDelay = 7 * time.Second

// TBMActions holds the business logic behind each FSM transition. Initialize
// is real (see below); every other method is still a noop that sleeps
// TransitionSimulatedDelay — cancellable via ctx, mirroring the wait pattern
// in migration.MigrationActions.CheckLags — then reports completion.
type TBMActions struct {
	reporter *reporter
}

// NewTBMActions creates a new TBMActions.
func NewTBMActions() *TBMActions {
	return &TBMActions{reporter: newReporter()}
}

// simulateTransition is the shared noop body every still-noop action method calls.
func (a *TBMActions) simulateTransition(ctx context.Context, doneMsg string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(TransitionSimulatedDelay):
	}
	a.reporter.success("%s", doneMsg)
	return nil
}

// Initialize runs the initialize transition: validates the reconcile plan
// migplan.Reconcile already computed live (before Execute was even invoked)
// and, if the plan is feasible, captures its artifacts onto config for every
// later transition to consume — never re-derived. res.Refused is checked
// before config is touched, so a cancelled transition never leaves config
// partially mutated. There is no simulated delay here: the expensive work
// (contacting source/target/gateway/cluster-link) already happened producing
// res; this step is pure validate-and-copy.
func (a *TBMActions) Initialize(ctx context.Context, config *TBMConfig, res *migplan.Result) error {
	if res.Refused {
		return fmt.Errorf("reconcile plan refused:\n%s", strings.Join(res.Reasons, "\n"))
	}

	config.Topics = res.Topics
	config.FenceYAML = res.FenceYAML
	config.SwitchoverYAML = res.SwitchoverYAML
	config.GatewayYAML = res.GatewayYAML

	a.reporter.success("TBM migration initialized (%d topic(s) in plan)", len(res.Topics))
	return nil
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
