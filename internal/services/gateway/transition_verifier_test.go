package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

// mockService implements the full gateway.Service interface with configurable
// func fields for the methods PatchCR/PatchConfigIDOnly drive; every other
// method is a no-op returning zero values, modeled on
// cmd/migration/cutover/execute/migration_dependencies_test.go's
// stubGatewayServiceImpl.
type mockService struct {
	patchGatewayRouteFn              func(ctx context.Context, namespace, gatewayName string, rp RoutePatch, configID string) (string, error)
	patchGatewayConfigIDFn           func(ctx context.Context, namespace, gatewayName, configID string) (string, error)
	getGatewayGenerationFn           func(ctx context.Context, namespace, name string) (int64, error)
	getGatewayDeploymentGenerationFn func(ctx context.Context, namespace, gatewayName string) (int64, error)

	// patchExpected records each patch call's expectedGeneration; patchResults
	// scripts the PatchResult of successive patch calls (consulted only when the
	// matching Fn is nil), and patchErr is returned alongside them.
	patchExpected  []int64
	patchResults   []PatchResult
	patchErr       error
	generationGets int
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

func (m *mockService) PatchGatewayRoute(ctx context.Context, namespace, gatewayName string, rp RoutePatch, configID string, expectedGeneration int64) (PatchResult, error) {
	m.patchExpected = append(m.patchExpected, expectedGeneration)
	if m.patchGatewayRouteFn != nil {
		str, err := m.patchGatewayRouteFn(ctx, namespace, gatewayName, rp, configID)
		return PatchResult{ConfigID: str}, err
	}
	return m.nextPatchResult()
}

func (m *mockService) PatchGatewayConfigID(ctx context.Context, namespace, gatewayName, configID string, expectedGeneration int64) (PatchResult, error) {
	m.patchExpected = append(m.patchExpected, expectedGeneration)
	if m.patchGatewayConfigIDFn != nil {
		str, err := m.patchGatewayConfigIDFn(ctx, namespace, gatewayName, configID)
		return PatchResult{ConfigID: str}, err
	}
	return m.nextPatchResult()
}

func (m *mockService) nextPatchResult() (PatchResult, error) {
	var res PatchResult
	if i := len(m.patchExpected) - 1; i < len(m.patchResults) {
		res = m.patchResults[i]
	}
	return res, m.patchErr
}

func (m *mockService) GetGatewayGeneration(ctx context.Context, namespace, name string) (int64, error) {
	m.generationGets++
	if m.getGatewayGenerationFn != nil {
		return m.getGatewayGenerationFn(ctx, namespace, name)
	}
	return 0, nil
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

func TestTransitionVerifier_GenerationFollowsOwnWrites(t *testing.T) {
	svc := &mockService{patchResults: []PatchResult{{Generation: 5}, {Generation: 6}, {Generation: 6}}}
	gen := int64(4)
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Generation: &gen}

	_, err := v.PatchConfigIDOnly(context.Background(), "ns", "gw", "smoke test")
	require.NoError(t, err)
	_, err = v.PatchCR(context.Background(), "ns", "gw", RoutePatch{}, "fence")
	require.NoError(t, err)
	_, err = v.PatchCR(context.Background(), "ns", "gw", RoutePatch{}, "switch")
	require.NoError(t, err)

	assert.Equal(t, []int64{4, 5, 6}, svc.patchExpected)
	assert.Equal(t, int64(6), gen)
}

func TestTransitionVerifier_UnverifiedWriteAdvancesGeneration(t *testing.T) {
	svc := &mockService{
		patchResults: []PatchResult{{Generation: 5}},
		patchErr:     fmt.Errorf("could not confirm: %w", ErrApplyUnverified),
	}
	gen := int64(4)
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Generation: &gen}

	_, err := v.PatchCR(context.Background(), "ns", "gw", RoutePatch{}, "fence")
	require.ErrorIs(t, err, ErrApplyUnverified)
	assert.Equal(t, int64(5), gen)
}

func TestTransitionVerifier_CheckGeneration(t *testing.T) {
	five, zero := int64(5), int64(0)
	tests := []struct {
		name     string
		expected *int64
		observed int64
		wantErr  bool
		wantGets int
	}{
		{"equal", &five, 5, false, 1},
		{"moved", &five, 7, true, 1},
		{"zero is unknown", &zero, 7, false, 0},
		{"nil pointer is unknown", nil, 7, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockService{getGatewayGenerationFn: func(context.Context, string, string) (int64, error) {
				return tt.observed, nil
			}}
			v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Generation: tt.expected}

			err := v.CheckGeneration(context.Background(), "ns", "gw", "fence")
			assert.Equal(t, tt.wantGets, svc.generationGets)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrGatewayChanged)
			var gce *GenerationChangedError
			require.True(t, errors.As(err, &gce))
			assert.Equal(t, int64(7), gce.Observed)
			assert.Equal(t, int64(5), gce.Expected)
		})
	}
}
