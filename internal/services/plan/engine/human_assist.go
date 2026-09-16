package engine

// Human Assist — a first-class output, not a fallback: it routes genuinely
// complex scenarios and protects trust by handing off answers we could compute
// but would rather not have a customer self-serve into. A high trigger rate is
// the expected outcome.

const breadthFabric = "Shared fabric across many apps and teams"

// specialistHandoff is the single closing call to action on every human-assist
// trigger, so they read consistently (a "specialist", never "technical experts").
const specialistHandoff = "Talk to a specialist and they'll work through it with you."

// specialistWhy renders a trigger reason in the one normalized shape every
// human-assist trigger shares: "Based on <src>: <reason> <CTA>". src names what set
// it off (an answer, or a scanned finding); reason is the plain explanation.
func specialistWhy(src, reason string) string {
	return "Based on " + src + ": " + reason + " " + specialistHandoff
}

// dedicatedSrcReason attributes one Dedicated cause to its real driver, so the
// specialist Why reads honestly — an mTLS SOURCE (not an unset target-auth choice),
// or the size/shape that crossed the ceiling. Other causes (e.g. the migration
// link's outbound connection) already carry an honest customer phrase.
func dedicatedSrcReason(p Profile, c cause) (src, reason string) {
	switch c.ID {
	case "mtls_on_non_aws_target":
		return "your answer that your source uses mTLS", "keeping mTLS on your " + targetCloud(p) + " target needs a Dedicated cluster."
	case "band4_exceeds_enterprise_cap":
		cust := c.Customer
		if cap := sizingCapForDriver(p); cap != "" {
			cust += " (" + cap + ")"
		}
		return lowerFirst(cust), "we'd plan this with you on a Dedicated cluster."
	default:
		return lowerFirst(c.Customer), "we'd plan this with you on a Dedicated cluster."
	}
}

// openVerdictValues mean "we don't have an answer yet" rather than a
// recommendation; computePlan's `complete` flag reads them. "Start fresh" is a
// complete answer and is deliberately absent.
var openVerdictValues = []string{"Pending", "Unknown", "Not set yet", "Not assessed", "Special handling"}

// Trigger is one human-assist reason. Why is customer-facing (no pricing);
// Internal is the commercial reasoning, never rendered to the customer.
type Trigger struct {
	ID       string `json:"id"`
	Why      string `json:"why"`
	Internal string `json:"-"` // commercial reasoning; kept in-process, never serialized to the contract
}

// HumanAssistResult is the human-assist verdict.
type HumanAssistResult struct {
	Value    string    `json:"value"`
	Required bool      `json:"required"`
	Triggers []Trigger `json:"triggers,omitempty"`
	Reason   string    `json:"reason"`
	Action   string    `json:"action"`
	Internal string    `json:"-"` // commercial reasoning; kept in-process, never serialized to the contract
}

// haCtx is what humanAssistDecision needs from the settled plan.
type haCtx struct {
	Tier             Tier
	Band             int
	NetworkingMethod string
	DedicatedReasons []cause
	Complete         bool
}

// sizingDriverPhrase is the self-describing value of the dimension that drove the
// band (e.g. "34,000 partitions", "700 megabytes/sec peak ingress", "Over 30,000
// partitions"), or "" when nothing was measured. It lets a size-based handoff lead
// with the number that actually triggered it.
func sizingDriverPhrase(p Profile) string {
	for _, s := range sizingSignals(p) {
		if s.Driver {
			return s.Value
		}
	}
	return ""
}

// dimCeilingAtBand is the self-describing upper edge of a dimension at a given
// band — the value a workload sitting in that band would stay under. Used to name
// the throughput a partition-matched workload would run, so the unusual-shape
// handoff can say "above N megabytes/sec" instead of "above what your partitions imply".
func dimCeilingAtBand(d dim, band int) string {
	edges := bandEdges[d]
	if band < 1 || band > len(edges) {
		return ""
	}
	v := formatCommas(float64(edges[band-1]))
	switch d {
	case dimIngress, dimEgress:
		return v + " megabytes/sec"
	case dimRequestRate:
		return v + " requests/sec"
	case dimPartitions:
		return v + " partitions"
	}
	return ""
}

// sizingCapForDriver names what an Enterprise cluster holds on the dimension that
// drove the band — the band-3 edge (the 10 eCKU Enterprise/PrivateLink cap) for
// that dimension. Used to say "above what our Enterprise clusters hold (N …)".
func sizingCapForDriver(p Profile) string {
	// The top band edge is band 2's ceiling; dimCeilingAtBand(_, 2) reads it.
	return dimCeilingAtBand(dim(sizingBand(p).Driver), sharedTierMaxBand+1)
}

// partitionAnchorPhrase is the partition count the other dimensions are read
// against: the exact scanned count, the supplied band, or a bare fallback.
func partitionAnchorPhrase(p Profile) string {
	if p.PartitionsExact != nil {
		return formatCommas(*p.PartitionsExact) + " partitions"
	}
	if p.PartitionBand != "" {
		return p.PartitionBand + " partitions"
	}
	return "your partition count"
}

func humanAssistDecision(p Profile, ctx haCtx) HumanAssistResult {
	var triggers []Trigger
	add := func(id, why, internal string) {
		triggers = append(triggers, Trigger{ID: id, Why: why, Internal: internal})
	}

	// Every route to Dedicated is an edge case; name the requirement, not just the
	// outcome. exceeds_enterprise_limits is excluded here — it has its own trigger
	// below with protected copy.
	if ctx.Tier == TierDedicated {
		var causes []cause
		for _, c := range ctx.DedicatedReasons {
			if c.Customer != "" && c.ID != "exceeds_enterprise_limits" {
				causes = append(causes, c)
			}
		}
		if len(causes) == 0 && !exceedsEnterpriseLimits(p) {
			causes = []cause{{Customer: "the size and shape of this workload"}}
		}
		for i, c := range causes {
			// Attribute to the real driver (an mTLS source, the size/shape that crossed,
			// the named cause) and render in the one normalized specialist shape.
			src, reason := dedicatedSrcReason(p, c)
			why := "Based on " + src + ": " + reason
			if c.Encouragement != "" {
				why += " " + c.Encouragement
			}
			why += " " + specialistHandoff
			add("dedicated_"+itoa(i), why, "Dedicated escalation: "+c.Customer+". Sizing and commercial terms both need a human.")
		}
	}

	// Declared past the Enterprise ceiling (Band 2 only). The banner names the
	// tier, never a specific ceiling; the internal record keeps the figures.
	if exceedsEnterpriseLimits(p) && ctx.Band > sharedTierMaxBand && ctx.Band < bandXL {
		add("exceeds_enterprise_limits",
			specialistWhy("your answer that your workload goes past what our Enterprise clusters hold",
				"we'd plan a Dedicated cluster with you rather than size it automatically."),
			"Declared past the Enterprise ceiling ("+joinComma(enterpriseLimits())+"). Route to Dedicated: sizing needs a human.")
	}

	// Compound topology — only on the private path, where we ask.
	if requiresPrivate(p) && connectsToday(p) == connectsOther {
		add("connection_other",
			specialistWhy("your answer that you connect over more than one method",
				"the right combination depends on details we can't see from here, so we'd like to go through it with you."),
			"")
	}

	// Breadth is an upside trigger — sprawl is where Confluent wins on cost.
	if p.UseCaseBreadth == breadthFabric {
		add("use_case_breadth",
			specialistWhy("your answer that this cluster is a shared fabric across many apps and teams",
				"shared clusters like yours usually involve more moving parts than an automated plan can account for, so we'd like to talk through the right approach for your footprint."),
			"Highest migration complexity: many teams, many apps, coupled cutovers. A genuine confidence failure, which carries this on its own.")
	}

	// A reviewed dimension came back a full band above the partition anchor. The
	// dimensions genuinely disagree, which prices very differently and can't be sized
	// from a form, so we route it to a person rather than let max-wins hand back a
	// confident answer that is probably wrong.
	if shape := sizingShape(p); shape.Mismatch != nil {
		driverVal := sizingDriverPhrase(p)
		if driverVal == "" {
			driverVal = "your " + shape.Mismatch.Name
		}
		// Name the throughput a workload with this partition count would normally stay
		// under (the mismatched dimension's ceiling at the anchor's band), so we say
		// "above N megabytes/sec" rather than "above what your partitions imply".
		ceiling := dimCeilingAtBand(dim(shape.Mismatch.Name), shape.Mismatch.AnchorBand)
		reason := "your " + driverVal + " sits a full size band above what " + partitionAnchorPhrase(p) + " would imply. That's an unusual shape, not a wrong answer, so we'd rather size it with you than have a form guess."
		if ceiling != "" {
			reason = "your " + driverVal + " is above the " + ceiling + " that " + partitionAnchorPhrase(p) + " would suggest. That's an unusual shape, not a wrong answer, so we'd rather size it with you than have a form guess."
		}
		add("unusual_workload_shape", specialistWhy("your scanned workload shape", reason),
			"Dimensions disagree by "+itoa(shape.Mismatch.Band-shape.Mismatch.AnchorBand)+" band(s): "+
				shape.Mismatch.Name+" at Band "+itoa(shape.Mismatch.Band)+" vs "+string(sizingAnchor)+" at Band "+itoa(shape.Mismatch.AnchorBand)+
				". Check whether it is a fan-out/partition-sprawl pattern before quoting.")
	}

	if len(triggers) == 0 {
		cleanReason := "You still have a few open questions. Answer them to finalize your plan, or talk to us if you'd like a hand."
		if ctx.Complete {
			cleanReason = "Your answers land inside what we can plan. This plan is yours to run. Talk to us if you want a second opinion."
		}
		return HumanAssistResult{Value: "Not needed", Required: false, Reason: cleanReason, Action: "Talk to a person"}
	}

	whys := make([]string, len(triggers))
	for i, t := range triggers {
		whys[i] = t.Why
	}
	return HumanAssistResult{
		Value:    "Recommended",
		Required: true,
		Triggers: triggers,
		Reason:   joinSpace(whys),
		Action:   "Talk to a person",
	}
}
