package plan

import (
	"github.com/confluentinc/kcp/internal/types"
)

// downtime_tolerance enum values. Stable identifiers — used as YAML
// input tokens AND as keys in the style mapping.
const (
	DowntimeZero                      = "zero"
	DowntimeSecondsPerService         = "seconds_per_service"
	DowntimeMinutesPerService         = "minutes_per_service"
	DowntimeScheduledWindowSequential = "scheduled_window_sequential"
	DowntimeScheduledWindowAllAtOnce  = "scheduled_window_all_at_once"
	DowntimeLetConfluentChoose        = "let_confluent_choose"
	DowntimeUnsetFallback             = DowntimeLetConfluentChoose
)

// Gateway prereq status enum values.
const (
	PrereqNotStarted            = "not_started"
	PrereqStatusInProgressInput = "in_progress"
	PrereqStatusCompleteInput   = "complete"
)

// decideCutover produces the fleet-wide cutover decision.
// Reads:
//   - inputs.DowntimeTolerance → CutoverStyle (1:1 mapping)
//   - gateway eligibility (3 prereqs + fleet-wide IAM cross-reference)
//   - inputs.PreferGateway opt-out
//
// Emits CutoverDecision with all four recommendation_status branches
// modelled. Open Questions are emitted separately by the caller; this
// function only produces the decision, not the OQs.
//
// **Input contract:** `inputs` MUST come from `ResolvePlanInputs` (or
// `applyClusterOverride` on top of it). The function reads
// `PreferGateway` as a bool, so a Go zero-value `PlanInputsResolved{}`
// would silently land on the customer-choice / plain-CL branch —
// distinct from "the customer set `prefer_gateway: false`" but
// indistinguishable at this layer. Callers from tests should construct
// inputs via the resolver, not by struct literal.
func decideCutover(clusters []types.ProcessedCluster, inputs types.PlanInputsResolved) types.CutoverDecision {
	style, sub := resolveStyle(inputs)

	// Blue/Green sidesteps the gateway question entirely — the gateway
	// doesn't sit on a parallel-run cutover step.
	if style == types.CutoverBlueGreen {
		return types.CutoverDecision{
			Style:                style,
			SubPattern:           sub,
			GatewayMediated:      types.GatewayMediatedNotApplicable,
			RecommendationStatus: types.RecommendationCustomerChoice,
			AlternativesShown:    alternativesShown(style),
			Prereqs:              prereqsForStyle(style, inputs, fleetUsesIAM(clusters)),
		}
	}

	iamInUse := fleetUsesIAM(clusters)
	eligible := gatewayEligible(inputs, iamInUse)
	ambiguous := ambiguousGatewayIntent(inputs, iamInUse)

	var mediated types.GatewayMediated
	var status types.RecommendationStatus
	switch {
	case !inputs.PreferGateway:
		mediated = types.GatewayMediatedFalse
		status = types.RecommendationCustomerChoice
	case eligible:
		mediated = types.GatewayMediatedTrue
		status = types.RecommendationCanonical
	case ambiguous:
		mediated = types.GatewayMediatedFalse
		status = types.RecommendationDegradedAwaitingOQ
	default:
		// `prefer_gateway: true` but at least one prereq is still
		// `not_started`. Customer has engaged but not finished.
		mediated = types.GatewayMediatedFalse
		status = types.RecommendationDegradedPrereqsPending
	}

	// Suppress the gateway prereq table when the customer opted out of
	// the gateway — those prereqs aren't needed for plain Cluster
	// Linking, so showing "not started" against them is misleading.
	var prereqs []types.Prereq
	if mediated != types.GatewayMediatedFalse || status != types.RecommendationCustomerChoice {
		prereqs = prereqsForStyle(style, inputs, iamInUse)
	}
	return types.CutoverDecision{
		Style:                style,
		SubPattern:           sub,
		GatewayMediated:      mediated,
		RecommendationStatus: status,
		AlternativesShown:    alternativesShown(style),
		Prereqs:              prereqs,
	}
}

// resolveStyle maps downtime_tolerance to a CutoverStyle plus optional
// sub-pattern. sub-pattern is only populated when style is
// Stop-Restart-Repeat; for everything else it's empty.
func resolveStyle(inputs types.PlanInputsResolved) (types.CutoverStyle, types.CutoverSubPattern) {
	tolerance := inputs.DowntimeTolerance
	if tolerance == "" {
		tolerance = DowntimeUnsetFallback
	}
	var style types.CutoverStyle
	switch tolerance {
	case DowntimeZero:
		style = types.CutoverBlueGreen
	case DowntimeSecondsPerService, DowntimeMinutesPerService, DowntimeLetConfluentChoose:
		style = types.CutoverStopRestartRepeat
	case DowntimeScheduledWindowSequential:
		style = types.CutoverStopWaitRestart
	case DowntimeScheduledWindowAllAtOnce:
		style = types.CutoverRestartAllAtOnce
	default:
		// Unknown value: degrade to the Confluent default rather than
		// erroring. `detectCutoverOpenQuestions` surfaces an OQ
		// (`downtime_tolerance_unknown`) so the customer sees the
		// typo / unrecognised value rather than silently inheriting
		// the default.
		style = types.CutoverStopRestartRepeat
	}

	var sub types.CutoverSubPattern
	if style == types.CutoverStopRestartRepeat {
		switch inputs.SubPattern {
		case string(types.SubPatternTopicByTopic):
			sub = types.SubPatternTopicByTopic
		default:
			sub = types.SubPatternAppByApp
		}
	}
	return style, sub
}

// knownDowntimeTolerance reports whether `tolerance` is one of the
// recognised enum values. Empty string counts as known (resolves to
// the default). Used by the OQ detector to surface typos rather than
// silently mapping unknown values to Stop-Restart-Repeat.
func knownDowntimeTolerance(tolerance string) bool {
	return knownEnum(tolerance,
		DowntimeZero,
		DowntimeSecondsPerService,
		DowntimeMinutesPerService,
		DowntimeScheduledWindowSequential,
		DowntimeScheduledWindowAllAtOnce,
		DowntimeLetConfluentChoose,
	)
}

// knownCutoverSubPattern reports whether `sub` is one of the
// Stop-Restart-Repeat sub-patterns. Empty counts as known (falls back
// to app-by-app in resolveStyle).
func knownCutoverSubPattern(sub string) bool {
	return knownEnum(sub,
		string(types.SubPatternAppByApp),
		string(types.SubPatternTopicByTopic),
	)
}

// gatewayEligible reports whether all gateway prereqs are at
// `in_progress` or `complete`. IAM prereq only counts when the fleet
// actually has IAM enabled (otherwise the IAM-pre-migration constraint
// is vacuous — there's nothing to pre-migrate).
func gatewayEligible(inputs types.PlanInputsResolved, iamInUse bool) bool {
	if !prereqAdvanced(inputs.ConfluentForKubernetesStatus) {
		return false
	}
	if !prereqAdvanced(inputs.CCGatewayLicenseStatus) {
		return false
	}
	if iamInUse && !prereqAdvanced(inputs.IAMPreMigrationStatus) {
		return false
	}
	return true
}

// prereqAdvanced returns true for `in_progress` or `complete`.
// `not_started` (or anything unrecognised) returns false.
func prereqAdvanced(status string) bool {
	return status == PrereqStatusInProgressInput || status == PrereqStatusCompleteInput
}

// ambiguousGatewayIntent reports whether the customer hasn't expressed
// any preference about the gateway — `prefer_gateway: true` (default)
// AND every *applicable* prereq is at `not_started`. The IAM prereq
// only counts when the fleet actually uses IAM, mirroring
// gatewayEligible — otherwise a non-IAM fleet that accidentally sets
// `iam_pre_migration_status: in_progress` would flip the status from
// `degraded_awaiting_oq` to `degraded_prereqs_pending` even though the
// IAM prereq doesn't apply to this fleet.
func ambiguousGatewayIntent(inputs types.PlanInputsResolved, iamInUse bool) bool {
	if !inputs.PreferGateway {
		return false
	}
	if inputs.ConfluentForKubernetesStatus != PrereqNotStarted {
		return false
	}
	if inputs.CCGatewayLicenseStatus != PrereqNotStarted {
		return false
	}
	if iamInUse && inputs.IAMPreMigrationStatus != PrereqNotStarted {
		return false
	}
	return true
}

// alternativesShown returns the cutover styles that the renderer
// should explain for trust ("we considered these and didn't pick
// them") — every style EXCEPT the recommended one.
func alternativesShown(recommended types.CutoverStyle) []types.CutoverStyle {
	all := []types.CutoverStyle{
		types.CutoverStopRestartRepeat,
		types.CutoverStopWaitRestart,
		types.CutoverRestartAllAtOnce,
		types.CutoverBlueGreen,
	}
	var out []types.CutoverStyle
	for _, s := range all {
		if s != recommended {
			out = append(out, s)
		}
	}
	return out
}

// prereqsForStyle renders the Prerequisites table rows. The Cluster
// Linking floor (`source_min_kafka_version: 2.4.0`) and Express tier
// compatibility are state-derived prereqs surfaced by the renderer
// based on plan-config; this function only emits the customer-driven
// gateway prereqs. Blue/Green has no kcp-emitted prereqs.
func prereqsForStyle(style types.CutoverStyle, inputs types.PlanInputsResolved, iamInUse bool) []types.Prereq {
	if style == types.CutoverBlueGreen {
		return nil
	}
	out := []types.Prereq{
		{Description: "Confluent for Kubernetes (CFK) cluster", Status: prereqStatusFromInput(inputs.ConfluentForKubernetesStatus)},
		{Description: "Confluent Cloud Gateway Add-On license", Status: prereqStatusFromInput(inputs.CCGatewayLicenseStatus)},
	}
	if iamInUse {
		out = append(out, types.Prereq{
			Description: "IAM clients pre-migrated to SCRAM / mTLS",
			Status:      prereqStatusFromInput(inputs.IAMPreMigrationStatus),
		})
	}
	return out
}

// prereqStatusFromInput maps plan-input status tokens to the rendered
// PrereqStatus enum. Unknown values fall through to `unconfirmed`.
func prereqStatusFromInput(status string) types.PrereqStatus {
	switch status {
	case PrereqStatusCompleteInput:
		return types.PrereqMet
	case PrereqStatusInProgressInput:
		return types.PrereqInProgress
	case PrereqNotStarted:
		return types.PrereqBlocked
	default:
		return types.PrereqUnconfirmed
	}
}
