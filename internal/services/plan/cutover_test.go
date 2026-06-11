package plan

import (
	"strings"
	"testing"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// styleInputs returns a base PlanInputsResolved with the given
// downtime_tolerance plus sensible defaults (eligible gateway, no IAM).
// Each test layers its own modifications on top.
func styleInputs(tolerance string) types.PlanInputsResolved {
	return types.PlanInputsResolved{
		DowntimeTolerance:            tolerance,
		SubPattern:                   string(types.SubPatternAppByApp),
		PreferGateway:                true,
		ConfluentForKubernetesStatus: PrereqStatusCompleteInput,
		CCGatewayLicenseStatus:       PrereqStatusCompleteInput,
		IAMPreMigrationStatus:        PrereqNotStarted, // irrelevant when no IAM in fleet
	}
}

func TestDecideCutover_StyleMapping(t *testing.T) {
	cases := []struct {
		tolerance string
		want      types.CutoverStyle
	}{
		{DowntimeZero, types.CutoverBlueGreen},
		{DowntimeSecondsPerService, types.CutoverStopRestartRepeat},
		{DowntimeMinutesPerService, types.CutoverStopRestartRepeat},
		{DowntimeScheduledWindowSequential, types.CutoverStopWaitRestart},
		{DowntimeScheduledWindowAllAtOnce, types.CutoverRestartAllAtOnce},
		{DowntimeLetConfluentChoose, types.CutoverStopRestartRepeat},
	}
	for _, tc := range cases {
		t.Run(tc.tolerance, func(t *testing.T) {
			d := decideCutover(nil, styleInputs(tc.tolerance))
			assert.Equal(t, tc.want, d.Style)
		})
	}
}

func TestDecideCutover_SubPatternOnlyForSRR(t *testing.T) {
	srr := styleInputs(DowntimeMinutesPerService)
	srr.SubPattern = string(types.SubPatternTopicByTopic)
	d := decideCutover(nil, srr)
	assert.Equal(t, types.SubPatternTopicByTopic, d.SubPattern, "SRR should carry the sub-pattern")

	bg := styleInputs(DowntimeZero)
	bg.SubPattern = string(types.SubPatternTopicByTopic)
	d = decideCutover(nil, bg)
	assert.Empty(t, d.SubPattern, "non-SRR styles must not surface a sub-pattern")
}

func TestDecideCutover_Canonical(t *testing.T) {
	d := decideCutover(nil, styleInputs(DowntimeLetConfluentChoose))
	assert.Equal(t, types.RecommendationCanonical, d.RecommendationStatus)
	assert.Equal(t, types.GatewayMediatedTrue, d.GatewayMediated)
}

func TestDecideCutover_CustomerChoiceOptOut(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	inputs.PreferGateway = false
	d := decideCutover(nil, inputs)
	assert.Equal(t, types.RecommendationCustomerChoice, d.RecommendationStatus)
	assert.Equal(t, types.GatewayMediatedFalse, d.GatewayMediated)
}

func TestDecideCutover_BlueGreenIsCustomerChoice(t *testing.T) {
	d := decideCutover(nil, styleInputs(DowntimeZero))
	assert.Equal(t, types.CutoverBlueGreen, d.Style)
	assert.Equal(t, types.GatewayMediatedNotApplicable, d.GatewayMediated)
	assert.Equal(t, types.RecommendationCustomerChoice, d.RecommendationStatus)
}

// Ambiguous = prefer_gateway default true + all three prereqs at
// not_started. Customer hasn't engaged with the gateway question.
func TestDecideCutover_DegradedAwaitingOQ(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	inputs.ConfluentForKubernetesStatus = PrereqNotStarted
	inputs.CCGatewayLicenseStatus = PrereqNotStarted
	inputs.IAMPreMigrationStatus = PrereqNotStarted
	d := decideCutover(nil, inputs)
	assert.Equal(t, types.RecommendationDegradedAwaitingOQ, d.RecommendationStatus)
	assert.Equal(t, types.GatewayMediatedFalse, d.GatewayMediated)
}

// Pending = prefer_gateway true + at least one prereq advanced but
// at least one still at not_started.
func TestDecideCutover_DegradedPrereqsPending(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	inputs.ConfluentForKubernetesStatus = PrereqStatusInProgressInput
	inputs.CCGatewayLicenseStatus = PrereqNotStarted
	d := decideCutover(nil, inputs)
	assert.Equal(t, types.RecommendationDegradedPrereqsPending, d.RecommendationStatus)
	assert.Equal(t, types.GatewayMediatedFalse, d.GatewayMediated)
}

// IAM in the fleet does NOT block gateway eligibility. CC doesn't
// support IAM at all, so IAM clients have to migrate regardless of
// cutover path — gating the gateway on it would punish the customer
// for engaging with the gateway when the underlying work is the same
// either way.
func TestDecideCutover_IAMDoesNotGateGatewayEligibility(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	// IAM prereq at not_started; the two infra prereqs complete.
	inputs.IAMPreMigrationStatus = PrereqNotStarted

	// Without IAM in the fleet: canonical (as before).
	d := decideCutover([]types.ProcessedCluster{withSourceAuth("nofleetiam", SourceAuthSCRAM)}, inputs)
	assert.Equal(t, types.RecommendationCanonical, d.RecommendationStatus, "no IAM in fleet → canonical")

	// With IAM in the fleet: still canonical — IAM is not a gateway prereq.
	d = decideCutover([]types.ProcessedCluster{withSourceAuth("fleetiam", SourceAuthIAM)}, inputs)
	assert.Equal(t, types.RecommendationCanonical, d.RecommendationStatus, "IAM in fleet must not gate the gateway path")
}

func TestDecideCutover_AlternativesShown(t *testing.T) {
	d := decideCutover(nil, styleInputs(DowntimeLetConfluentChoose))
	assert.Equal(t, types.CutoverStopRestartRepeat, d.Style)
	assert.Len(t, d.AlternativesShown, 3, "alternatives = all styles except the recommended one")
	assert.NotContains(t, d.AlternativesShown, types.CutoverStopRestartRepeat)
	assert.Contains(t, d.AlternativesShown, types.CutoverBlueGreen)
}

// IAM never appears as a gateway prereq row. The IAM workstream lives
// in §Auth + the IAM-client effort signal; including it in the
// gateway prereq table would frame it as gateway-specific when it
// applies to every cutover path.
func TestDecideCutover_IAMNeverAppearsInGatewayPrereqs(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)

	for _, auth := range []string{SourceAuthSCRAM, SourceAuthIAM} {
		d := decideCutover([]types.ProcessedCluster{withSourceAuth("c", auth)}, inputs)
		for _, p := range d.Prereqs {
			assert.False(t, strings.Contains(p.Description, "IAM"), "IAM must never appear as a gateway prereq (auth=%s)", auth)
		}
	}
}

// withSourceAuth returns a minimal ProcessedCluster with the named
// source auth enabled on the AWS-side ClientAuthentication. Used to
// drive fleetUsesIAM() through decideCutover.
func withSourceAuth(name, auth string) types.ProcessedCluster {
	c := types.ProcessedCluster{Name: name}
	c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeProvisioned
	enabled := true
	clientAuth := &kafkatypes.ClientAuthentication{}
	switch auth {
	case SourceAuthIAM:
		clientAuth.Sasl = &kafkatypes.Sasl{Iam: &kafkatypes.Iam{Enabled: &enabled}}
	case SourceAuthSCRAM:
		clientAuth.Sasl = &kafkatypes.Sasl{Scram: &kafkatypes.Scram{Enabled: &enabled}}
	case SourceAuthMTLS:
		clientAuth.Tls = &kafkatypes.Tls{Enabled: &enabled}
	case SourceAuthUnauth:
		clientAuth.Unauthenticated = &kafkatypes.Unauthenticated{Enabled: &enabled}
	}
	c.AWSClientInformation.MskClusterConfig.Provisioned = &kafkatypes.Provisioned{ClientAuthentication: clientAuth}
	return c
}

// ----- detectCutoverOpenQuestions -----

// hasOQ returns whether any OQ matches the given ID.
func hasOQ(oqs []types.OpenQuestion, id string) bool {
	for _, oq := range oqs {
		if oq.ID == id {
			return true
		}
	}
	return false
}

func TestDetectCutoverOpenQuestions_AmbiguousIntent(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	inputs.ConfluentForKubernetesStatus = PrereqNotStarted
	inputs.CCGatewayLicenseStatus = PrereqNotStarted
	inputs.IAMPreMigrationStatus = PrereqNotStarted
	d := decideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, nil, inputs, false)
	assert.True(t, hasOQ(oqs, "gateway_intent_unconfirmed"))
	assert.False(t, hasOQ(oqs, "gateway_prereqs_pending"))
}

func TestDetectCutoverOpenQuestions_PrereqsPending(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	inputs.ConfluentForKubernetesStatus = PrereqStatusInProgressInput
	inputs.CCGatewayLicenseStatus = PrereqNotStarted
	d := decideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, nil, inputs, false)
	assert.True(t, hasOQ(oqs, "gateway_prereqs_pending"))
	assert.False(t, hasOQ(oqs, "gateway_intent_unconfirmed"))
}

// seconds_per_service + prefer_gateway:false → cross-check OQ fires
// with the "prefer_gateway: false" message.
func TestDetectCutoverOpenQuestions_SecondsPerServiceOptOut(t *testing.T) {
	inputs := styleInputs(DowntimeSecondsPerService)
	inputs.PreferGateway = false
	d := decideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, nil, inputs, false)
	assert.True(t, hasOQ(oqs, "downtime_tolerance_requires_gateway"))
}

// seconds_per_service + ambiguous intent → cross-check OQ fires AND
// gateway_intent_unconfirmed also fires (separate concerns).
func TestDetectCutoverOpenQuestions_SecondsPerServiceAmbiguous(t *testing.T) {
	inputs := styleInputs(DowntimeSecondsPerService)
	inputs.ConfluentForKubernetesStatus = PrereqNotStarted
	inputs.CCGatewayLicenseStatus = PrereqNotStarted
	inputs.IAMPreMigrationStatus = PrereqNotStarted
	d := decideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, nil, inputs, false)
	assert.True(t, hasOQ(oqs, "downtime_tolerance_requires_gateway"))
	assert.True(t, hasOQ(oqs, "gateway_intent_unconfirmed"))
}

// Blue/Green sidesteps the gateway question entirely; seconds_per_service
// can't combine with Blue/Green (different style), so the cross-check
// OQ should NOT fire on a Blue/Green resolved style.
func TestDetectCutoverOpenQuestions_BlueGreenSkipsCrossCheck(t *testing.T) {
	inputs := styleInputs(DowntimeZero)
	d := decideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, nil, inputs, false)
	assert.False(t, hasOQ(oqs, "downtime_tolerance_requires_gateway"))
}

func TestDetectCutoverOpenQuestions_UnknownTolerance(t *testing.T) {
	inputs := styleInputs("scheduled_window_quarterly") // typo
	d := decideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, nil, inputs, false)
	assert.True(t, hasOQ(oqs, "downtime_tolerance_unknown"))
}

func TestDetectCutoverOpenQuestions_CanonicalEmits_NoOQs(t *testing.T) {
	d := decideCutover(nil, styleInputs(DowntimeLetConfluentChoose))
	oqs := detectCutoverOpenQuestions(d, nil, styleInputs(DowntimeLetConfluentChoose), false)
	assert.Empty(t, oqs, "canonical recommendation has nothing to ask")
}

// Customer opted out + gateway prereqs absent: prereq table is empty
// (S5 fix). When opted in (default), prereqs surface.
func TestDecideCutover_PrereqsSuppressedOnOptOut(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	inputs.PreferGateway = false
	d := decideCutover(nil, inputs)
	assert.Empty(t, d.Prereqs, "opt-out hides the gateway prereq table — they don't apply")

	inputs.PreferGateway = true
	d = decideCutover(nil, inputs)
	assert.NotEmpty(t, d.Prereqs, "opt-in keeps the prereq table visible")
}

// Per-cluster `downtime_tolerance` override emits a CutoverOverrides
// entry whose style differs from the fleet default; clusters that
// match the fleet (or have no override) don't appear.
func TestComputeCutoverOverrides_PerClusterDifference(t *testing.T) {
	fleet := decideCutover(nil, styleInputs(DowntimeMinutesPerService))
	inputs := styleInputs(DowntimeMinutesPerService)
	zero := DowntimeZero
	sched := DowntimeScheduledWindowAllAtOnce
	inputs.Raw = &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			"a": {DowntimeTolerance: &zero},  // override → Blue/Green
			"b": {DowntimeTolerance: &sched}, // override → Restart-All-At-Once
			"c": {},                          // no cutover override → no entry
		},
	}
	clusters := []types.ProcessedCluster{
		{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, // d has no entry in Clusters map
	}
	out := computeCutoverOverrides(clusters, fleet, inputs)
	require := []struct {
		id    string
		style types.CutoverStyle
	}{
		{"a", types.CutoverBlueGreen},
		{"b", types.CutoverRestartAllAtOnce},
	}
	assert.Len(t, out, len(require), "only clusters whose resolved style differs surface")
	for i, want := range require {
		assert.Equal(t, want.id, out[i].ClusterID)
		assert.Equal(t, want.style, out[i].Style)
	}
}

// Typo'd per-cluster downtime_tolerance fires a per-cluster OQ — the
// override silently falls back to the fleet default in
// applyClusterOverride, so without this detector the reader can't
// tell why their override didn't take effect.
func TestDetectClusterCutoverOpenQuestions_TypoPerCluster(t *testing.T) {
	typo := "zerooo"
	inputs := types.PlanInputsResolved{
		Raw: &types.PlanInputs{
			Clusters: map[string]types.ClusterPlanInputs{
				"a": {DowntimeTolerance: &typo},
			},
		},
	}
	oqs := detectClusterCutoverOpenQuestions([]types.ProcessedCluster{{Name: "a"}}, inputs)
	require := 1
	assert.Len(t, oqs, require)
	assert.Equal(t, "downtime_tolerance_unknown", oqs[0].ID)
	assert.Equal(t, "a", oqs[0].ClusterID)
	assert.Contains(t, oqs[0].Title, "zerooo")
}

// One cluster can carry BOTH a per-cluster target_auth_method AND a
// per-cluster downtime_tolerance override. Both must propagate to the
// per-cluster resolved inputs and surface in the right Plan sections
// (auth row + cutover override entry). Without this guard a future
// refactor of applyClusterOverride could silently drop one of them.
func TestPerCluster_AuthAndCutoverOverridesCoexistOnSameCluster(t *testing.T) {
	zero := DowntimeZero
	oauth := "oauth"
	raw := &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			"alpha": {
				DowntimeTolerance: &zero,
				TargetAuthMethod:  &oauth,
			},
		},
	}
	base := styleInputs(DowntimeMinutesPerService)
	base.Raw = raw

	resolved := applyClusterOverride(base, raw, "alpha")
	assert.Equal(t, DowntimeZero, resolved.DowntimeTolerance, "per-cluster downtime_tolerance must layer on top of fleet inputs")
	assert.Equal(t, "oauth", resolved.TargetAuthMethod, "per-cluster target_auth_method must layer on top of fleet inputs")

	fleet := decideCutover([]types.ProcessedCluster{withSourceAuth("alpha", SourceAuthSCRAM)}, base)
	overrides := computeCutoverOverrides([]types.ProcessedCluster{withSourceAuth("alpha", SourceAuthSCRAM)}, fleet, base)
	require.Len(t, overrides, 1, "per-cluster downtime_tolerance must produce a cutover override entry")
	assert.Equal(t, types.CutoverBlueGreen, overrides[0].Style)

	auth := decideAuth(withSourceAuth("alpha", SourceAuthSCRAM), defaultCfg(t), resolved)
	row := requireRow(t, auth, SourceAuthSCRAM)
	assert.Equal(t, "oauth", row.EffectiveTarget, "per-cluster target_auth_method must beat the fleet default on the auth row")
}

// Per-cluster `sub_pattern` set WITHOUT `downtime_tolerance` should
// flip only the sub-pattern (the cluster inherits the fleet's
// downtime_tolerance → style). Without this guard a refactor of
// computeCutoverOverrides could miss the sub_pattern-only path
// because `raw.DowntimeTolerance == nil` looks like "no override".
func TestPerCluster_SubPatternOnlyOverride(t *testing.T) {
	tbt := string(types.SubPatternTopicByTopic)
	raw := &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			"alpha": {SubPattern: &tbt},
		},
	}
	base := styleInputs(DowntimeMinutesPerService)
	base.SubPattern = string(types.SubPatternAppByApp) // fleet default
	base.Raw = raw

	fleet := decideCutover(nil, base)
	assert.Equal(t, types.CutoverStopRestartRepeat, fleet.Style, "fleet must still resolve to SRR")
	assert.Equal(t, types.SubPatternAppByApp, fleet.SubPattern, "fleet sub-pattern unchanged")

	overrides := computeCutoverOverrides([]types.ProcessedCluster{{Name: "alpha"}}, fleet, base)
	require.Len(t, overrides, 1, "sub_pattern-only override must still produce a CutoverOverrides entry")
	assert.Equal(t, "alpha", overrides[0].ClusterID)
	assert.Equal(t, types.CutoverStopRestartRepeat, overrides[0].Style, "style inherits the fleet's")
	assert.Equal(t, types.SubPatternTopicByTopic, overrides[0].SubPattern, "sub-pattern reflects the override")
}

// Fleet has all 3 gateway prereqs complete (so canonical recommendation
// is gateway-mediated) + IAM on the source + a per-cluster Blue/Green
// override on the IAM cluster. Expectations:
//   - Fleet cutover is canonical, gateway-mediated.
//   - Override cluster surfaces as Blue/Green with gateway N/A.
//   - No fleet-level gateway OQ fires (recommendation_status is canonical),
//     so no exemption suffix is needed. Specifically `gateway_intent_unconfirmed`
//     and `gateway_prereqs_pending` MUST stay silent.
func TestPerCluster_BlueGreenOverrideOnIAMClusterWithCompletePrereqs(t *testing.T) {
	zero := DowntimeZero
	raw := &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			"iam-cluster": {DowntimeTolerance: &zero},
		},
	}
	base := styleInputs(DowntimeMinutesPerService)
	// IAM source means the IAM prereq is consulted — set it complete
	// so the fleet decision is canonical, not degraded.
	base.IAMPreMigrationStatus = PrereqStatusCompleteInput
	base.Raw = raw
	clusters := []types.ProcessedCluster{withSourceAuth("iam-cluster", SourceAuthIAM)}

	fleet := decideCutover(clusters, base)
	assert.Equal(t, types.RecommendationCanonical, fleet.RecommendationStatus, "all prereqs complete + IAM in fleet must still resolve canonical")
	assert.Equal(t, types.GatewayMediatedTrue, fleet.GatewayMediated)

	overrides := computeCutoverOverrides(clusters, fleet, base)
	require.Len(t, overrides, 1)
	assert.Equal(t, types.CutoverBlueGreen, overrides[0].Style)
	assert.Equal(t, types.GatewayMediatedNotApplicable, overrides[0].GatewayMediated, "BG override on IAM cluster still sidesteps the gateway")

	oqs := detectCutoverOpenQuestions(fleet, overrides, base, fleetUsesIAM(clusters))
	for _, oq := range oqs {
		assert.NotEqual(t, "gateway_intent_unconfirmed", oq.ID, "canonical fleet must not fire gateway-intent OQ")
		assert.NotEqual(t, "gateway_prereqs_pending", oq.ID, "complete prereqs must not fire prereqs-pending OQ")
	}
}

// Per-cluster cutover overrides inherit the fleet's GatewayMediated
// for non-Blue/Green styles — gateway prereqs are fleet-scoped, so
// computing them against a single-cluster slice via decideCutover
// could in principle drift from the fleet decision. Regression guard:
// a non-BG override must surface the fleet's GatewayMediated verbatim.
func TestComputeCutoverOverrides_GatewayMediationInheritedFromFleet(t *testing.T) {
	base := styleInputs(DowntimeMinutesPerService)
	base.PreferGateway = true
	// One infra prereq complete, the other still not_started → fleet
	// lands on degraded_prereqs_pending (not gateway-mediated).
	base.ConfluentForKubernetesStatus = PrereqStatusCompleteInput
	base.CCGatewayLicenseStatus = PrereqNotStarted
	clusters := []types.ProcessedCluster{
		withSourceAuth("c1", SourceAuthSCRAM),
		withSourceAuth("c2", SourceAuthSCRAM),
	}
	srr := DowntimeMinutesPerService
	tbt := string(types.SubPatternTopicByTopic)
	raw := &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			// c2 flips sub-pattern only — same style as fleet.
			"c2": {DowntimeTolerance: &srr, SubPattern: &tbt},
		},
	}
	base.Raw = raw
	fleet := decideCutover(clusters, base)
	require.NotEqual(t, types.GatewayMediatedTrue, fleet.GatewayMediated, "fleet must be degraded by the pending infra prereq")

	overrides := computeCutoverOverrides(clusters, fleet, base)
	require.Len(t, overrides, 1, "c2 must surface (sub-pattern differs)")
	assert.Equal(t, "c2", overrides[0].ClusterID)
	assert.Equal(t, fleet.GatewayMediated, overrides[0].GatewayMediated,
		"non-BG override must inherit fleet's GatewayMediated, not re-evaluate against the single cluster")
}

// Per-cluster `downtime_tolerance: seconds_per_service` against a
// fleet without gateway mediation must surface the conflict — gateway
// prereqs are fleet-scoped so a per-cluster override can't earn its
// own gateway path. Without an explicit OQ the customer's choice is
// silently lost.
func TestPerCluster_SecondsPerServiceWithoutFleetGateway(t *testing.T) {
	sps := DowntimeSecondsPerService
	raw := &types.PlanInputs{
		Clusters: map[string]types.ClusterPlanInputs{
			"alpha": {DowntimeTolerance: &sps},
		},
	}
	// Fleet doesn't have any gateway prereqs advanced → plain CL.
	base := types.PlanInputsResolved{
		DowntimeTolerance:            DowntimeMinutesPerService,
		SubPattern:                   string(types.SubPatternAppByApp),
		PreferGateway:                true,
		ConfluentForKubernetesStatus: PrereqNotStarted,
		CCGatewayLicenseStatus:       PrereqNotStarted,
		IAMPreMigrationStatus:        PrereqNotStarted,
		Raw:                          raw,
	}
	clusters := []types.ProcessedCluster{withSourceAuth("alpha", SourceAuthSCRAM)}

	fleet := decideCutover(clusters, base)
	require.NotEqual(t, types.GatewayMediatedTrue, fleet.GatewayMediated, "fleet must NOT be gateway-mediated for this scenario")

	oqs := detectPerClusterGatewayIncompat(clusters, fleet, base)
	require.Len(t, oqs, 1, "per-cluster seconds_per_service must fire the gateway-incompat OQ when fleet isn't mediated")
	assert.Equal(t, "downtime_tolerance_requires_gateway", oqs[0].ID)
	assert.Equal(t, "alpha", oqs[0].ClusterID)
	assert.Contains(t, oqs[0].Title, "seconds_per_service")

	// Inverse: when fleet IS gateway-mediated, no per-cluster OQ.
	base.ConfluentForKubernetesStatus = PrereqStatusCompleteInput
	base.CCGatewayLicenseStatus = PrereqStatusCompleteInput
	mediatedFleet := decideCutover(clusters, base)
	require.Equal(t, types.GatewayMediatedTrue, mediatedFleet.GatewayMediated)
	assert.Empty(t, detectPerClusterGatewayIncompat(clusters, mediatedFleet, base), "mediated fleet → no per-cluster OQ")
}
