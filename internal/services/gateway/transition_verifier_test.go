package gateway

import (
	"context"
	"time"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

// mockService implements the full gateway.Service interface with configurable
// func fields for the methods PatchCR/PatchConfigIDOnly drive; every other
// method is a no-op returning zero values, modeled on
// cmd/migration/execute/cmd_migration_execute_tbm_test.go's
// stubGatewayServiceImpl.
type mockService struct {
	patchGatewayRouteFn              func(ctx context.Context, namespace, gatewayName string, rp RoutePatch, configID string) (string, error)
	patchGatewayConfigIDFn           func(ctx context.Context, namespace, gatewayName, configID string) (string, error)
	getGatewayDeploymentGenerationFn func(ctx context.Context, namespace, gatewayName string) (int64, error)
}

func (m *mockService) GetGatewayYAML(context.Context, string, string) ([]byte, error) {
	return nil, nil
}

func (m *mockService) DetectCapability(context.Context, string, string, int, []byte, []byte) (Capability, error) {
	return Capability{}, nil
}

func (m *mockService) WaitForGatewayConfigID(context.Context, string, string, ConfigWaitOptions) error {
	return nil
}

func (m *mockService) CheckPermissions(context.Context, string, string, string, string) (bool, error) {
	return true, nil
}

func (m *mockService) PatchGatewayRoute(ctx context.Context, namespace, gatewayName string, rp RoutePatch, configID string) (string, error) {
	if m.patchGatewayRouteFn != nil {
		return m.patchGatewayRouteFn(ctx, namespace, gatewayName, rp, configID)
	}
	return "", nil
}

func (m *mockService) PatchGatewayConfigID(ctx context.Context, namespace, gatewayName, configID string) (string, error) {
	if m.patchGatewayConfigIDFn != nil {
		return m.patchGatewayConfigIDFn(ctx, namespace, gatewayName, configID)
	}
	return "", nil
}

func (m *mockService) WaitForGatewayAccepted(context.Context, string, string, time.Duration, time.Duration) error {
	return nil
}

func (m *mockService) GetGatewayPodUIDs(context.Context, string, string) (map[types.UID]struct{}, error) {
	return nil, nil
}

func (m *mockService) GetGatewayDeploymentGeneration(ctx context.Context, namespace, gatewayName string) (int64, error) {
	if m.getGatewayDeploymentGenerationFn != nil {
		return m.getGatewayDeploymentGenerationFn(ctx, namespace, gatewayName)
	}
	return 0, nil
}

func (m *mockService) WaitForGatewayPods(context.Context, string, string, map[types.UID]struct{}, int64, time.Duration, time.Duration, func(PodRolloutProgress)) error {
	return nil
}

func (m *mockService) WaitForGatewayReady(context.Context, string, string, int64, time.Duration, time.Duration, func(GatewayReadinessProgress)) error {
	return nil
}

// noopReporter implements Reporter with no-ops, for tests that don't assert
// on terminal output.
type noopReporter struct{}

func (noopReporter) Detail(string, ...any)      {}
func (noopReporter) Success(string, ...any)     {}
func (noopReporter) Remediation(string, ...any) {}

func TestPatchCR_GatesConfigID(t *testing.T) {
	var gotRP RoutePatch
	var gotConfigID string
	svc := &mockService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp RoutePatch, configID string) (string, error) {
			gotRP, gotConfigID = rp, configID
			return configID, nil
		},
		getGatewayDeploymentGenerationFn: func(context.Context, string, string) (int64, error) { return 7, nil },
	}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyRollout}}

	res, err := v.PatchCR(context.Background(), "ns", "gw", RoutePatch{RouteName: "r", Field: "fence", Value: 1}, "fence")
	require.NoError(t, err)
	assert.Empty(t, gotConfigID, "rollout mode must not inject a configId")
	assert.Empty(t, res.ConfigID)
	assert.Equal(t, "r", gotRP.RouteName)
	assert.Equal(t, int64(7), res.BaselineDeploymentGeneration)

	v.Capability = Capability{Mode: VerifyPerPodConfigID}
	res, err = v.PatchCR(context.Background(), "ns", "gw", RoutePatch{RouteName: "r", Field: "fence", Value: 1}, "fence")
	require.NoError(t, err)
	assert.NotEmpty(t, gotConfigID, "per-pod-configid mode must inject a configId")
	assert.Equal(t, gotConfigID, res.ConfigID)
	assert.Equal(t, "r", gotRP.RouteName)
	assert.Equal(t, "fence", gotRP.Field)
	assert.Equal(t, 1, gotRP.Value)
}

func TestPatchConfigIDOnly(t *testing.T) {
	var gotNamespace, gotGatewayName, gotConfigID string
	svc := &mockService{
		patchGatewayConfigIDFn: func(_ context.Context, namespace, gatewayName, configID string) (string, error) {
			gotNamespace, gotGatewayName, gotConfigID = namespace, gatewayName, configID
			return configID, nil
		},
		getGatewayDeploymentGenerationFn: func(context.Context, string, string) (int64, error) { return 3, nil },
	}
	// Mode is irrelevant here — PatchConfigIDOnly always injects a configId,
	// unlike PatchCR which gates on Capability.InjectsConfigID.
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyRollout}}

	res, err := v.PatchConfigIDOnly(context.Background(), "ns", "gw", "hot-reload check")
	require.NoError(t, err)
	assert.Equal(t, "ns", gotNamespace)
	assert.Equal(t, "gw", gotGatewayName)
	assert.NotEmpty(t, gotConfigID)
	assert.Equal(t, gotConfigID, res.ConfigID)
	assert.Equal(t, int64(3), res.BaselineDeploymentGeneration)
}
