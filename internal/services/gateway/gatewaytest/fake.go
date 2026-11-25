// Package gatewaytest holds the shared test fake for gateway.TransitionClient,
// used by the migration, tbm and d2s FSM test packages.
package gatewaytest

import (
	"context"
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// Fake implements gateway.TransitionClient using function fields for test
// control. Unset fields fall back to the defaults documented per method; the
// error texts keep their historical "mockGatewayService" prefix. Not safe for
// concurrent use: RouteExpected is appended unlocked.
type Fake struct {
	DetectCapabilityFn     func(ctx context.Context, namespace, name string, port int, fenced, switchover []byte) (gateway.Capability, error)
	WaitForConfigIDFn      func(ctx context.Context, namespace, name string, opts gateway.ConfigWaitOptions) error
	PatchGatewayRouteFn    func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, configID string) (string, error)
	PatchGatewayConfigIDFn func(ctx context.Context, namespace, name, configID string) (string, error)
	GetGatewayGenerationFn func(ctx context.Context, namespace, name string) (int64, error)
	// PatchRouteGenFn, when set, takes precedence over PatchGatewayRouteFn and
	// scripts the full PatchResult (generation included) or a refusal.
	PatchRouteGenFn func(ctx context.Context, namespace, name string, rp gateway.RoutePatch, configID string, expectedGeneration int64) (gateway.PatchResult, error)
	// RouteExpected records every PatchGatewayRoute call's expectedGeneration.
	RouteExpected            []int64
	WaitForGatewayAcceptedFn func(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration) error
	GetGatewayPodUIDsFn      func(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error)
	GetDeploymentGenFn       func(ctx context.Context, namespace, name string) (int64, error)
	WaitForGatewayPodsFn     func(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error
}

// DetectCapability defaults to VerifyRollout — the mode every pre-hot-reload
// cluster gets — so tests that do not care about hot-reload keep exercising the
// pre-existing rollout path.
func (m *Fake) DetectCapability(ctx context.Context, namespace, name string, port int, fenced, switchover []byte) (gateway.Capability, error) {
	if m.DetectCapabilityFn != nil {
		return m.DetectCapabilityFn(ctx, namespace, name, port, fenced, switchover)
	}
	return gateway.Capability{Mode: gateway.VerifyRollout}, nil
}

func (m *Fake) WaitForGatewayConfigID(ctx context.Context, namespace, name string, opts gateway.ConfigWaitOptions) error {
	if m.WaitForConfigIDFn != nil {
		return m.WaitForConfigIDFn(ctx, namespace, name, opts)
	}
	return nil
}

func (m *Fake) PatchGatewayRoute(ctx context.Context, namespace, name string, rp gateway.RoutePatch, configID string, expectedGeneration int64) (gateway.PatchResult, error) {
	m.RouteExpected = append(m.RouteExpected, expectedGeneration)
	if m.PatchRouteGenFn != nil {
		return m.PatchRouteGenFn(ctx, namespace, name, rp, configID, expectedGeneration)
	}
	if m.PatchGatewayRouteFn != nil {
		str, err := m.PatchGatewayRouteFn(ctx, namespace, name, rp, configID)
		return gateway.PatchResult{ConfigID: str}, err
	}
	return gateway.PatchResult{}, fmt.Errorf("mockGatewayService.PatchGatewayRoute not configured")
}

func (m *Fake) PatchGatewayConfigID(ctx context.Context, namespace, name, configID string, _ int64) (gateway.PatchResult, error) {
	if m.PatchGatewayConfigIDFn != nil {
		str, err := m.PatchGatewayConfigIDFn(ctx, namespace, name, configID)
		return gateway.PatchResult{ConfigID: str}, err
	}
	return gateway.PatchResult{}, fmt.Errorf("mockGatewayService.PatchGatewayConfigID not configured")
}

func (m *Fake) GetGatewayGeneration(ctx context.Context, namespace, name string) (int64, error) {
	if m.GetGatewayGenerationFn != nil {
		return m.GetGatewayGenerationFn(ctx, namespace, name)
	}
	return 0, nil
}

func (m *Fake) WaitForGatewayAccepted(ctx context.Context, namespace, name string, pollInterval, timeout time.Duration) error {
	if m.WaitForGatewayAcceptedFn != nil {
		return m.WaitForGatewayAcceptedFn(ctx, namespace, name, pollInterval, timeout)
	}
	return nil
}

func (m *Fake) GetGatewayPodUIDs(ctx context.Context, namespace, name string) (map[k8stypes.UID]struct{}, error) {
	if m.GetGatewayPodUIDsFn != nil {
		return m.GetGatewayPodUIDsFn(ctx, namespace, name)
	}
	// A one-UID default: PatchCR captures the pods of a rolling gateway before
	// every patch. Tests that care override GetGatewayPodUIDsFn.
	return map[k8stypes.UID]struct{}{"default-pod": {}}, nil
}

// GetGatewayDeploymentGeneration defaults to 0 — the "could not read it"
// baseline, which the rollout waits treat as "any generation counts as a bump".
// Tests that care about the baseline set GetDeploymentGenFn.
func (m *Fake) GetGatewayDeploymentGeneration(ctx context.Context, namespace, name string) (int64, error) {
	if m.GetDeploymentGenFn != nil {
		return m.GetDeploymentGenFn(ctx, namespace, name)
	}
	return 0, nil
}

func (m *Fake) WaitForGatewayPods(ctx context.Context, namespace, name string, initialPodUIDs map[k8stypes.UID]struct{}, baselineGeneration int64, pollInterval, timeout time.Duration, onProgress func(gateway.PodRolloutProgress)) error {
	if m.WaitForGatewayPodsFn != nil {
		return m.WaitForGatewayPodsFn(ctx, namespace, name, initialPodUIDs, baselineGeneration, pollInterval, timeout, onProgress)
	}
	return nil
}
