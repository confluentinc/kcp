package plan

import (
	"strings"
	"testing"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
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
			d := DecideCutover(nil, styleInputs(tc.tolerance))
			assert.Equal(t, tc.want, d.Style)
		})
	}
}

func TestDecideCutover_SubPatternOnlyForSRR(t *testing.T) {
	srr := styleInputs(DowntimeMinutesPerService)
	srr.SubPattern = string(types.SubPatternTopicByTopic)
	d := DecideCutover(nil, srr)
	assert.Equal(t, types.SubPatternTopicByTopic, d.SubPattern, "SRR should carry the sub-pattern")

	bg := styleInputs(DowntimeZero)
	bg.SubPattern = string(types.SubPatternTopicByTopic)
	d = DecideCutover(nil, bg)
	assert.Empty(t, d.SubPattern, "non-SRR styles must not surface a sub-pattern")
}

func TestDecideCutover_Canonical(t *testing.T) {
	d := DecideCutover(nil, styleInputs(DowntimeLetConfluentChoose))
	assert.Equal(t, types.RecommendationCanonical, d.RecommendationStatus)
	assert.Equal(t, types.GatewayMediatedTrue, d.GatewayMediated)
}

func TestDecideCutover_CustomerChoiceOptOut(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	inputs.PreferGateway = false
	d := DecideCutover(nil, inputs)
	assert.Equal(t, types.RecommendationCustomerChoice, d.RecommendationStatus)
	assert.Equal(t, types.GatewayMediatedFalse, d.GatewayMediated)
}

func TestDecideCutover_BlueGreenIsCustomerChoice(t *testing.T) {
	d := DecideCutover(nil, styleInputs(DowntimeZero))
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
	d := DecideCutover(nil, inputs)
	assert.Equal(t, types.RecommendationDegradedAwaitingOQ, d.RecommendationStatus)
	assert.Equal(t, types.GatewayMediatedFalse, d.GatewayMediated)
}

// Pending = prefer_gateway true + at least one prereq advanced but
// at least one still at not_started.
func TestDecideCutover_DegradedPrereqsPending(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	inputs.ConfluentForKubernetesStatus = PrereqStatusInProgressInput
	inputs.CCGatewayLicenseStatus = PrereqNotStarted
	d := DecideCutover(nil, inputs)
	assert.Equal(t, types.RecommendationDegradedPrereqsPending, d.RecommendationStatus)
	assert.Equal(t, types.GatewayMediatedFalse, d.GatewayMediated)
}

// IAM prereq is only consulted when the fleet actually has IAM enabled.
func TestDecideCutover_IAMPrereqOnlyMattersWhenIAMInFleet(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	// IAM prereq at not_started; the other two complete.
	inputs.IAMPreMigrationStatus = PrereqNotStarted

	// Without IAM in the fleet, still eligible / canonical.
	d := DecideCutover([]types.ProcessedCluster{withSourceAuth("nofleetiam", SourceAuthSCRAM)}, inputs)
	assert.Equal(t, types.RecommendationCanonical, d.RecommendationStatus, "no IAM in fleet → IAM prereq irrelevant")

	// With IAM in the fleet, the IAM-not-started prereq now blocks eligibility.
	d = DecideCutover([]types.ProcessedCluster{withSourceAuth("fleetiam", SourceAuthIAM)}, inputs)
	assert.Equal(t, types.RecommendationDegradedPrereqsPending, d.RecommendationStatus, "IAM in fleet → IAM prereq required")
}

func TestDecideCutover_AlternativesShown(t *testing.T) {
	d := DecideCutover(nil, styleInputs(DowntimeLetConfluentChoose))
	assert.Equal(t, types.CutoverStopRestartRepeat, d.Style)
	assert.Len(t, d.AlternativesShown, 3, "alternatives = all styles except the recommended one")
	assert.NotContains(t, d.AlternativesShown, types.CutoverStopRestartRepeat)
	assert.Contains(t, d.AlternativesShown, types.CutoverBlueGreen)
}

// IAM prereq row only appears in the rendered prereq table when IAM is
// in the fleet — otherwise it's irrelevant noise.
func TestDecideCutover_IAMPrereqRowOnlyWhenIAMInFleet(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)

	noIAM := DecideCutover([]types.ProcessedCluster{withSourceAuth("c", SourceAuthSCRAM)}, inputs)
	for _, p := range noIAM.Prereqs {
		assert.False(t, strings.Contains(p.Description, "IAM"), "IAM prereq must NOT appear when fleet has no IAM")
	}

	iam := DecideCutover([]types.ProcessedCluster{withSourceAuth("c", SourceAuthIAM)}, inputs)
	var sawIAM bool
	for _, p := range iam.Prereqs {
		if strings.Contains(p.Description, "IAM") {
			sawIAM = true
		}
	}
	assert.True(t, sawIAM, "IAM prereq row must appear when fleet has IAM")
}

// withSourceAuth returns a minimal ProcessedCluster with the named
// source auth enabled on the AWS-side ClientAuthentication. Used to
// drive fleetUsesIAM() through DecideCutover.
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
	d := DecideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, inputs, false)
	assert.True(t, hasOQ(oqs, "gateway_intent_unconfirmed"))
	assert.False(t, hasOQ(oqs, "gateway_prereqs_pending"))
}

func TestDetectCutoverOpenQuestions_PrereqsPending(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	inputs.ConfluentForKubernetesStatus = PrereqStatusInProgressInput
	inputs.CCGatewayLicenseStatus = PrereqNotStarted
	d := DecideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, inputs, false)
	assert.True(t, hasOQ(oqs, "gateway_prereqs_pending"))
	assert.False(t, hasOQ(oqs, "gateway_intent_unconfirmed"))
}

// seconds_per_service + prefer_gateway:false → cross-check OQ fires
// with the "prefer_gateway: false" message.
func TestDetectCutoverOpenQuestions_SecondsPerServiceOptOut(t *testing.T) {
	inputs := styleInputs(DowntimeSecondsPerService)
	inputs.PreferGateway = false
	d := DecideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, inputs, false)
	assert.True(t, hasOQ(oqs, "downtime_tolerance_requires_gateway"))
}

// seconds_per_service + ambiguous intent → cross-check OQ fires AND
// gateway_intent_unconfirmed also fires (separate concerns).
func TestDetectCutoverOpenQuestions_SecondsPerServiceAmbiguous(t *testing.T) {
	inputs := styleInputs(DowntimeSecondsPerService)
	inputs.ConfluentForKubernetesStatus = PrereqNotStarted
	inputs.CCGatewayLicenseStatus = PrereqNotStarted
	inputs.IAMPreMigrationStatus = PrereqNotStarted
	d := DecideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, inputs, false)
	assert.True(t, hasOQ(oqs, "downtime_tolerance_requires_gateway"))
	assert.True(t, hasOQ(oqs, "gateway_intent_unconfirmed"))
}

// Blue/Green sidesteps the gateway question entirely; seconds_per_service
// can't combine with Blue/Green (different style), so the cross-check
// OQ should NOT fire on a Blue/Green resolved style.
func TestDetectCutoverOpenQuestions_BlueGreenSkipsCrossCheck(t *testing.T) {
	inputs := styleInputs(DowntimeZero)
	d := DecideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, inputs, false)
	assert.False(t, hasOQ(oqs, "downtime_tolerance_requires_gateway"))
}

func TestDetectCutoverOpenQuestions_UnknownTolerance(t *testing.T) {
	inputs := styleInputs("scheduled_window_quarterly") // typo
	d := DecideCutover(nil, inputs)
	oqs := detectCutoverOpenQuestions(d, inputs, false)
	assert.True(t, hasOQ(oqs, "downtime_tolerance_unknown"))
}

func TestDetectCutoverOpenQuestions_CanonicalEmits_NoOQs(t *testing.T) {
	d := DecideCutover(nil, styleInputs(DowntimeLetConfluentChoose))
	oqs := detectCutoverOpenQuestions(d, styleInputs(DowntimeLetConfluentChoose), false)
	assert.Empty(t, oqs, "canonical recommendation has nothing to ask")
}

// Customer opted out + gateway prereqs absent: prereq table is empty
// (S5 fix). When opted in (default), prereqs surface.
func TestDecideCutover_PrereqsSuppressedOnOptOut(t *testing.T) {
	inputs := styleInputs(DowntimeMinutesPerService)
	inputs.PreferGateway = false
	d := DecideCutover(nil, inputs)
	assert.Empty(t, d.Prereqs, "opt-out hides the gateway prereq table — they don't apply")

	inputs.PreferGateway = true
	d = DecideCutover(nil, inputs)
	assert.NotEmpty(t, d.Prereqs, "opt-in keeps the prereq table visible")
}
