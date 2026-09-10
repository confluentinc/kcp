package engine

import (
	"math"
	"strings"
)

// Block 2 — Cluster type. A workload lands on Standard, Enterprise, or Dedicated.
// Basic is never an output. Two hard rules (plus a declared Enterprise-ceiling
// breach) force Dedicated; the private/public requirement and the size band
// settle Standard vs Enterprise. Networking can escalate the tier after the fact
// via the two-phase settle (the extraForced argument), never the reverse.

// ── Tier-ceiling questions (generated from tierMax, never typed) ────────────

func tierLimits(tier Tier) []string {
	m := tierMax[tier]
	n := func(v int) string { return formatCommas(float64(v)) }
	return []string{
		n(m.IngressMbps) + " megabytes/sec ingress",
		n(m.EgressMbps) + " megabytes/sec egress",
		n(m.RequestsPerSec) + " requests/sec",
	}
}

func limitsQuestion(list []string) string {
	return "Does your workload exceed any of the following: " +
		strings.Join(list[:len(list)-1], ", ") + ", or " + list[len(list)-1] + "?"
}

func enterpriseLimits() []string       { return tierLimits(TierEnterprise) }
func standardLimits() []string         { return tierLimits(TierStandard) }
func enterpriseLimitsQuestion() string { return limitsQuestion(enterpriseLimits()) }
func standardLimitsQuestion() string   { return limitsQuestion(standardLimits()) }

// "No" is the pre-selected default, so unanswered resolves to the same verdict.
func exceedsEnterpriseLimits(p Profile) bool { return p.ExceedsEnterpriseLimits == "Yes" }
func exceedsStandardLimits(p Profile) bool   { return p.ExceedsStandardLimits == "Yes" }

// ── tierFit — does the workload fit a tier's published ceilings? ─────────────
// Three-valued ("yes" | "no" | "maybe"): a band can STRADDLE a tier ceiling, so a
// banded answer often cannot settle it. An exact figure settles it outright.
// Only dimensions we actually have a figure or band for are checked.
func tierFit(p Profile, tier Tier) string {
	m, ok := tierMax[tier]
	if !ok {
		return "yes"
	}
	type dimCheck struct {
		exact *float64
		max   int
		band  dim // "" for acls/connections, which have no band
	}
	checks := []dimCheck{
		{p.PartitionsExact, m.Partitions, dimPartitions},
		{p.PeakIngressMbps, m.IngressMbps, dimIngress},
		{p.PeakEgressMbps, m.EgressMbps, dimEgress},
		{p.PeakRequestsPerSec, m.RequestsPerSec, dimRequestRate},
		{p.ACLCountExact, m.ACLs, ""},
		{p.ClientConnectionsExact, m.Connections, ""},
	}
	given := sizingBand(p).Given
	verdict := "yes"
	for _, c := range checks {
		if verdict == "no" {
			break
		}
		if c.exact != nil { // an exact figure settles it
			if *c.exact > float64(c.max) {
				verdict = "no"
			}
			continue
		}
		if c.band == "" {
			continue
		}
		band, has := given[c.band]
		if !has {
			continue
		}
		edges := bandEdges[c.band]
		lo := 0
		if band != 1 {
			lo = edges[band-2]
		}
		hi := math.Inf(1)
		if band <= len(edges) {
			hi = float64(edges[band-1])
		}
		if float64(lo) >= float64(c.max) { // the whole band is over
			verdict = "no"
		} else if hi > float64(c.max) && verdict == "yes" { // the band straddles it
			verdict = "maybe"
		}
	}
	return verdict
}

// smallTier is Standard, always. Basic is never recommended and never reachable:
// it offers only a 99.5% single-AZ SLA, and a migration target should not start
// below the multi-AZ cluster the customer already runs on MSK.
func smallTier(p Profile) Tier { return TierStandard }

// ── The verdict ─────────────────────────────────────────────────────────────

type ruleEval struct {
	ID            string `json:"id"`
	Customer      string `json:"customer"`
	Encouragement string `json:"encouragement,omitempty"`
	Fired         bool   `json:"fired"`
}

type cause struct {
	ID            string `json:"id"`
	Customer      string `json:"customer"`
	Encouragement string `json:"encouragement,omitempty"`
}

// forcedDedicated carries a networking escalation back into the tier decision
// (computePlan's two-phase settle). Its Reason is already in the customer's
// voice, so it becomes a cause directly.
type forcedDedicated struct {
	Fired         bool
	Reason        string
	Encouragement string
}

// ClusterTypeResult is the tier verdict.
type ClusterTypeResult struct {
	Value            Tier    `json:"value"`
	Pending          bool    `json:"pending,omitempty"` // set at JSON marshal when a deciding question is unanswered; Value is blanked
	Tier             Tier    `json:"tier"`
	Dedicated        bool    `json:"dedicated"`
	CrossedToPrivate bool    `json:"crossed_to_private"`
	SelfServe        bool    `json:"self_serve"`
	Causes           []cause `json:"causes,omitempty"` // customer-facing
	ZoneConfig       string  `json:"zone_config"`
	ZoneReason       string  `json:"zone_reason"`
	Reason           string  `json:"reason"`
	Why              string  `json:"why"`
	AssessmentReason string  `json:"assessment_reason"`
	Action           string  `json:"action"`
	Source           string  `json:"source,omitempty"`
}

func clusterTypeRules(p Profile, sizing SizingResult, tc string) (rules []ruleEval, hardFired []ruleEval) {
	mtlsNonAws := tc != "" && tc != "AWS" && (authHas(p, authMTLS) || targetHasMtls(p))

	// A declared Enterprise-ceiling breach forces Dedicated — meaningful only on
	// Band 2, which is the band whose card asks the Enterprise ceilings.
	overEnterprise := exceedsEnterpriseLimits(p) && sizing.Band > sharedTierMaxBand && sizing.Band < bandXL

	rules = []ruleEval{
		{
			ID:            "band4_exceeds_enterprise_cap",
			Customer:      "a workload above what our Enterprise clusters hold",
			Encouragement: "Dedicated clusters scale well past the limits we publish for Enterprise.",
			Fired:         sizing.Band == bandXL,
		},
		{
			ID:            "mtls_on_non_aws_target",
			Customer:      "mTLS authentication on your " + tc + " target",
			Encouragement: "Dedicated supports mTLS on your target cloud.",
			Fired:         mtlsNonAws,
		},
		{
			// No encouragement: this one withholds the plan and renders a bespoke
			// human-assist trigger rather than the generic Dedicated template.
			ID:       "exceeds_enterprise_limits",
			Customer: "a workload above what our Enterprise clusters hold",
			Fired:    overEnterprise,
		},
	}
	for _, r := range rules {
		if r.Fired {
			hardFired = append(hardFired, r)
		}
	}
	return rules, hardFired
}

func clusterType(p Profile, sizing SizingResult, tc string, extraForced *forcedDedicated) ClusterTypeResult {
	_, hardFired := clusterTypeRules(p, sizing, tc)
	dedicated := len(hardFired) > 0

	causes := make([]cause, 0, len(hardFired))
	for _, x := range hardFired {
		causes = append(causes, cause{ID: x.ID, Customer: x.Customer, Encouragement: x.Encouragement})
	}

	if !dedicated && extraForced != nil && extraForced.Fired {
		dedicated = true
		causes = []cause{{ID: "networking_forces_dedicated", Customer: extraForced.Reason, Encouragement: extraForced.Encouragement}}
	}

	privateReq := requiresPrivate(p)
	crossedToPrivate := false
	crossedForSize, crossedForMtls, crossedForThroughput := false, false, false
	// Read only on Band 1, where the Standard-ceiling card is the one shown.
	exceedsStandardCeiling := sizing.Band <= sharedTierMaxBand && exceedsStandardLimits(p)

	var tier Tier
	switch {
	case dedicated:
		tier = TierDedicated
	case !privateReq:
		if sizing.Band <= sharedTierMaxBand && !mtlsNeeded(p) && !exceedsStandardCeiling {
			tier = smallTier(p)
		} else {
			tier = TierEnterprise
			crossedToPrivate = true
			crossedForSize = sizing.Band > sharedTierMaxBand
			crossedForMtls = mtlsNeeded(p)
			crossedForThroughput = exceedsStandardCeiling
		}
	default:
		tier = TierEnterprise
	}

	// Every plan is multi-zone. Single-zone is no longer an output.
	zoneConfig := "Multi-zone"
	zoneSlaCeiling := "99.99%"
	if sla := tierSLA[tier]; len(sla) > 0 {
		zoneSlaCeiling = sla[len(sla)-1]
	}
	zoneReason := "Spread across 3 Availability Zones, with a published uptime SLA up to " + zoneSlaCeiling +
		" for this cluster type once you provision 2 or more eCKU (elastic Confluent Unit for Kafka). See the cluster-type docs for the SLA terms."

	var tierReason, assessReason, tierWhy string
	switch {
	case dedicated:
		// A Dedicated plan is always withheld (routed to a specialist) before any of
		// these strings would render or serialize, so we build none of them. The
		// Causes above stay live — they feed the human-assist trigger, which carries
		// the customer-facing copy for a Dedicated escalation.
	case crossedToPrivate:
		var crossCauses []string
		var crossDrivers []driver
		if crossedForSize {
			crossCauses = append(crossCauses, "your workload is above what a Standard cluster holds")
			t := "workload above Standard"
			if p.PartitionsExact != nil {
				t = formatCommas(*p.PartitionsExact) + " partitions"
			}
			crossDrivers = append(crossDrivers, srcOr(p.PartitionsAnswered, t))
		}
		if crossedForThroughput {
			crossCauses = append(crossCauses, "your throughput is above what a Standard cluster holds")
			t := "throughput above Standard"
			if p.PeakIngressMbps != nil {
				t = formatCommas(*p.PeakIngressMbps) + " megabytes/sec ingress"
			}
			// The cross is driven by the exceeds_standard_limits ANSWER, not a scanned
			// measurement, so attribute it to the answer (in scanless mode "your scan"
			// would be a lie).
			crossDrivers = append(crossDrivers, ans(t))
		}
		if crossedForMtls {
			crossCauses = append(crossCauses, "mTLS authentication needs an Enterprise cluster or above")
			crossDrivers = append(crossDrivers, srcOr(p.AuthAnswered, "mTLS in use"))
		}
		crossDrivers = append(crossDrivers, ans("public networking acceptable"))
		crossWhy := strings.Join(crossCauses, ", and ") + ". Enterprise clusters run on private networking"
		tierReason = basis(crossDrivers...) + "we recommend an Enterprise cluster with private networking: " + crossWhy + "."
		assessReason = "Your answers point to Enterprise with private networking: public is acceptable to you, but " + crossWhy + "."
		tierWhy = "Needed because " + strings.Join(crossCauses, ", and ") + "."
	case tier == TierEnterprise:
		if privateAnsweredField(p) {
			tierReason = basis(ans("private networking required")) + "an Enterprise cluster fits: private networking starts at Enterprise, and nothing else you told us pushes it up to Dedicated."
			assessReason = "Your answers point to Enterprise: private networking is a hard requirement for you, and that starts at Enterprise. Nothing else you told us pushes it up to Dedicated."
			tierWhy = "Private networking is a hard requirement you set, and that starts at Enterprise."
		} else {
			// The public/private fork is unanswered; private is the default assumption
			// (everyone on MSK runs private today), so we don't credit an answer the
			// customer hasn't given. Answering it public only drops the tier to Standard
			// when the workload actually fits Standard — the same test the answered-public
			// branch applies. When the size band (or another forcing signal) crosses to
			// private regardless, Enterprise is required either way, so we don't dangle a
			// Standard that answering can't reach.
			fitsStandard := sizing.Band <= sharedTierMaxBand && !mtlsNeeded(p) && !exceedsStandardCeiling
			if fitsStandard {
				tierReason = "Until you tell us whether public endpoints are acceptable, we assume private networking, which starts at Enterprise; nothing else you told us pushes it up to Dedicated. If public is fine, answer `private_networking_required` and this may move to Standard."
				assessReason = "Private networking is the default assumption until you answer whether public endpoints are acceptable: it starts at Enterprise, and nothing else you told us pushes it up to Dedicated."
				tierWhy = "Private networking is assumed by default (answer `private_networking_required` to confirm)."
			} else {
				forcedCause := "your workload is above what a Standard cluster holds"
				switch {
				case mtlsNeeded(p):
					forcedCause = "mTLS authentication needs an Enterprise cluster or above"
				case exceedsStandardCeiling:
					forcedCause = "your throughput is above what a Standard cluster holds"
				}
				tierReason = "Until you tell us whether public endpoints are acceptable, we assume private networking. Either way this is an Enterprise cluster, because " + forcedCause + "; answering `private_networking_required` won't move it to Standard. Nothing else you told us pushes it up to Dedicated."
				assessReason = "Private networking is the default assumption until you answer whether public endpoints are acceptable, but the workload requires Enterprise regardless: " + forcedCause + ". Nothing else you told us pushes it up to Dedicated."
				tierWhy = "Enterprise is required by your workload size (private networking is assumed by default)."
			}
		}
	default:
		tierReason = basis(ans("private networking not required"), srcOr(p.PartitionsAnswered, "workload fits "+string(tier))) + "a " + string(tier) + " cluster holds your workload and is the simplest fully managed option for it."
		// A Standard target quietly implies the data-movement mechanism: Cluster Linking
		// needs Enterprise/Dedicated, so moving existing data runs on self-managed
		// Replicator. Point at that here so the two recommendations aren't read in
		// isolation (mechanism only — no cost/effort claims).
		if tier == TierStandard && p.NeedsDataMigration != "No" {
			tierReason += " Moving existing data into a Standard cluster runs on self-managed Confluent Replicator (a Confluent Platform Enterprise license); an Enterprise cluster would migrate over managed Cluster Linking instead — no self-managed worker, no license."
		}
		assessReason = "Your answers point to " + string(tier) + ": private networking is not a hard requirement for you, and " + string(tier) + " holds this workload."
		tierWhy = "Public networking works for your workload size."
	}

	article := "a "
	if isVowel(rune(tier[0])) {
		article = "an "
	}

	return ClusterTypeResult{
		Value:            tier,
		Tier:             tier,
		Dedicated:        dedicated,
		CrossedToPrivate: crossedToPrivate,
		SelfServe:        tierCapabilities[tier].SelfServe,
		Causes:           causes,
		ZoneConfig:       zoneConfig,
		ZoneReason:       zoneReason,
		Reason:           tierReason,
		Why:              tierWhy,
		AssessmentReason: assessReason,
		Action:           "Create " + article + string(tier) + " cluster",
	}
}
