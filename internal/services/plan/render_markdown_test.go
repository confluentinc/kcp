package plan

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Renderer must surface a cost callout when a customer-declared flag
// is the reason a cluster was escalated to Dedicated — a wrong `true`
// raises the monthly cost and the customer needs to see it inline with
// the verdict, not buried in an appendix.
func TestRenderMarkdown_CostCalloutOnCustomerDeclaredDedicated(t *testing.T) {
	cfg := defaultCfg(t)
	p := &Plan{
		Sizing: []ClusterSizing{
			{ClusterID: "wrong-click", FinalECKU: 5, SizedInMBps: 10, SizedOutMBps: 10, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []ClusterTypeDecision{
			{
				ClusterID: "wrong-click",
				Verdict:   ClusterTypeDedicated,
				Triggers: []HardLimitTrigger{
					{
						RowID:            "sla_99_95_single_zone",
						Description:      "99.95% SLA within a single zone required",
						Evidence:         "plan-inputs.yaml requires_99_95_sla_within_a_single_zone: true",
						CustomerDeclared: true,
					},
				},
			},
		},
		NetworkingDecision: []NetworkingDecision{
			{ClusterID: "wrong-click", Verdict: NetworkingPNI, Reason: "Dedicated cluster — PNI required"},
		},
	}

	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)

	assert.Contains(t, body, "**Cost callout", "cost callout must be surfaced for customer-declared Dedicated escalations")
	assert.Contains(t, body, "higher monthly cost", "must communicate the cost-direction signal without naming a multiplier")
	assert.Contains(t, body, "sla_99_95_single_zone", "must name the rule that fired so the customer can find the flag")
}

// Spiky FYI guards against P95 == 0 (the spiky flag fires for any
// positive peak when P95 is zero — `peak > 2.0 * 0`). Without the
// guard the renderer would print "+Inf" or "NaN" as the multiplier.
func TestRenderMarkdown_SpikyNoteHandlesZeroP95(t *testing.T) {
	cfg := defaultCfg(t)
	p := &Plan{
		Sizing: []ClusterSizing{
			{ClusterID: "zero-p95", FinalECKU: 1, SizedInMBps: 0, PeakInMBps: 5, SpikyIngress: true, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []ClusterTypeDecision{{ClusterID: "zero-p95", Verdict: ClusterTypeEnterprise}},
		NetworkingDecision:  []NetworkingDecision{{ClusterID: "zero-p95", Verdict: NetworkingPrivateLink, Reason: "fits"}},
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
	p := &Plan{
		Sizing: []ClusterSizing{
			{ClusterID: "ent-small", FinalECKU: 2, SizedInMBps: 10, SizedOutMBps: 20, MaxRatioDriver: "ingress"},
			{ClusterID: "ded-mz", FinalECKU: 4, SizedInMBps: 100, SizedOutMBps: 180, MaxRatioDriver: "ingress"},
			{ClusterID: "ded-sz", FinalECKU: 4, SizedInMBps: 50, SizedOutMBps: 80, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []ClusterTypeDecision{
			{ClusterID: "ent-small", Verdict: ClusterTypeEnterprise},
			{
				ClusterID: "ded-mz",
				Verdict:   ClusterTypeDedicated,
				Topology:  TopologyMultiZone,
				FinalCKU:  &cku,
				Triggers:  []HardLimitTrigger{{RowID: "eCKU_exceeds_pni_cap", Description: "x", Evidence: "y"}},
			},
			{
				ClusterID: "ded-sz",
				Verdict:   ClusterTypeDedicated,
				Topology:  TopologySingleZone,
				FinalCKU:  &cku,
				Triggers:  []HardLimitTrigger{{RowID: "sla_99_95_single_zone", Description: "99.95 SZ", Evidence: "flag", CustomerDeclared: true}},
			},
		},
		NetworkingDecision: []NetworkingDecision{
			{ClusterID: "ent-small", Verdict: NetworkingPrivateLink, Reason: "fits"},
			{ClusterID: "ded-mz", Verdict: NetworkingPNI, Reason: "Dedicated"},
			{ClusterID: "ded-sz", Verdict: NetworkingPNI, Reason: "Dedicated"},
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
	p := &Plan{
		Sizing: []ClusterSizing{
			{ClusterID: "real-big", FinalECKU: 50, SizedInMBps: 4000, SizedOutMBps: 4000, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []ClusterTypeDecision{
			{
				ClusterID: "real-big",
				Verdict:   ClusterTypeDedicated,
				Triggers: []HardLimitTrigger{
					{
						RowID:       "eCKU_exceeds_pni_cap",
						Description: "Sized eCKU exceeds Enterprise PNI cap",
						Evidence:    "sized 50 eCKU > PNI cap 32 eCKU",
						// CustomerDeclared deliberately false — this is state-derived.
					},
				},
			},
		},
		NetworkingDecision: []NetworkingDecision{
			{ClusterID: "real-big", Verdict: NetworkingPNI, Reason: "Dedicated cluster — PNI required"},
		},
	}

	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)

	assert.NotContains(t, body, "**Cost callout:**", "state-derived Dedicated must not surface the customer-declared wrong-click callout")
	// State-derived Dedicated DOES get a separate cost-direction note
	// (round-3 feedback: bigger cost decisions than SLA-SZ shouldn't be
	// silent). It's labelled "Cost direction" not "Cost callout" and
	// frames recovery as "not recoverable by editing plan-inputs.yaml"
	// — semantically distinct from the wrong-click flow.
	assert.Contains(t, body, "**Cost direction:**", "state-derived Dedicated must surface the cost-direction note")
}

// Clusters whose source scan didn't populate the load-bearing signals
// still render their cluster-type + networking verdicts (those are
// deterministic given the inputs that ARE present), but the sizing
// column flips to provisional (`*` marker + `_unknown_` partitions)
// and the Why line carries an "Inputs missing:" note so the reader
// sees what to fix.
func TestRenderMarkdown_InputsMissingMarksProvisional(t *testing.T) {
	cfg := defaultCfg(t)
	p := &Plan{
		Sizing: []ClusterSizing{
			{ClusterID: "ok", FinalECKU: 1, SizedInMBps: 5, SizedOutMBps: 8, MaxRatioDriver: "ingress"},
			{ClusterID: "gap", FinalECKU: 1, SLAFloorECKU: 1, MaxRatioDriver: "ingress", InputsMissing: []string{"topics", "acls"}},
		},
		ClusterTypeDecision: []ClusterTypeDecision{
			{ClusterID: "ok", Verdict: ClusterTypeEnterprise},
			{ClusterID: "gap", Verdict: ClusterTypeEnterprise, InputsMissing: []string{"topics", "acls"}},
		},
		NetworkingDecision: []NetworkingDecision{
			{ClusterID: "ok", Verdict: NetworkingPNI, Reason: "PNI default"},
			{ClusterID: "gap", Verdict: NetworkingPNI, Reason: "PNI default"},
		},
		SizingAppendix: []SizingMathDetail{
			{ClusterID: "ok", Formula: "CEIL(max(P95In/60, P95Out/180, partitions/3000) * (1 + 0.30 headroom))"},
		},
	}
	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)

	// Verdict columns still render for the gap cluster — Enterprise + PNI are computable
	// even without scan signals (no rule could have fired anyway).
	assert.Contains(t, body, "| gap | 0.0 / 0.0 | _unknown_ | 1 eCKU * | Enterprise | PNI |",
		"inputs-missing cluster must still render its verdict, just with a provisional marker on sizing")
	// Why line carries the Inputs missing note.
	assert.Contains(t, body, "_Inputs missing: topics, acls",
		"inputs-missing cluster must surface the missing signals inline so the reader knows what to fix")
	// `*` legend appears once when any cluster is provisional.
	assert.Contains(t, body, "`*` = sizing is provisional",
		"the `*` marker legend must appear after the table when any cluster is provisional")
	// The healthy cluster renders unchanged.
	assert.Contains(t, body, "| ok | 5.0 / 8.0 | 0 | 1 eCKU | Enterprise | PNI |",
		"fully-scanned cluster must render its numeric verdict unchanged")

	// Negative checks: the old `_deferred_` shape should not appear anywhere now.
	for _, badPattern := range []string{"_deferred_", "_scan incomplete_"} {
		assert.False(t, strings.Contains(body, badPattern),
			"old deferral shape %q must not appear after refactor — verdicts now render with InputsMissing", badPattern)
	}
}

// Pipe character in a cluster name or evidence string must not break
// the rendered Appendix A2 table — `escapeMarkdownTableCell` is what
// keeps the table parseable. Regression guard for the markdown-
// injection vector the code reviewer flagged.
func TestRenderMarkdown_AppendixA2EscapesPipeInTableCells(t *testing.T) {
	cfg := defaultCfg(t)
	p := &Plan{
		Sizing: []ClusterSizing{
			{ClusterID: "weird|name", FinalECKU: 1, SizedInMBps: 1, SizedOutMBps: 1, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []ClusterTypeDecision{
			{
				ClusterID: "weird|name",
				Verdict:   ClusterTypeEnterprise,
				EvaluatedRules: []RuleEvaluation{
					{RowID: "demo", Description: "Demo rule", Outcome: RuleNotFired, Evidence: "value a|b"},
				},
			},
		},
		NetworkingDecision: []NetworkingDecision{{ClusterID: "weird|name", Verdict: NetworkingPNI, Reason: "x"}},
	}
	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)
	assert.Contains(t, body, `weird\|name`, "cluster name with | must be escaped in the appendix cell")
	assert.Contains(t, body, `value a\|b`, "evidence with | must be escaped")
	assert.False(t, strings.Contains(body, "| weird|name |"), "raw unescaped | must not appear inside a table row")
}

// Cost callout aggregates multiple customer-declared triggers into a
// single warning when more than one fires on the same cluster — verify
// the labels are joined by `;` not duplicated, and the human Description
// appears for each.
func TestRenderMarkdown_CostCalloutMultipleTriggers(t *testing.T) {
	cfg := defaultCfg(t)
	p := &Plan{
		Sizing: []ClusterSizing{
			{ClusterID: "combo", FinalECKU: 2, SizedInMBps: 10, SizedOutMBps: 10, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []ClusterTypeDecision{{
			ClusterID: "combo",
			Verdict:   ClusterTypeDedicated,
			Triggers: []HardLimitTrigger{
				{RowID: "sla_99_95_single_zone", Description: "99.95% single-zone SLA required", CustomerDeclared: true},
				{RowID: "broker_side_schema_validation_required", Description: "Broker-side schema ID validation required", CustomerDeclared: true},
			},
		}},
		NetworkingDecision: []NetworkingDecision{{ClusterID: "combo", Verdict: NetworkingPNI, Reason: "Dedicated"}},
	}
	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)
	assert.Contains(t, body, "99.95% single-zone SLA required")
	assert.Contains(t, body, "Broker-side schema ID validation required")
	// Both labels appear in a single cost-callout line, separated by `;`.
	assert.True(t, strings.Contains(body, "99.95% single-zone SLA required (`sla_99_95_single_zone`); Broker-side schema ID validation required (`broker_side_schema_validation_required`)") ||
		strings.Contains(body, "Broker-side schema ID validation required (`broker_side_schema_validation_required`); 99.95% single-zone SLA required (`sla_99_95_single_zone`)"),
		"cost callout must join multiple trigger labels with `;`")
}

// Negative assertion: a fully-scanned healthy cluster must NOT carry
// the `_Inputs missing_` Why-line note or a `*` provisional marker.
func TestRenderMarkdown_HealthyClusterHasNoProvisionalMarker(t *testing.T) {
	cfg := defaultCfg(t)
	p := &Plan{
		Sizing: []ClusterSizing{
			{ClusterID: "ok", FinalECKU: 1, SizedInMBps: 5, SizedOutMBps: 8, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []ClusterTypeDecision{{ClusterID: "ok", Verdict: ClusterTypeEnterprise}},
		NetworkingDecision:  []NetworkingDecision{{ClusterID: "ok", Verdict: NetworkingPNI, Reason: "default"}},
	}
	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)
	assert.NotContains(t, body, "_Inputs missing", "healthy cluster must not surface the provisional note")
	assert.NotContains(t, body, "1 eCKU *", "healthy cluster's Final size must not carry the provisional `*` marker")
	assert.NotContains(t, body, "`*` = sizing is provisional", "legend must not appear when no cluster is provisional")
}

// Plan C-shape regression: a customer-declared trigger that fires on
// every cluster gets one banner up top, not N repeated per-cluster
// callouts.
func TestRenderMarkdown_GlobalTriggerCollapsesToBanner(t *testing.T) {
	cfg := defaultCfg(t)
	mkTrigger := func(id, desc string) []HardLimitTrigger {
		return []HardLimitTrigger{{RowID: id, Description: desc, CustomerDeclared: true}}
	}
	p := &Plan{
		Sizing: []ClusterSizing{
			{ClusterID: "a", FinalECKU: 1, MaxRatioDriver: "ingress"},
			{ClusterID: "b", FinalECKU: 1, MaxRatioDriver: "ingress"},
			{ClusterID: "c", FinalECKU: 1, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []ClusterTypeDecision{
			{ClusterID: "a", Verdict: ClusterTypeDedicated, Topology: TopologySingleZone, Triggers: mkTrigger("sla_99_95_single_zone", "99.95% single-zone SLA required")},
			{ClusterID: "b", Verdict: ClusterTypeDedicated, Topology: TopologySingleZone, Triggers: mkTrigger("sla_99_95_single_zone", "99.95% single-zone SLA required")},
			{ClusterID: "c", Verdict: ClusterTypeDedicated, Topology: TopologySingleZone, Triggers: mkTrigger("sla_99_95_single_zone", "99.95% single-zone SLA required")},
		},
		NetworkingDecision: []NetworkingDecision{
			{ClusterID: "a", Verdict: NetworkingPNI, Reason: "Dedicated"},
			{ClusterID: "b", Verdict: NetworkingPNI, Reason: "Dedicated"},
			{ClusterID: "c", Verdict: NetworkingPNI, Reason: "Dedicated"},
		},
	}
	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)
	bannerCount := strings.Count(body, "applies to every cluster below")
	// Per-cluster callouts use the prefix `  - **Cost callout:**`
	// (note the leading `  - `); the banner uses `> **Cost callout
	// (applies to every cluster below):**`. Count only the per-cluster
	// shape to verify it didn't ALSO fire when the global banner did.
	perClusterCount := strings.Count(body, "  - **Cost callout:**")
	szTradeoffCount := strings.Count(body, "Single-Zone resilience tradeoff")
	assert.Equal(t, 1, bannerCount, "global trigger must collapse to exactly one banner up top")
	assert.Equal(t, 0, perClusterCount, "the per-cluster cost callout must NOT fire when the banner already did")
	assert.Equal(t, 1, szTradeoffCount, "SZ resilience tradeoff must collapse to one banner when the SZ trigger is global")
}

// slaFloorList renders the SLA-floor table inline; verify it's
// deterministic (sorted by key) and pulls values from cfg.
func TestSlaFloorList_SortedDeterministic(t *testing.T) {
	cfg := defaultCfg(t)
	out := slaFloorList(cfg)
	// Default config: 99.9 → 1, 99.95 → 1, 99.99 → 2
	assert.Contains(t, out, "1 eCKU for 99.9")
	assert.Contains(t, out, "1 eCKU for 99.95")
	assert.Contains(t, out, "2 eCKU for 99.99")
	// Deterministic order — 99.9 sorts before 99.95 sorts before 99.99.
	assert.True(t, strings.Index(out, "99.9") < strings.Index(out, "99.95") &&
		strings.Index(out, "99.95") < strings.Index(out, "99.99"),
		"slaFloorList must be sorted by SLA key for deterministic output")
}

// Partial-fire: trigger fires on 2 of 3 clusters → it is NOT global,
// so the per-cluster cost callout must still appear inline on each
// firing cluster (the global banner must NOT appear).
func TestDetectGlobalCustomerTriggers_PartialFireKeepsInline(t *testing.T) {
	cfg := defaultCfg(t)
	mkTrigger := func() []HardLimitTrigger {
		return []HardLimitTrigger{{RowID: "sla_99_95_single_zone", Description: "99.95% single-zone SLA required", CustomerDeclared: true}}
	}
	p := &Plan{
		Sizing: []ClusterSizing{
			{ClusterID: "a", FinalECKU: 1, MaxRatioDriver: "ingress"},
			{ClusterID: "b", FinalECKU: 1, MaxRatioDriver: "ingress"},
			{ClusterID: "c", FinalECKU: 1, MaxRatioDriver: "ingress"},
		},
		ClusterTypeDecision: []ClusterTypeDecision{
			// a + b fire; c doesn't (Enterprise default)
			{ClusterID: "a", Verdict: ClusterTypeDedicated, Topology: TopologySingleZone, Triggers: mkTrigger()},
			{ClusterID: "b", Verdict: ClusterTypeDedicated, Topology: TopologySingleZone, Triggers: mkTrigger()},
			{ClusterID: "c", Verdict: ClusterTypeEnterprise},
		},
		NetworkingDecision: []NetworkingDecision{
			{ClusterID: "a", Verdict: NetworkingPNI, Reason: "Dedicated"},
			{ClusterID: "b", Verdict: NetworkingPNI, Reason: "Dedicated"},
			{ClusterID: "c", Verdict: NetworkingPNI, Reason: "default for AWS Enterprise"},
		},
	}
	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)
	bannerCount := strings.Count(body, "applies to every cluster below")
	perClusterCount := strings.Count(body, "  - **Cost callout:**")
	szTradeoffCount := strings.Count(body, "Single-Zone resilience tradeoff")
	assert.Equal(t, 0, bannerCount, "partial fire (2 of 3) must NOT collapse to a global banner")
	// Both firing clusters share identical rationale + identical extras,
	// so they collapse to ONE bullet with both clusters listed and the
	// inline cost callout rendered once. The non-firing cluster (c) is
	// its own bullet with no callout. Pre-fix behavior rendered the
	// same paragraph N times; post-fix collapses identical paragraphs.
	assert.Equal(t, 1, perClusterCount, "identical inline cost callouts must collapse to one bullet across firing clusters")
	assert.Equal(t, 1, szTradeoffCount, "identical SZ tradeoff callouts must collapse to one bullet across firing clusters")
}

// groupOpenQuestions collapses two OQs identical except for the
// embedded `--region <X>` flag in HowToClose: the per-region command
// difference must NOT defeat the grouping, and the renderer should
// swap a `<region>` placeholder once multiple distinct regions exist
// in the merged group.
func TestGroupOpenQuestions_RegionFlagNormalizationCollapses(t *testing.T) {
	oqs := []OpenQuestion{
		{
			ID:         "auth_posture_unknown",
			ClusterID:  "a",
			Title:      "No client-authentication methods detected on the source — auth migration recommendation is unconfirmed",
			Body:       "Body shared across regions",
			HowToClose: "Re-run `kcp discover --region us-east-1` against the source AWS account.",
		},
		{
			ID:         "auth_posture_unknown",
			ClusterID:  "b",
			Title:      "No client-authentication methods detected on the source — auth migration recommendation is unconfirmed",
			Body:       "Body shared across regions",
			HowToClose: "Re-run `kcp discover --region eu-central-1` against the source AWS account.",
		},
	}
	groups := groupOpenQuestions(oqs)
	require.Len(t, groups, 1, "OQs identical except for --region must collapse to one group")
	require.Len(t, groups[0].clusters, 2)
	require.Len(t, groups[0].regions, 2, "the two distinct regions must be tracked")
}

// Renderer swap path: when a grouped OQ spans multiple regions, the
// concrete `--region <X>` is replaced with `--region <region>` in the
// rendered HowToClose so the user sees a generic command, not the
// first-seen region.
func TestActionsNeededRender_MultiRegionGroupSwapsPlaceholder(t *testing.T) {
	cfg := defaultCfg(t)
	p := &Plan{
		OpenQuestions: []OpenQuestion{
			{
				ID:         "auth_posture_unknown",
				ClusterID:  "a",
				Title:      "No client-authentication methods detected — region a",
				Body:       "shared body",
				HowToClose: "Re-run `kcp discover --region us-east-1` against the source AWS account.",
			},
			{
				ID:         "auth_posture_unknown",
				ClusterID:  "b",
				Title:      "No client-authentication methods detected — region a",
				Body:       "shared body",
				HowToClose: "Re-run `kcp discover --region eu-central-1` against the source AWS account.",
			},
		},
	}
	out, err := RenderMarkdown(p, cfg)
	require.NoError(t, err)
	body := string(out)
	assert.Contains(t, body, "--region <region>", "multi-region grouped OQ must render a placeholder")
	assert.NotContains(t, body, "--region us-east-1", "concrete first-seen region must be swapped out")
	assert.NotContains(t, body, "--region eu-central-1")
}

// §5 Schema Migration "Scan gap" branch: when the customer declared
// `migrate_existing_schema_registry` but the scan found no SR, both
// the recommended-path line and the strategy-declared line should
// surface the contradiction so the reader doesn't see a misleading
// "Pending — declare the missing input".
func TestSchemaLabels_ScanGapWhenDeclaredButNotScanned(t *testing.T) {
	dec := &SchemaDecision{Source: SchemaSourceNone}
	pathLabel := schemaPathsLabelForContext(nil, SchemaStrategyMigrateExistingSchemaRegistry, dec.Source)
	assert.Contains(t, pathLabel, "Scan gap", "recommended-path should call out the scan gap explicitly")
	assert.Contains(t, pathLabel, "re-run", "scan-gap label must nudge the customer to re-run the scan")

	strategyLabel := schemaStrategyDeclaredLabel(SchemaStrategyMigrateExistingSchemaRegistry, dec)
	assert.Contains(t, strategyLabel, "(scan found", "declared strategy must carry a scan-contradiction caveat when it contradicts the scan")
	assert.Contains(t, strategyLabel, "no Schema Registry", "the strategy label must name the contradiction")
}

// formatUSDWithCommas handles the edge cases that broke the prior
// implementation: small-magnitude negatives losing their sign, and
// fractional carry on values just below an integer boundary.
func TestFormatUSDWithCommas(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0.00"},
		{0.5, "0.50"},
		{1.999, "2.00"},      // fractional carry
		{-0.99, "-0.99"},     // small-magnitude negative kept its sign
		{1234.5, "1,234.50"}, // thousands separator
		{1234567.89, "1,234,567.89"},
		{-1234567.89, "-1,234,567.89"},
		{1795.75, "1,795.75"}, // the canonical §Cost Reconciliation amount
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%v", c.in), func(t *testing.T) {
			assert.Equal(t, c.want, formatUSDWithCommas(c.in))
		})
	}
}
