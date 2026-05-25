package plan

import (
	"strings"
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
			{ClusterID: "wrong-click", FinalECKU: 5, SizedInMBps: 10, SizedOutMBps: 10, MaxRatioDriver: "ingress"},
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

// Spiky FYI guards against P95 == 0 (the spiky flag fires for any
// positive peak when P95 is zero — `peak > 2.0 * 0`). Without the
// guard the renderer would print "+Inf" or "NaN" as the multiplier.
func TestRenderMarkdown_SpikyNoteHandlesZeroP95(t *testing.T) {
	cfg := defaultCfg(t)
	p := &types.Plan{
		Sizing: []types.ClusterSizing{
			{ClusterID: "zero-p95", FinalECKU: 1, SizedInMBps: 0, PeakInMBps: 5, SpikyIngress: true, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []types.ClusterTypeDecision{{ClusterID: "zero-p95", Verdict: types.ClusterTypeEnterprise}},
		NetworkingDecision:  []types.NetworkingDecision{{ClusterID: "zero-p95", Verdict: types.NetworkingPrivateLink, Reason: "fits"}},
	}
	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)
	assert.NotContains(t, body, "+Inf", "must not leak +Inf from peak/p95 when p95 is zero")
	assert.NotContains(t, body, "NaN")
	assert.Contains(t, body, "no P95 baseline", "must surface the missing-baseline phrase instead of a bogus ratio")
}

// Dedicated clusters render with MZ/SZ topology suffix so the reader
// sees at a glance whether the verdict is Multi-Zone or the
// 99.95%-SLA-driven Single-Zone variant. The size column flips from
// eCKU to CKU because Dedicated uses a different unit.
func TestRenderMarkdown_DedicatedTopologyAndCKULabel(t *testing.T) {
	cfg := defaultCfg(t)
	cku := 4
	p := &types.Plan{
		Sizing: []types.ClusterSizing{
			{ClusterID: "ent-small", FinalECKU: 2, SizedInMBps: 10, SizedOutMBps: 20, MaxRatioDriver: "ingress"},
			{ClusterID: "ded-mz", FinalECKU: 4, SizedInMBps: 100, SizedOutMBps: 180, MaxRatioDriver: "ingress"},
			{ClusterID: "ded-sz", FinalECKU: 4, SizedInMBps: 50, SizedOutMBps: 80, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []types.ClusterTypeDecision{
			{ClusterID: "ent-small", Verdict: types.ClusterTypeEnterprise},
			{
				ClusterID: "ded-mz",
				Verdict:   types.ClusterTypeDedicated,
				Topology:  types.TopologyMultiZone,
				FinalCKU:  &cku,
				Triggers:  []types.HardLimitTrigger{{RowID: "eCKU_exceeds_pni_cap", Description: "x", Evidence: "y"}},
			},
			{
				ClusterID: "ded-sz",
				Verdict:   types.ClusterTypeDedicated,
				Topology:  types.TopologySingleZone,
				FinalCKU:  &cku,
				Triggers:  []types.HardLimitTrigger{{RowID: "sla_99_95_single_zone", Description: "99.95 SZ", Evidence: "flag", CustomerDeclared: true}},
			},
		},
		NetworkingDecision: []types.NetworkingDecision{
			{ClusterID: "ent-small", Verdict: types.NetworkingPrivateLink, Reason: "fits"},
			{ClusterID: "ded-mz", Verdict: types.NetworkingPNI, Reason: "Dedicated"},
			{ClusterID: "ded-sz", Verdict: types.NetworkingPNI, Reason: "Dedicated"},
		},
	}

	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)

	// Enterprise row: unit stays as eCKU, no topology suffix.
	assert.Contains(t, body, "2 eCKU")
	assert.Regexp(t, `ent-small.*Enterprise`, splitLine(body, "ent-small"))

	// Dedicated MZ row: unit flips to CKU, label gets MZ suffix.
	assert.Contains(t, body, "4 CKU")
	assert.Contains(t, body, "Dedicated Multi-Zone (MZ)")

	// Dedicated SZ row: same unit, SZ suffix.
	assert.Contains(t, body, "Dedicated Single-Zone (SZ)")
}

// splitLine returns the substring of body that starts with the cluster
// name through end-of-line — handy for per-row assertions.
func splitLine(body, clusterID string) string {
	idx := strings.Index(body, clusterID)
	if idx < 0 {
		return ""
	}
	rel := strings.Index(body[idx:], "\n")
	if rel < 0 {
		return body[idx:]
	}
	return body[idx : idx+rel]
}

// State-derived Dedicated verdicts (e.g. eCKU exceeds PNI cap) reflect
// real source-environment facts, not wrong clicks. No callout — the
// recommendation isn't recoverable by flipping a YAML flag.
func TestRenderMarkdown_NoCostCalloutOnStateDerivedDedicated(t *testing.T) {
	cfg := defaultCfg(t)
	p := &types.Plan{
		Sizing: []types.ClusterSizing{
			{ClusterID: "real-big", FinalECKU: 50, SizedInMBps: 4000, SizedOutMBps: 4000, MaxRatioDriver: "ingress"},
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

// Clusters whose source scan didn't populate the load-bearing signals
// (topics + ACLs on a PROVISIONED cluster) render every downstream
// column as `_deferred_` in both the Sizing & Cluster Decisions table
// and the Sizing Math appendix — we won't ship a confident verdict
// against missing data. Regression guard: an incomplete-scan cluster
// must NOT render the numeric "1 eCKU Enterprise PNI" pattern.
func TestRenderMarkdown_ScanIncompleteRendersDeferred(t *testing.T) {
	cfg := defaultCfg(t)
	p := &types.Plan{
		Sizing: []types.ClusterSizing{
			{ClusterID: "ok", FinalECKU: 1, SizedInMBps: 5, SizedOutMBps: 8, MaxRatioDriver: "ingress"},
			{ClusterID: "gap", FinalECKU: 1, ScanIncomplete: true, SLAFloorECKU: 1, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []types.ClusterTypeDecision{
			{ClusterID: "ok", Verdict: types.ClusterTypeEnterprise},
			{ClusterID: "gap", Verdict: types.ClusterTypeEnterprise},
		},
		NetworkingDecision: []types.NetworkingDecision{
			{ClusterID: "ok", Verdict: types.NetworkingPNI, Reason: "PNI default"},
			{ClusterID: "gap", Verdict: types.NetworkingPNI, Reason: "PNI default"},
		},
		SizingAppendix: []types.SizingMathDetail{
			{ClusterID: "ok", Formula: "CEIL(max(P95In/60, P95Out/180, partitions/3000) * (1 + 0.30 headroom))"},
		},
	}
	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)

	// Main decisions table: gap cluster shows _deferred_ across the verdict columns.
	assert.Contains(t, body, "| gap | _scan incomplete_ | _unknown_ | _deferred_ | _deferred_ | _deferred_ |",
		"incomplete-scan cluster must render every downstream column as deferred in the Sizing table")
	// "Why These Recommendations" should explain the deferral.
	assert.Contains(t, body, "**gap** — sizing **deferred**",
		"incomplete-scan cluster must surface a deferred rationale, not a confident verdict")
	// Sizing-math appendix row must also defer.
	assert.Contains(t, body, "| gap | _scan incomplete_ | _scan incomplete_ | _unknown_ | _n/a_ | _deferred_ |",
		"incomplete-scan cluster must render the appendix row as deferred too")
	// The healthy cluster's numeric verdict must still render — we're
	// gating per-cluster, not blanket-suppressing the whole table.
	assert.Contains(t, body, "| ok | 5.0 / 8.0 | 0 | 1 eCKU | Enterprise | PNI |",
		"fully-scanned cluster must still render its numeric verdict alongside deferred ones")

	// Negative checks: no confident verdict leaked for the gap cluster.
	for _, badPattern := range []string{"| gap | 0.0 / 0.0", "gap | 1 eCKU"} {
		assert.False(t, strings.Contains(body, badPattern),
			"incomplete-scan cluster must not render the confident-verdict pattern %q", badPattern)
	}
}
