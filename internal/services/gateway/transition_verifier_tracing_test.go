package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracing_PatchThenVerify_SpansEachVerifierCall(t *testing.T) {
	rec := tracingtest.Install(t)
	v := &TransitionVerifier{Service: &mockService{}, Reporter: noopReporter{}}

	applied, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "fence")
	require.NoError(t, err)
	require.NoError(t, v.WaitForAccepted(context.Background(), "confluent", "gw", "fence"))
	require.NoError(t, v.VerifyTransition(context.Background(), "confluent", "gw", 9180, applied, "fence"))

	spans := tracingtest.Spans(rec)
	assert.Equal(t, []string{"gateway.patch_cr", "gateway.wait_accepted", "gateway.verify_transition"}, tracingtest.Names(spans))
	for _, s := range spans {
		step, _ := tracingtest.Attr(s, "kcp.gateway.step")
		assert.Equal(t, "fence", step.AsString(), s.Name())
	}
	mech, _ := tracingtest.Attr(tracingtest.Find(t, spans, "gateway.verify_transition"), "kcp.gateway.mechanism")
	assert.Equal(t, "rollout", mech.AsString(), "no configId → rollout verification")
}

func TestTracing_ResolveCapability_RecordsTheMode(t *testing.T) {
	rec := tracingtest.Install(t)
	v := &TransitionVerifier{Service: &mockService{}, Reporter: noopReporter{}}
	_, err := v.ResolveCapability(context.Background(), "confluent", "gw", 9180, nil, nil)
	require.NoError(t, err)

	span := tracingtest.Find(t, tracingtest.Spans(rec), "gateway.resolve_capability")
	_, ok := tracingtest.Attr(span, "kcp.gateway.mode")
	assert.True(t, ok)
}

func TestTracing_VerifyHotReloadCapability_NoSpanWhenNotApplicable(t *testing.T) {
	rec := tracingtest.Install(t)
	v := &TransitionVerifier{Service: &mockService{}, Reporter: noopReporter{}} // zero Capability: no configId injection
	require.NoError(t, v.VerifyHotReloadCapability(context.Background(), "confluent", "gw", 9180))
	assert.Empty(t, tracingtest.Spans(rec), "a no-op check opens no span")
}

func TestTracing_WaitForConfigApplied_RecordsPodTimes(t *testing.T) {
	rec := tracingtest.Install(t)
	svc := &mockService{configWaitProgress: []ConfigWaitProgress{
		{Want: "c1", PodsTotal: 3, PodsReady: 3, PodsAtWant: 1, Mechanism: MechanismNoRollObserved, Elapsed: 2 * time.Second},
		{Want: "c1", PodsTotal: 3, PodsReady: 3, PodsAtWant: 3, Mechanism: MechanismNoRollObserved, Elapsed: 4 * time.Second, Converged: true},
	}}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}}
	require.NoError(t, v.WaitForConfigApplied(context.Background(), "confluent", "gw", 9180, ApplyResult{ConfigID: "c1"}, "fence"))

	span := tracingtest.Find(t, tracingtest.Spans(rec), "gateway.wait_config_applied")
	_, ok := tracingtest.Attr(span, "kcp.gateway.all_pods_ms")
	assert.True(t, ok)
	from, _ := tracingtest.Attr(span, "kcp.gateway.timed_from")
	assert.Equal(t, "wait_start", from.AsString(), "an ApplyResult with no patch time is timed from the wait's start")
}

// A gateway without hot reload is verified by the rollout wait; its span carries the rollout timing.
func TestTracing_VerifyTransition_RolloutRecordsAllPods(t *testing.T) {
	rec := tracingtest.Install(t)
	svc := &mockService{readinessProgress: []GatewayReadinessProgress{
		{InitialPodCount: 3, PodsReady: 2, RolloutDetected: true},
		{InitialPodCount: 3, PodsReady: 3, RolloutDetected: true, Ready: true},
	}}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}}
	require.NoError(t, v.VerifyTransition(context.Background(), "confluent", "gw", 9180, ApplyResult{PatchedAt: time.Now()}, "fence"))
	span := tracingtest.Find(t, tracingtest.Spans(rec), "gateway.verify_transition")
	_, ok := tracingtest.Attr(span, "kcp.gateway.all_pods_ms")
	assert.True(t, ok)
	mech, _ := tracingtest.Attr(span, "kcp.gateway.observed_mechanism")
	assert.Equal(t, "pod-roll", mech.AsString())
}

// Each read of the operator's signals runs inside the wait's poll and is bounded, so a slow API
// server can't hold up the wait.
func TestTracing_HotReloadReadsAreBounded(t *testing.T) {
	tracingtest.Install(t)
	var deadlines []time.Duration
	svc := &readerService{
		mockService: &mockService{configWaitProgress: []ConfigWaitProgress{
			{Want: "c1", PodsTotal: 3, PodsReady: 3, PodsAtWant: 3, Elapsed: 2 * time.Second, Converged: true},
		}},
		read: func(int) (HotReloadSignals, error) { return HotReloadSignals{}, nil },
	}
	svc.onRead = func(ctx context.Context) {
		dl, ok := ctx.Deadline()
		require.True(t, ok, "a read without a deadline")
		deadlines = append(deadlines, time.Until(dl))
	}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: hotReloadCapability}
	applied, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "fence")
	require.NoError(t, err)
	applied.ConfigID = "c1"
	require.NoError(t, v.WaitForConfigApplied(context.Background(), "confluent", "gw", 9180, applied, "fence"))
	require.Len(t, deadlines, 2)
	for _, d := range deadlines {
		assert.LessOrEqual(t, d, time.Second)
	}
}

// readerService is a mockService that also reads the operator's hot reload signals.
type readerService struct {
	*mockService
	read   func(call int) (HotReloadSignals, error)
	onRead func(ctx context.Context)
	calls  int
}

func (r *readerService) ReadHotReloadSignals(ctx context.Context, _, _ string) (HotReloadSignals, error) {
	r.calls++
	if r.onRead != nil {
		r.onRead(ctx)
	}
	return r.read(r.calls)
}

var hotReloadCapability = Capability{Mode: VerifyPerPodConfigID, HotReloadEnabled: true}

func TestTracing_WaitForConfigApplied_RecordsThePromotion(t *testing.T) {
	rec := tracingtest.Install(t)
	before := time.Now().Add(-time.Minute)
	svc := &readerService{
		mockService: &mockService{configWaitProgress: []ConfigWaitProgress{
			{Want: "c1", PodsTotal: 3, PodsReady: 3, PodsAtWant: 3, Elapsed: 2 * time.Second, Converged: true},
		}},
		read: func(call int) (HotReloadSignals, error) {
			if call == 1 { // the baseline, just before the patch
				return HotReloadSignals{Events: []OperatorEvent{{Reason: EventHotReloadAppliedDirectly, Count: 1, LastSeen: before}}}, nil
			}
			return HotReloadSignals{Events: []OperatorEvent{{Reason: EventHotReloadAppliedDirectly, Count: 2, LastSeen: time.Now()}}}, nil
		},
	}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: hotReloadCapability}
	applied, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "fence")
	require.NoError(t, err)
	require.NotNil(t, applied.HotReloadBaseline)
	require.False(t, applied.PatchedAt.IsZero())
	applied.ConfigID = "c1"
	require.NoError(t, v.WaitForConfigApplied(context.Background(), "confluent", "gw", 9180, applied, "fence"))

	span := tracingtest.Find(t, tracingtest.Spans(rec), "gateway.wait_config_applied")
	outcome, ok := tracingtest.Attr(span, "kcp.gateway.hot_reload.outcome")
	require.True(t, ok)
	assert.Equal(t, EventHotReloadAppliedDirectly, outcome.AsString())
	_, ok = tracingtest.Attr(span, "kcp.gateway.hot_reload.promoted_ms")
	assert.True(t, ok)
}

func TestTracing_WaitForConfigApplied_ReadErrorIsIgnored(t *testing.T) {
	rec := tracingtest.Install(t)
	forbidden := errors.New(`events is forbidden: User "kcp" cannot list resource "events"`)
	svc := &readerService{
		mockService: &mockService{configWaitProgress: []ConfigWaitProgress{
			{Want: "c1", PodsTotal: 3, PodsReady: 3, PodsAtWant: 1, Elapsed: 2 * time.Second},
			{Want: "c1", PodsTotal: 3, PodsReady: 3, PodsAtWant: 3, Elapsed: 4 * time.Second, Converged: true},
		}},
		read: func(int) (HotReloadSignals, error) { return HotReloadSignals{}, forbidden },
	}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: hotReloadCapability}
	applied, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "fence")
	require.NoError(t, err)
	assert.Nil(t, applied.HotReloadBaseline)
	applied.ConfigID = "c1"
	require.NoError(t, v.WaitForConfigApplied(context.Background(), "confluent", "gw", 9180, applied, "fence"))
	assert.Equal(t, 1, svc.calls, "a failed baseline read means the wait doesn't read at all")

	span := tracingtest.Find(t, tracingtest.Spans(rec), "gateway.wait_config_applied")
	for _, k := range []string{"kcp.gateway.hot_reload.outcome", "kcp.gateway.hot_reload.promoted_ms", "kcp.gateway.hot_reload.canary_started_ms"} {
		_, ok := tracingtest.Attr(span, k)
		assert.False(t, ok, k)
	}
}

func TestTracing_WaitForConfigApplied_StopsReadingAfterAnError(t *testing.T) {
	tracingtest.Install(t)
	svc := &readerService{
		mockService: &mockService{configWaitProgress: []ConfigWaitProgress{
			{Want: "c1", PodsTotal: 3, PodsReady: 3, Elapsed: 2 * time.Second},
			{Want: "c1", PodsTotal: 3, PodsReady: 3, Elapsed: 4 * time.Second},
			{Want: "c1", PodsTotal: 3, PodsReady: 3, PodsAtWant: 3, Elapsed: 6 * time.Second, Converged: true},
		}},
		read: func(call int) (HotReloadSignals, error) {
			if call == 1 {
				return HotReloadSignals{}, nil
			}
			return HotReloadSignals{}, errors.New("boom")
		},
	}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: hotReloadCapability}
	applied, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "fence")
	require.NoError(t, err)
	applied.ConfigID = "c1"
	require.NoError(t, v.WaitForConfigApplied(context.Background(), "confluent", "gw", 9180, applied, "fence"))
	assert.Equal(t, 2, svc.calls, "the baseline, then one failed read; none after")
}

func TestTracing_WaitForConfigApplied_NoReaderNoHotReloadAttributes(t *testing.T) {
	rec := tracingtest.Install(t)
	v := &TransitionVerifier{Service: &mockService{}, Reporter: noopReporter{}, Capability: hotReloadCapability}
	applied, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "fence")
	require.NoError(t, err)
	assert.Nil(t, applied.HotReloadBaseline)
	applied.ConfigID = "c1"
	require.NoError(t, v.WaitForConfigApplied(context.Background(), "confluent", "gw", 9180, applied, "fence"))
	span := tracingtest.Find(t, tracingtest.Spans(rec), "gateway.wait_config_applied")
	_, ok := tracingtest.Attr(span, "kcp.gateway.hot_reload.outcome")
	assert.False(t, ok)
}

func TestTracing_NoBaselineWithoutHotReload(t *testing.T) {
	tracingtest.Install(t)
	svc := &readerService{mockService: &mockService{}, read: func(int) (HotReloadSignals, error) { return HotReloadSignals{}, nil }}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyPerPodConfigID}}
	applied, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "fence")
	require.NoError(t, err)
	assert.Nil(t, applied.HotReloadBaseline)
	assert.Equal(t, 0, svc.calls)
}
