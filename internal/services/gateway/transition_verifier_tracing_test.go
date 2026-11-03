package gateway

import (
	"context"
	"testing"

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
