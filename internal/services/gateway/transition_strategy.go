package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// applyPrep is what a strategy gathers immediately before a patch, for the
// ApplyResult the patch hands back.
type applyPrep struct {
	configID string
	podUIDs  map[types.UID]struct{}
	baseline int64
}

// transitionStrategy holds the part of a gateway transition that differs
// between a hot-reloading gateway (verified by per-pod configId) and a rolling
// one (verified by its pre-apply pods being replaced).
type transitionStrategy interface {
	name() string // "config_id" or "rollout": the kcp.gateway.mechanism span value
	prepare(ctx context.Context, v *TransitionVerifier, namespace, crName, step string) (applyPrep, error)
	confirm(ctx context.Context, v *TransitionVerifier, namespace, crName string, port int, applied ApplyResult, step string) error
}

// strategyForCapability picks the strategy a patch prepares with, from the
// resolved capability.
func strategyForCapability(c Capability) transitionStrategy {
	if c.InjectsConfigID() {
		return hotReloadStrategy{}
	}
	return rolloutStrategy{}
}

// strategyForApplied picks the strategy a verification confirms with, from the
// configId the apply actually produced rather than the capability: an
// ApplyResult without a configId must take the pod wait.
func strategyForApplied(a ApplyResult) transitionStrategy {
	if a.ConfigID != "" {
		return hotReloadStrategy{}
	}
	return rolloutStrategy{}
}

// hotReloadStrategy verifies a transition by every pod reporting the applied
// configId.
type hotReloadStrategy struct{}

func (hotReloadStrategy) name() string { return "config_id" }

func (hotReloadStrategy) prepare(ctx context.Context, v *TransitionVerifier, namespace, crName, step string) (applyPrep, error) {
	configID, err := NewConfigID()
	if err != nil {
		return applyPrep{}, err
	}
	baseline := v.DeploymentBaseline(ctx, namespace, crName, step)
	return applyPrep{configID: configID, baseline: baseline}, nil
}

func (hotReloadStrategy) confirm(ctx context.Context, v *TransitionVerifier, namespace, crName string, port int, applied ApplyResult, step string) error {
	return v.WaitForConfigApplied(ctx, namespace, crName, port, applied, step)
}

// rolloutStrategy verifies a transition by waiting for the pods that existed
// before the apply to be gone.
type rolloutStrategy struct{}

func (rolloutStrategy) name() string { return "rollout" }

func (rolloutStrategy) prepare(ctx context.Context, v *TransitionVerifier, namespace, crName, step string) (applyPrep, error) {
	// A rolling gateway replaces its pods, so record which exist before the
	// patch. Failing to capture is fatal and happens before anything is written,
	// so it is deliberately not an ErrApplyUnverified.
	podUIDs, err := v.capturePods(ctx, namespace, crName, step)
	if err != nil {
		return applyPrep{}, fmt.Errorf("failed to capture the gateway pods before %s: %w", step, err)
	}

	// A rolling gateway's later waits compare against this generation, so an
	// unreadable baseline (silently 0) could make them hang forever. Refuse
	// before anything is written; like the capture, not an ErrApplyUnverified.
	baseline, err := v.Service.GetGatewayDeploymentGeneration(ctx, namespace, crName)
	if err != nil {
		return applyPrep{}, fmt.Errorf("failed to read the gateway deployment generation before %s: %w", step, err)
	}
	return applyPrep{podUIDs: podUIDs, baseline: baseline}, nil
}

func (rolloutStrategy) confirm(ctx context.Context, v *TransitionVerifier, namespace, crName string, port int, applied ApplyResult, step string) error {
	v.Reporter.Detail("Waiting for the replaced gateway pods to be gone...")
	slog.Debug("waiting for the replaced gateway pods", "step", step, "rolloutTimeout", v.RolloutTimeout,
		"baselineDeploymentGeneration", applied.BaselineDeploymentGeneration, "preApplyPods", len(applied.PreApplyPodUIDs))

	if err := v.Service.WaitForGatewayPods(ctx, namespace, crName, applied.PreApplyPodUIDs,
		applied.BaselineDeploymentGeneration, 5*time.Second, v.RolloutTimeout, v.printPodRolloutProgress); err != nil {
		return fmt.Errorf("failed waiting for the gateway pods to be replaced during %s: %w", step, err)
	}
	return nil
}
