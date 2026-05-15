package plan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Renderer must surface a ⚠ cost callout when a customer-declared flag
// is the reason a cluster was escalated to Dedicated — a wrong `true`
// costs 5–10× monthly and the customer needs to see it inline with the
// verdict, not buried in an appendix.
func TestRenderMarkdown_CostCalloutOnCustomerDeclaredDedicated(t *testing.T) {
	cfg := defaultCfg(t)
	p := &types.Plan{
		Sizing: []types.ClusterSizing{
			{ClusterID: "wrong-click", FinalECKU: 5, P95InMBps: 10, P95OutMBps: 10, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []types.ClusterTypeDecision{
			{
				ClusterID: "wrong-click",
				Verdict:   types.ClusterTypeDedicated,
				Triggers: []types.HardLimitTrigger{
					{
						RowID:            "sla_99_95_single_zone",
						Description:      "99.95% SLA within a single zone required",
						Evidence:         "plan-inputs.yaml requires_99_95_sla_within_a_single_zone: true",
						CustomerDeclared: true,
					},
				},
			},
		},
		NetworkingDecision: []types.NetworkingDecision{
			{ClusterID: "wrong-click", Verdict: types.NetworkingPNI, Reason: "Dedicated cluster — PNI required"},
		},
	}

	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)

	assert.Contains(t, body, "⚠", "cost callout must be surfaced for customer-declared Dedicated escalations")
	assert.Contains(t, body, "5–10×", "must communicate the order-of-magnitude cost delta")
	assert.Contains(t, body, "sla_99_95_single_zone", "must name the rule that fired so the customer can find the flag")
}

// State-derived Dedicated verdicts (e.g. eCKU exceeds PNI cap) reflect
// real source-environment facts, not wrong clicks. No callout — the
// recommendation isn't recoverable by flipping a YAML flag.
func TestRenderMarkdown_NoCostCalloutOnStateDerivedDedicated(t *testing.T) {
	cfg := defaultCfg(t)
	p := &types.Plan{
		Sizing: []types.ClusterSizing{
			{ClusterID: "real-big", FinalECKU: 50, P95InMBps: 4000, P95OutMBps: 4000, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []types.ClusterTypeDecision{
			{
				ClusterID: "real-big",
				Verdict:   types.ClusterTypeDedicated,
				Triggers: []types.HardLimitTrigger{
					{
						RowID:       "eCKU_exceeds_pni_cap",
						Description: "Sized eCKU exceeds Enterprise PNI cap",
						Evidence:    "sized 50 eCKU > PNI cap 32 eCKU",
						// CustomerDeclared deliberately false — this is state-derived.
					},
				},
			},
		},
		NetworkingDecision: []types.NetworkingDecision{
			{ClusterID: "real-big", Verdict: types.NetworkingPNI, Reason: "Dedicated cluster — PNI required"},
		},
	}

	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)

	assert.NotContains(t, body, "⚠", "state-derived Dedicated escalations must not surface the wrong-click callout")
	assert.NotContains(t, body, "5–10×")
}
