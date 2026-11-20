package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

// eventReporter records every reporter call, in order, into a shared events
// slice so a test can interleave them with service-side events.
type eventReporter struct{ events *[]string }

func (r eventReporter) Detail(f string, a ...any) {
	*r.events = append(*r.events, "detail:"+fmt.Sprintf(f, a...))
}

func (r eventReporter) Success(f string, a ...any) {
	*r.events = append(*r.events, "success:"+fmt.Sprintf(f, a...))
}

func (r eventReporter) Remediation(f string, a ...any) {
	*r.events = append(*r.events, "remediation:"+fmt.Sprintf(f, a...))
}

func newEventReporter() (eventReporter, *[]string) {
	events := []string{}
	return eventReporter{events: &events}, &events
}

// probeTarget is a target over the d2s golden CR and route changes, so the
// Transitioner can build real probe CRs from it.
func probeTarget(t *testing.T) Target {
	t.Helper()
	return Target{
		Namespace: "ns",
		Name:      "gw",
		GatewayCR: readGolden(t, "d2s", "cr.yaml"),
		Route:     "migration-route",
		Fence:     RouteChange{Edit: EditRouteRules, Artifact: readGolden(t, "d2s", "fence.yaml")},
		Switch:    RouteChange{Edit: EditRoute, Artifact: readGolden(t, "d2s", "switch.yaml")},
	}
}

func fenceTransition() Transition {
	return Transition{
		Step:    "fence",
		Label:   "fence",
		Change:  RouteChange{Edit: EditRouteFence, Artifact: []byte("fence:\n  blocked: true\n")},
		What:    "fenced gateway CR",
		Applied: "Fenced gateway CR applied",
	}
}

func unfenceTransition() Transition {
	return Transition{
		Step:          "unfence",
		Label:         "unfence",
		Change:        RouteChange{Edit: EditRouteFence, Artifact: []byte("fence:\n  blocked: false\n")},
		RestoresStart: true,
		What:          "unfenced gateway CR",
		Applied:       "Unfenced gateway CR applied",
	}
}

func perPodCapability() (Capability, error) {
	return Capability{Mode: VerifyPerPodConfigID}, nil
}

// echoConfigID makes a configId patch report back the id it was sent, as the
// real API server does, so the smoke test sees a stored revision.
func echoConfigID(_ context.Context, _, _, configID string) (string, error) { return configID, nil }

func uidSet(uids ...string) map[types.UID]struct{} {
	out := make(map[types.UID]struct{}, len(uids))
	for _, u := range uids {
		out[types.UID(u)] = struct{}{}
	}
	return out
}

// scriptedPods returns a GetGatewayPodUIDs hook handing out captures in order.
func scriptedPods(captures ...map[types.UID]struct{}) func() (map[types.UID]struct{}, error) {
	i := 0
	return func() (map[types.UID]struct{}, error) {
		c := captures[i]
		i++
		return c, nil
	}
}

func TestTransitionerApply_RolloutOrder(t *testing.T) {
	svc := &mockService{
		getGatewayPodUIDsFn: func() (map[types.UID]struct{}, error) { return uidSet("a"), nil },
		waitConfigIDFn: func(ConfigWaitOptions) error {
			t.Fatal("a rolling gateway must not wait on a configId")
			return nil
		},
	}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })

	result := tn.Apply(context.Background(), probeTarget(t), fenceTransition())

	assert.Equal(t, []string{"GetGatewayPodUIDs", "PatchGatewayRoute"}, svc.calls)
	assert.Equal(t, 1, svc.waitPodsCalls)
	assert.Empty(t, svc.waits)
	assert.True(t, result.Landed)
	assert.NoError(t, result.Err)
}

func TestTransitionerApply_HotReloadStampsTheConfigIDAndWaitsForIt(t *testing.T) {
	var routeConfigID string
	svc := &mockService{
		detectCapabilityFn:     perPodCapability,
		patchGatewayConfigIDFn: echoConfigID,
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ RoutePatch, configID string) (string, error) {
			routeConfigID = configID
			return configID, nil
		},
	}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec }, WithTimeouts(7*time.Minute, 3*time.Minute))

	result := tn.Apply(context.Background(), probeTarget(t), fenceTransition())

	require.True(t, result.Landed)
	require.NoError(t, result.Err)
	require.Len(t, svc.waits, 2, "the smoke test, then the transition")
	assert.NotEmpty(t, routeConfigID)
	assert.Equal(t, routeConfigID, svc.waits[1].ConfigID)
	assert.Equal(t, DefaultGatewayConfigPort, svc.waits[1].Port)
	assert.Equal(t, 7*time.Minute, svc.waits[1].RollTimeout)
	assert.Equal(t, 3*time.Minute, svc.waits[1].HotReloadTimeout)
	assert.NotContains(t, svc.calls, "GetGatewayPodUIDs")
}

func TestTransitionerApply_LandedSemantics(t *testing.T) {
	patchBoom := errors.New("patch boom")
	acceptedBoom := errors.New("accepted boom")
	podsBoom := errors.New("pods boom")

	tests := []struct {
		name       string
		patchErr   error
		accepted   error
		pods       error
		wantLanded bool
		check      func(t *testing.T, err error)
	}{
		{
			name: "patch returns a generic error", patchErr: patchBoom, wantLanded: false,
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, patchBoom)
				assert.Contains(t, err.Error(), "failed to apply fenced gateway CR: ")
			},
		},
		{
			name: "patch returns ErrApplyUnverified", patchErr: fmt.Errorf("could not confirm: %w", ErrApplyUnverified), wantLanded: true,
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, ErrApplyUnverified)
			},
		},
		{
			name: "WaitForAccepted fails", accepted: acceptedBoom, wantLanded: true,
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, acceptedBoom)
				assert.NotContains(t, err.Error(), "failed to apply")
				assert.NotContains(t, err.Error(), "fenced gateway CR")
			},
		},
		{
			name: "VerifyTransition fails", pods: podsBoom, wantLanded: true,
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, podsBoom)
				assert.NotContains(t, err.Error(), "failed to apply")
			},
		},
		{
			name: "all succeed", wantLanded: true,
			check: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockService{
				patchErr:       tt.patchErr,
				waitAcceptedFn: func() error { return tt.accepted },
				waitForGatewayPodsFn: func(map[types.UID]struct{}, int64, time.Duration, time.Duration) error {
					return tt.pods
				},
			}
			rec, _ := newEventReporter()
			tn := NewTransitioner(svc, func() Reporter { return rec })

			result := tn.Apply(context.Background(), probeTarget(t), fenceTransition())

			assert.Equal(t, tt.wantLanded, result.Landed)
			tt.check(t, result.Err)
		})
	}
}

func TestTransitionerApply_AppliedMessagePrintsAfterThePatchAndBeforeTheAcceptanceWait(t *testing.T) {
	rec, events := newEventReporter()
	svc := &mockService{
		waitAcceptedFn: func() error {
			*events = append(*events, "accepted-wait")
			return nil
		},
	}
	tn := NewTransitioner(svc, func() Reporter { return rec })

	result := tn.Apply(context.Background(), probeTarget(t), fenceTransition())
	require.True(t, result.Landed)
	require.NoError(t, result.Err)

	successAt, waitAt := -1, -1
	for i, e := range *events {
		switch e {
		case "success:Fenced gateway CR applied":
			successAt = i
		case "accepted-wait":
			waitAt = i
		}
	}
	require.NotEqual(t, -1, successAt, "the applied message must be printed: %v", *events)
	require.NotEqual(t, -1, waitAt, "the acceptance wait must run: %v", *events)
	assert.Less(t, successAt, waitAt, "the applied message must precede the acceptance wait: %v", *events)
	assert.Contains(t, svc.calls, "PatchGatewayRoute")
}

// runFenceFenceUnfence drives two fences and an unfence on a rolling gateway
// and returns the pod set the unfence waited on.
func runFenceFenceUnfence(t *testing.T, target Target) map[types.UID]struct{} {
	t.Helper()
	var initials []map[types.UID]struct{}
	svc := &mockService{
		getGatewayPodUIDsFn: scriptedPods(uidSet("pre-1"), uidSet("pre-1", "f1"), uidSet("pre-1", "f1", "f2")),
		waitForGatewayPodsFn: func(initial map[types.UID]struct{}, _ int64, _, _ time.Duration) error {
			initials = append(initials, initial)
			return nil
		},
	}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })

	fence1 := fenceTransition()
	fence1.Step = "fence-1"
	fence2 := fenceTransition()
	fence2.Step = "fence-2"
	for _, tr := range []Transition{fence1, fence2, unfenceTransition()} {
		result := tn.Apply(context.Background(), target, tr)
		require.True(t, result.Landed, tr.Step)
		require.NoError(t, result.Err, tr.Step)
	}
	require.Len(t, initials, 3)
	return initials[2]
}

func TestTransitionerApply_StartSetCapturedOnceAndOnlyItsPodsSkippedOnRestore(t *testing.T) {
	got := runFenceFenceUnfence(t, probeTarget(t))
	assert.Equal(t, uidSet("f1", "f2"), got, "only the start-of-run pod is skipped; a later fence's pod is still waited on")
}

func TestTransitionerApply_StartRouteFencedKeepsEveryPod(t *testing.T) {
	target := probeTarget(t)
	target.StartRouteFenced = true
	got := runFenceFenceUnfence(t, target)
	assert.Equal(t, uidSet("pre-1", "f1", "f2"), got)
}

func TestTransitionerApply_RestoreWithNoEarlierFenceWaitsOnEveryPod(t *testing.T) {
	var initial map[types.UID]struct{}
	svc := &mockService{
		getGatewayPodUIDsFn: scriptedPods(uidSet("a", "b")),
		waitForGatewayPodsFn: func(got map[types.UID]struct{}, _ int64, _, _ time.Duration) error {
			initial = got
			return nil
		},
	}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })

	result := tn.Apply(context.Background(), probeTarget(t), unfenceTransition())

	require.True(t, result.Landed)
	require.NoError(t, result.Err)
	assert.Equal(t, uidSet("a", "b"), initial)
}

func TestTransitionerEnsureCapability_ResolvesAndSmokeTestsOnce(t *testing.T) {
	configIDPatches := 0
	svc := &mockService{
		detectCapabilityFn: perPodCapability,
		patchGatewayConfigIDFn: func(ctx context.Context, ns, gw, configID string) (string, error) {
			configIDPatches++
			return echoConfigID(ctx, ns, gw, configID)
		},
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ RoutePatch, configID string) (string, error) {
			return configID, nil
		},
	}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })

	for i := 0; i < 2; i++ {
		result := tn.Apply(context.Background(), probeTarget(t), fenceTransition())
		require.True(t, result.Landed)
		require.NoError(t, result.Err)
	}

	assert.Equal(t, 1, svc.detects)
	assert.Equal(t, 1, configIDPatches, "exactly one configId-only patch: the smoke test")
	assert.Equal(t, VerifyPerPodConfigID, tn.Capability().Mode)
}

func TestTransitionerResolveCapability_MarksTheSmokeTestAttempted(t *testing.T) {
	configIDPatches := 0
	svc := &mockService{
		detectCapabilityFn: perPodCapability,
		patchGatewayConfigIDFn: func(ctx context.Context, ns, gw, configID string) (string, error) {
			configIDPatches++
			return echoConfigID(ctx, ns, gw, configID)
		},
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ RoutePatch, configID string) (string, error) {
			return configID, nil
		},
	}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })

	require.NoError(t, tn.ResolveCapability(context.Background(), probeTarget(t)))
	require.Equal(t, VerifyPerPodConfigID, tn.Capability().Mode)

	result := tn.Apply(context.Background(), probeTarget(t), fenceTransition())

	require.True(t, result.Landed)
	require.NoError(t, result.Err)
	assert.Equal(t, 1, svc.detects)
	assert.Equal(t, 0, configIDPatches, "a direct ResolveCapability owns the smoke test, so Apply must not run it")
}

func TestTransitionerApply_AFailedSmokeTestIsNotRetried(t *testing.T) {
	configIDPatches := 0
	smokeBoom := errors.New("smoke boom")
	waitCalls := 0
	svc := &mockService{
		detectCapabilityFn: perPodCapability,
		patchGatewayConfigIDFn: func(ctx context.Context, ns, gw, configID string) (string, error) {
			configIDPatches++
			return echoConfigID(ctx, ns, gw, configID)
		},
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ RoutePatch, configID string) (string, error) {
			return configID, nil
		},
		waitConfigIDFn: func(ConfigWaitOptions) error {
			waitCalls++
			if waitCalls == 1 {
				return smokeBoom
			}
			return nil
		},
	}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })

	first := tn.Apply(context.Background(), probeTarget(t), fenceTransition())
	assert.False(t, first.Landed)
	require.Error(t, first.Err)
	assert.Contains(t, first.Err.Error(), "failed to resolve gateway capability")
	assert.ErrorIs(t, first.Err, smokeBoom)
	assert.NotContains(t, svc.calls, "PatchGatewayRoute")

	second := tn.Apply(context.Background(), probeTarget(t), fenceTransition())
	assert.True(t, second.Landed)
	assert.NoError(t, second.Err)
	assert.Equal(t, 1, svc.detects)
	assert.Equal(t, 1, configIDPatches, "the failed smoke test must not be retried")
	assert.Contains(t, svc.calls, "PatchGatewayRoute")
}

func TestTransitionerApply_ProbeErrorStopsBeforeAnyWrite(t *testing.T) {
	svc := &mockService{}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })
	target := probeTarget(t)
	target.GatewayCR = []byte("{{ not yaml")

	result := tn.Apply(context.Background(), target, fenceTransition())

	assert.False(t, result.Landed)
	require.Error(t, result.Err)
	assert.Contains(t, result.Err.Error(), "failed to derive fenced gateway CR")
	assert.Empty(t, svc.calls)
	assert.Empty(t, svc.patchExpected)
	assert.Equal(t, 0, svc.detects)
}

func TestTransitionerGeneration(t *testing.T) {
	svc := &mockService{
		patchResults:           []PatchResult{{Generation: 8}},
		getGatewayGenerationFn: func(context.Context, string, string) (int64, error) { return 9, nil },
	}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })
	tn.SetExpectedGeneration(7)

	result := tn.Apply(context.Background(), probeTarget(t), fenceTransition())
	require.True(t, result.Landed)
	require.NoError(t, result.Err)

	require.NotEmpty(t, svc.patchExpected)
	assert.Equal(t, int64(7), svc.patchExpected[0])
	assert.Equal(t, int64(8), tn.ExpectedGeneration())

	err := tn.CheckGeneration(context.Background(), probeTarget(t), "switch")
	var gce *GenerationChangedError
	require.True(t, errors.As(err, &gce), "want a *GenerationChangedError, got %v", err)
	assert.Equal(t, int64(8), gce.Expected)
	assert.Equal(t, int64(9), gce.Observed)
}

func TestTransitionerApply_WithConfigPortReachesTheVerifyWait(t *testing.T) {
	tests := []struct {
		port int
		want int
	}{
		{-1, DefaultGatewayConfigPort},
		{0, DefaultGatewayConfigPort},
		{DefaultGatewayConfigPort + 1, DefaultGatewayConfigPort + 1},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("port %d", tt.port), func(t *testing.T) {
			svc := &mockService{
				detectCapabilityFn:     perPodCapability,
				patchGatewayConfigIDFn: echoConfigID,
				patchGatewayRouteFn: func(_ context.Context, _, _ string, _ RoutePatch, configID string) (string, error) {
					return configID, nil
				},
			}
			rec, _ := newEventReporter()
			tn := NewTransitioner(svc, func() Reporter { return rec }, WithConfigPort(tt.port))

			result := tn.Apply(context.Background(), probeTarget(t), fenceTransition())

			require.True(t, result.Landed)
			require.NoError(t, result.Err)
			require.Len(t, svc.waits, 2)
			assert.Equal(t, tt.want, svc.waits[1].Port)
		})
	}
}

func TestNewTransitioner_WithTimeoutsReachesTheWaits(t *testing.T) {
	svc := &mockService{
		detectCapabilityFn:     perPodCapability,
		patchGatewayConfigIDFn: echoConfigID,
		patchGatewayRouteFn: func(_ context.Context, _, _ string, _ RoutePatch, configID string) (string, error) {
			return configID, nil
		},
	}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec }, WithTimeouts(7*time.Minute, 3*time.Minute))

	result := tn.Apply(context.Background(), probeTarget(t), fenceTransition())

	require.True(t, result.Landed)
	require.NoError(t, result.Err)
	require.Len(t, svc.waits, 2, "the smoke test, then the transition")
	assert.Equal(t, 7*time.Minute, svc.waits[1].RollTimeout)
	assert.Equal(t, 3*time.Minute, svc.waits[1].HotReloadTimeout)
}

func TestTransitionerHasClient(t *testing.T) {
	rep := func() Reporter { return &recordingReporter{} }
	assert.True(t, NewTransitioner(&mockService{}, rep).HasClient())
	assert.False(t, NewTransitioner(nil, rep).HasClient())
}

func TestTransitionerCheckObservedGeneration(t *testing.T) {
	target := Target{Namespace: "ns", Name: "gw"}
	tests := []struct {
		name               string
		expected, observed int64
		wantErr            bool
	}{
		{"expected unknown", 0, 7, false},
		{"observed unknown", 5, 0, false},
		{"equal", 5, 5, false},
		{"moved", 5, 7, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tn := &Transitioner{}
			tn.SetExpectedGeneration(tc.expected)
			err := tn.CheckObservedGeneration(target, tc.observed)
			if !tc.wantErr {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrGatewayChanged)
			var gce *GenerationChangedError
			require.True(t, errors.As(err, &gce))
			assert.Equal(t, int64(5), gce.Expected)
			assert.Equal(t, int64(7), gce.Observed)
			assert.Equal(t, "ns", gce.Namespace)
			assert.Equal(t, "gw", gce.Gateway)
		})
	}
}

func TestTransitionerBuildsProbeCRsFromTheTarget(t *testing.T) {
	target := probeTarget(t)
	wantFenced, wantSwitched, err := RouteProbe(target.GatewayCR, target.Route, target.Fence, target.Switch)
	require.NoError(t, err)
	assert.Equal(t, string(readGolden(t, "d2s", "fenced_cr.yaml")), string(wantFenced))
	assert.Equal(t, string(readGolden(t, "d2s", "switched_cr.yaml")), string(wantSwitched))

	svc := &mockService{}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })

	require.NoError(t, tn.ResolveCapability(context.Background(), target))
	require.NoError(t, tn.ResolveCapability(context.Background(), target))

	assert.Equal(t, 1, svc.detects, "the probe runs at most once per Transitioner")
	assert.Equal(t, string(wantFenced), string(svc.detectFenced))
	assert.Equal(t, string(wantSwitched), string(svc.detectSwitched))
}

func TestTransitionerProbeErrorsKeepTheirWrappers(t *testing.T) {
	t.Run("a bad CR fails the fenced derivation", func(t *testing.T) {
		target := probeTarget(t)
		target.GatewayCR = []byte("{{ not yaml")
		svc := &mockService{}
		rec, _ := newEventReporter()
		err := NewTransitioner(svc, func() Reporter { return rec }).ResolveCapability(context.Background(), target)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to derive fenced gateway CR: failed to parse gateway CR YAML")
		assert.Equal(t, 0, svc.detects)
	})
	t.Run("a bad switch edit fails the switched derivation", func(t *testing.T) {
		target := probeTarget(t)
		target.Switch = RouteChange{Edit: RouteEdit(99)}
		svc := &mockService{}
		rec, _ := newEventReporter()
		err := NewTransitioner(svc, func() Reporter { return rec }).ResolveCapability(context.Background(), target)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to derive switched gateway CR: unknown route edit 99")
		assert.Equal(t, 0, svc.detects)
	})
}

func TestTransitionerWithConfigPort(t *testing.T) {
	tests := []struct {
		name string
		opts []TransitionerOption
		want int
	}{
		{"unset selects the default", nil, DefaultGatewayConfigPort},
		{"zero selects the default", []TransitionerOption{WithConfigPort(0)}, DefaultGatewayConfigPort},
		{"negative selects the default", []TransitionerOption{WithConfigPort(-1)}, DefaultGatewayConfigPort},
		{"a configured port reaches DetectCapability", []TransitionerOption{WithConfigPort(19180)}, 19180},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockService{}
			rec, _ := newEventReporter()
			tn := NewTransitioner(svc, func() Reporter { return rec }, tt.opts...)
			require.NoError(t, tn.ResolveCapability(context.Background(), probeTarget(t)))
			assert.Equal(t, tt.want, svc.detectPort)
		})
	}
}

func TestTransitionerApply_DerivesThePatchFromTheChange(t *testing.T) {
	var got RoutePatch
	svc := &mockService{
		patchGatewayRouteFn: func(_ context.Context, _, _ string, rp RoutePatch, _ string) (string, error) {
			got = rp
			return "", nil
		},
	}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })
	target := probeTarget(t)
	tr := fenceTransition()

	result := tn.Apply(context.Background(), target, tr)

	require.NoError(t, result.Err)
	want, err := tr.Change.Patch(target.Route)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, target.Route, got.RouteName)
}

func TestTransitionerApply_ABadArtifactStopsBeforeAnyWrite(t *testing.T) {
	svc := &mockService{}
	rec, _ := newEventReporter()
	tn := NewTransitioner(svc, func() Reporter { return rec })
	tr := fenceTransition()
	tr.Label = "switch"
	tr.Change.Artifact = []byte("not: the-fragment\n")

	result := tn.Apply(context.Background(), probeTarget(t), tr)

	assert.False(t, result.Landed)
	require.Error(t, result.Err)
	assert.Contains(t, result.Err.Error(), "failed to build switch route patch: ")
	assert.Contains(t, result.Err.Error(), "no top-level fence key")
	assert.Empty(t, svc.calls)
	assert.Empty(t, svc.patchExpected)
	assert.Equal(t, 0, svc.detects)
}
