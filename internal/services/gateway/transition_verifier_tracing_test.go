package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"k8s.io/apimachinery/pkg/types"
)

func TestTracing_PatchThenVerify_SpansEachVerifierCall(t *testing.T) {
	rec := tracingtest.Install(t)
	v := &TransitionVerifier{Service: &mockService{}, Reporter: noopReporter{}}

	applied, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "fence")
	require.NoError(t, err)
	require.NoError(t, v.WaitForAccepted(context.Background(), "confluent", "gw", "fence"))
	require.NoError(t, v.VerifyTransition(context.Background(), "confluent", "gw", 9180, applied, "fence"))

	spans := tracingtest.Spans(rec)
	assert.Equal(t, []string{"gateway.patch_cr", "gateway.capture_pods", "gateway.wait_accepted", "gateway.verify_transition"}, tracingtest.Names(spans))
	for _, s := range spans {
		step, _ := tracingtest.Attr(s, "kcp.gateway.step")
		assert.Equal(t, "fence", step.AsString(), s.Name())
	}
	mech, _ := tracingtest.Attr(tracingtest.Find(t, spans, "gateway.verify_transition"), "kcp.gateway.mechanism")
	assert.Equal(t, "rollout", mech.AsString(), "no configId → rollout verification")
}

func TestTracing_PatchCR_CapturePodsSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	svc := &mockService{getGatewayPodUIDsFn: func() (map[types.UID]struct{}, error) {
		return map[types.UID]struct{}{"uid-a": {}, "uid-b": {}}, nil
	}}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyRollout}}

	_, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "switchover")
	require.NoError(t, err)

	spans := tracingtest.Spans(rec)
	capture := tracingtest.Find(t, spans, "gateway.capture_pods")
	assert.Contains(t, tracingtest.Names(tracingtest.Children(spans, tracingtest.Find(t, spans, "gateway.patch_cr"))), "gateway.capture_pods")
	pods, _ := tracingtest.Attr(capture, "kcp.gateway.pods")
	assert.Equal(t, int64(2), pods.AsInt64(), "the span records how many pods were captured, never their names")
	step, _ := tracingtest.Attr(capture, "kcp.gateway.step")
	assert.Equal(t, "switchover", step.AsString())
	tracingtest.AssertNoValues(t, spans, "uid-a", "uid-b")
}

func TestTracing_PatchCR_NoCaptureSpanWhenConfigIDInjected(t *testing.T) {
	rec := tracingtest.Install(t)
	v := &TransitionVerifier{Service: &mockService{}, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyPerPodConfigID}}

	_, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "fence")
	require.NoError(t, err)
	assert.Empty(t, tracingtest.FindAll(tracingtest.Spans(rec), "gateway.capture_pods"), "a hot-reload gateway captures nothing, so no span")
}

func TestTracing_PatchCR_CaptureFailureEndsTheSpanWithTheError(t *testing.T) {
	rec := tracingtest.Install(t)
	svc := &mockService{getGatewayPodUIDsFn: func() (map[types.UID]struct{}, error) { return nil, errors.New("list pods: boom") }}
	v := &TransitionVerifier{Service: svc, Reporter: noopReporter{}, Capability: Capability{Mode: VerifyRollout}}

	_, err := v.PatchCR(context.Background(), "confluent", "gw", RoutePatch{RouteName: "r"}, "fence")
	require.Error(t, err)

	spans := tracingtest.Spans(rec)
	assert.Equal(t, codes.Error, tracingtest.Find(t, spans, "gateway.capture_pods").Status().Code)
	_, hasPods := tracingtest.Attr(tracingtest.Find(t, spans, "gateway.capture_pods"), "kcp.gateway.pods")
	assert.False(t, hasPods, "no count is recorded for a capture that failed")
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
