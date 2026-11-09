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
	"k8s.io/client-go/kubernetes"
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

	// getGatewayPodUIDsFn scripts GetGatewayPodUIDs; calls records the order of
	// the capture and patch calls.
	getGatewayPodUIDsFn func() (map[types.UID]struct{}, error)
	calls               []string

	// waitForGatewayPodsFn scripts WaitForGatewayPods (nil = succeed);
	// waitPodsCalls and waitReadyCalls count invocations of the two rollout waits.
	waitForGatewayPodsFn func(initial map[types.UID]struct{}, baseline int64, poll, timeout time.Duration) error
	waitPodsCalls        int
	waitReadyCalls       int
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
	m.calls = append(m.calls, "PatchGatewayRoute")
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
	m.calls = append(m.calls, "GetGatewayPodUIDs")
	if m.getGatewayPodUIDsFn != nil {
		return m.getGatewayPodUIDsFn()
	}
	return nil, nil
}

func (m *mockService) GetGatewayDeploymentGeneration(ctx context.Context, namespace, gatewayName string) (int64, error) {
	if m.getGatewayDeploymentGenerationFn != nil {
		return m.getGatewayDeploymentGenerationFn(ctx, namespace, gatewayName)
	}
	return 0, nil
}

func (m *mockService) WaitForGatewayPods(_ context.Context, _, _ string, initial map[types.UID]struct{}, baseline int64, poll, timeout time.Duration, _ func(PodRolloutProgress)) error {
	m.waitPodsCalls++
	if m.waitForGatewayPodsFn != nil {
		return m.waitForGatewayPodsFn(initial, baseline, poll, timeout)
	}
	return nil
}

func (m *mockService) WaitForGatewayReady(context.Context, string, string, int64, time.Duration, time.Duration, func(GatewayReadinessProgress)) error {
	m.waitReadyCalls++
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

func TestPatchCR_CapturesPodUIDsWhenRolling(t *testing.T) {
	want := map[types.UID]struct{}{"uid-a": {}, "uid-b": {}}
	svc := &mockService{getGatewayPodUIDsFn: func() (map[types.UID]struct{}, error) { return want, nil }}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyRollout}}

	res, err := v.PatchCR(context.Background(), "ns", "gw", RoutePatch{RouteName: "r", Field: "fence", Value: 1}, "fence")
	require.NoError(t, err)
	assert.Equal(t, want, res.PreApplyPodUIDs)
	assert.Equal(t, []string{"GetGatewayPodUIDs", "PatchGatewayRoute"}, svc.calls, "pods must be captured before the patch")
}

func TestPatchCR_DoesNotCaptureWhenConfigIDInjected(t *testing.T) {
	svc := &mockService{getGatewayPodUIDsFn: func() (map[types.UID]struct{}, error) {
		return map[types.UID]struct{}{"uid-a": {}}, nil
	}}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyPerPodConfigID}}

	res, err := v.PatchCR(context.Background(), "ns", "gw", RoutePatch{RouteName: "r", Field: "fence", Value: 1}, "fence")
	require.NoError(t, err)
	assert.NotContains(t, svc.calls, "GetGatewayPodUIDs")
	assert.Nil(t, res.PreApplyPodUIDs)
}

func TestPatchCR_CaptureFailureAbortsBeforePatch(t *testing.T) {
	svc := &mockService{getGatewayPodUIDsFn: func() (map[types.UID]struct{}, error) { return nil, errors.New("list pods: boom") }}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyRollout}}

	_, err := v.PatchCR(context.Background(), "ns", "gw", RoutePatch{RouteName: "r", Field: "fence", Value: 1}, "fence")
	require.Error(t, err)
	assert.NotContains(t, svc.calls, "PatchGatewayRoute")
	assert.False(t, errors.Is(err, ErrApplyUnverified))
}

func TestPatchCR_BaselineReadFailureAbortsBeforePatchWhenRolling(t *testing.T) {
	svc := &mockService{getGatewayDeploymentGenerationFn: func(context.Context, string, string) (int64, error) {
		return 0, errors.New("get deployment: boom")
	}}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyRollout}}

	_, err := v.PatchCR(context.Background(), "ns", "gw", RoutePatch{RouteName: "r", Field: "fence", Value: 1}, "fence")
	require.Error(t, err)
	assert.NotContains(t, svc.calls, "PatchGatewayRoute")
	assert.False(t, errors.Is(err, ErrApplyUnverified))
	assert.Contains(t, err.Error(), "before fence")
}

func TestPatchCR_BaselineReadFailureStillPatchesWhenConfigIDInjected(t *testing.T) {
	svc := &mockService{getGatewayDeploymentGenerationFn: func(context.Context, string, string) (int64, error) {
		return 0, errors.New("get deployment: boom")
	}}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyPerPodConfigID}}

	res, err := v.PatchCR(context.Background(), "ns", "gw", RoutePatch{RouteName: "r", Field: "fence", Value: 1}, "fence")
	require.NoError(t, err)
	assert.Equal(t, int64(0), res.BaselineDeploymentGeneration)
	assert.Contains(t, svc.calls, "PatchGatewayRoute")
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

func TestVerifyTransition_RolloutWaitsForReplacedPods(t *testing.T) {
	var gotInitial map[types.UID]struct{}
	var gotBaseline int64
	var gotPoll, gotTimeout time.Duration
	svc := &mockService{waitForGatewayPodsFn: func(initial map[types.UID]struct{}, baseline int64, poll, timeout time.Duration) error {
		gotInitial, gotBaseline, gotPoll, gotTimeout = initial, baseline, poll, timeout
		return nil
	}}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyRollout}, RolloutTimeout: 7 * time.Minute}
	applied := ApplyResult{BaselineDeploymentGeneration: 12, PreApplyPodUIDs: map[types.UID]struct{}{"uid-a": {}}}

	require.NoError(t, v.VerifyTransition(context.Background(), "ns", "gw", 9180, applied, "fence"))

	assert.Equal(t, 1, svc.waitPodsCalls)
	assert.Equal(t, 0, svc.waitReadyCalls, "the rollout branch must not fall back to the readiness-only wait")
	assert.Equal(t, applied.PreApplyPodUIDs, gotInitial)
	assert.Equal(t, int64(12), gotBaseline)
	assert.Equal(t, 5*time.Second, gotPoll)
	assert.Equal(t, 7*time.Minute, gotTimeout)
}

func TestVerifyTransition_RolloutWaitErrorIsWrapped(t *testing.T) {
	boom := errors.New("boom")
	svc := &mockService{waitForGatewayPodsFn: func(map[types.UID]struct{}, int64, time.Duration, time.Duration) error { return boom }}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyRollout}}

	err := v.VerifyTransition(context.Background(), "ns", "gw", 9180, ApplyResult{}, "switchover")

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "failed waiting for the gateway pods to be replaced during switchover")
}

// realWaitService delegates WaitForGatewayPods to the real waitForGatewayPods
// over a fake clientset, so a verifier test exercises the genuine wait. It
// substitutes a short poll interval for the verifier's 5s so the test is fast.
type realWaitService struct {
	mockService
	cs kubernetes.Interface
}

func (r *realWaitService) WaitForGatewayPods(ctx context.Context, ns, name string, initial map[types.UID]struct{}, baseline int64, _, timeout time.Duration, onProgress func(PodRolloutProgress)) error {
	return waitForGatewayPods(ctx, r.cs, ns, name, initial, baseline, 5*time.Millisecond, timeout, onProgress)
}

func TestVerifyTransition_NoRollReturnsImmediately(t *testing.T) {
	shortenRollConfirmationWindow(t, 20*time.Millisecond)
	ns, gw := "test-ns", "test-gw"
	// The Deployment generation never advances past the baseline: no roll.
	cs := newFakeClientset(
		newGatewayPod("gw-old", ns, gw, "old-1", true),
		completeGatewayDeployment(gw, ns, 1),
	)
	svc := &realWaitService{cs: cs}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyRollout}, RolloutTimeout: 5 * time.Second}
	applied := ApplyResult{BaselineDeploymentGeneration: 1, PreApplyPodUIDs: map[types.UID]struct{}{"old-1": {}}}

	start := time.Now()
	err := v.VerifyTransition(context.Background(), ns, gw, 9180, applied, "fence")

	require.NoError(t, err)
	assert.Less(t, time.Since(start), 3*time.Second, "a no-roll apply must not wait out the timeout")
}

// recordingReporter captures rendered Detail lines.
type recordingReporter struct{ details []string }

func (r *recordingReporter) Detail(f string, a ...any) {
	r.details = append(r.details, fmt.Sprintf(f, a...))
}
func (r *recordingReporter) Success(string, ...any)     {}
func (r *recordingReporter) Remediation(string, ...any) {}

func TestPrintPodRolloutProgress_ShowsElapsedAndOldPodNames(t *testing.T) {
	rep := &recordingReporter{}
	v := &TransitionVerifier{Reporter: rep}
	v.printPodRolloutProgress(PodRolloutProgress{
		InitialPodCount: 5, NewPodsReady: 1, OldPodsRemaining: 5, RolloutDetected: true,
		Elapsed: 90 * time.Second, OldPodNames: []string{"a", "b", "c", "d", "e"},
	})
	require.Len(t, rep.details, 1)
	line := rep.details[0]
	assert.Contains(t, line, "1/5 new pods ready, 5 old pods remaining")
	assert.Contains(t, line, FormatElapsed(90*time.Second))
	assert.Contains(t, line, "a, b, c and 2 more")
	assert.NotContains(t, line, "d,")
}

func TestPrintPodRolloutProgress_FewNamesListedInFull(t *testing.T) {
	rep := &recordingReporter{}
	v := &TransitionVerifier{Reporter: rep}
	v.printPodRolloutProgress(PodRolloutProgress{
		InitialPodCount: 2, OldPodsRemaining: 2, RolloutDetected: true, OldPodNames: []string{"a", "b"},
	})
	require.Len(t, rep.details, 1)
	assert.Contains(t, rep.details[0], "a, b")
	assert.NotContains(t, rep.details[0], "more")
}

func TestPrintConfigWaitProgress_InPlaceShowsTerminating(t *testing.T) {
	rep := &recordingReporter{}
	v := &TransitionVerifier{Reporter: rep}
	v.printConfigWaitProgress(ConfigWaitProgress{
		Mechanism: MechanismNoRollObserved, PodsReady: 2, PodsAtWant: 2, PodsTerminating: 1,
	})
	require.Len(t, rep.details, 1)
	assert.Contains(t, rep.details[0], "1 terminating")

	rep.details = nil
	v.printConfigWaitProgress(ConfigWaitProgress{Mechanism: MechanismNoRollObserved, PodsReady: 2, PodsAtWant: 2})
	assert.NotContains(t, rep.details[0], "terminating")
}

func TestExcludePods(t *testing.T) {
	set := map[types.UID]struct{}{"a": {}, "b": {}, "c": {}}
	exclude := map[types.UID]struct{}{"b": {}}

	got := ExcludePods(set, exclude)

	assert.Equal(t, map[types.UID]struct{}{"a": {}, "c": {}}, got)
	assert.Equal(t, map[types.UID]struct{}{"a": {}, "b": {}, "c": {}}, set, "the set must not be mutated")
	assert.Equal(t, map[types.UID]struct{}{"b": {}}, exclude, "the exclusion must not be mutated")

	assert.Empty(t, ExcludePods(nil, exclude), "nil set gives an empty result")
	assert.Nil(t, ExcludePods(nil, nil), "nil set stays nil, so a hot-reload capture keeps meaning no pods")
	assert.Equal(t, set, ExcludePods(set, nil), "nil exclusion keeps every pod")
	got = ExcludePods(set, nil)
	got["d"] = struct{}{}
	assert.NotContains(t, set, types.UID("d"), "the result is a new map even with nothing excluded")
}
