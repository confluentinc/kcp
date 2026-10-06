package engine

import "strings"

// Human Assist — a first-class output, not a fallback: it routes genuinely
// complex scenarios and protects trust by handing off answers we could compute
// but would rather not have a customer self-serve into. A high trigger rate is
// the expected outcome.

const breadthFabric = "Shared fabric across many apps and teams"

// specialistHandoff is the single closing call to action on every human-assist
// trigger, so they read consistently (a "specialist"), except the Dedicated sizing
// handoff below.
const specialistHandoff = "Talk to a specialist and they'll work through it with you."

// dedicatedHandoff closes a Dedicated-cluster handoff, where the next step is sizing the
// cluster rather than working through a design.
const dedicatedHandoff = "Talk to one of our technical experts and they'll size it with you."

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
	case "mtls_on_gcp_target":
		if authHas(p, authMTLS) {
			return "your answer that your source uses mTLS", "keeping mTLS on your Google Cloud target needs a Dedicated cluster."
		}
		return "your answer that you chose mTLS for your clients on Confluent Cloud", "mTLS on your Google Cloud target needs a Dedicated cluster."
	case "band4_exceeds_enterprise_cap":
		// The size band is a planning cutoff, not what an Enterprise cluster can hold, so
		// the handoff names the size as the driver and leaves the reason to the cause's
		// custom-sizing encouragement.
		src = "the size of your workload"
		if driver := sizingDriverPhrase(p); driver != "" {
			src = "your workload size (" + driver + ")"
		}
		return src, ""
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
	GCPPrivateLink   bool
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

// iamCrossCloudWhy is the customer-facing handoff when the jump cluster the IAM
// source would need can't reach the target cloud.
func iamCrossCloudWhy(tc string) string {
	name := cloudDisplay(tc)
	return "Your MSK cluster uses AWS IAM, which can't cross a cluster link, and the jump cluster we'd use instead only runs on AWS. Your Confluent Cloud cluster is on " + name + ", so we'll design the migration path with you."
}

func humanAssistDecision(p Profile, ctx haCtx) HumanAssistResult {
	var triggers []Trigger
	add := func(id, why, internal string) {
		triggers = append(triggers, Trigger{ID: id, Why: why, Internal: internal})
	}

	// The GCP private-source link is its own handoff, never a Dedicated cause: raised
	// when networking needs the link or the app-scope answer is an explicit yes.
	linkCause := ctx.GCPPrivateLink || gcpPrivateLinkHandoff(p, ctx.Tier)

	// An AWS IAM source whose jump cluster can't reach the target cloud has its own
	// handoff below, which says the same thing, so it is the only one raised.
	iamCrossCloud := iamCrossCloudJumpCluster(p, ctx.Tier, p.AnyAppNeedsDataMigration) ||
		(p.NeedsDataMigration != "" && iamCrossCloudJumpCluster(p, ctx.Tier, p.NeedsDataMigration))

	// Every route to Dedicated is an edge case; name the requirement, not just the
	// outcome. exceeds_enterprise_limits is excluded here — it has its own trigger
	// below with protected copy.
	if ctx.Tier == TierDedicated {
		var causes []cause
		for _, c := range ctx.DedicatedReasons {
			// The GCP private-source link is its own handoff trigger below, not a
			// Dedicated escalation.
			if linkCause && c.Customer == gcpLinkCause {
				continue
			}
			if c.Customer != "" && c.ID != "exceeds_enterprise_limits" {
				causes = append(causes, c)
			}
		}
		if len(causes) == 0 && !exceedsEnterpriseLimits(p) && !linkCause {
			causes = []cause{{Customer: "the size and shape of this workload"}}
		}
		for i, c := range causes {
			// Attribute to the real driver (an mTLS source, the size/shape that crossed,
			// the named cause) and render in the one normalized specialist shape.
			src, reason := dedicatedSrcReason(p, c)
			why := "Based on " + src + ":"
			for _, part := range []string{reason, c.Encouragement, dedicatedHandoff} {
				if part != "" {
					why += " " + part
				}
			}
			add("dedicated_"+itoa(i), why, "Dedicated escalation: "+c.Customer+". Sizing and commercial terms both need a human.")
		}
	}

	// GCP target, private source, data moving over a cluster link (by the infra-wide
	// or this cluster's own answer): Cluster Linking into Google Cloud isn't
	// supported over Private Service Connect, so a specialist designs the path.
	if linkCause && !iamCrossCloud {
		add("gcp_private_source_cluster_link", gcpLinkWhy,
			"GCP target, private source, data moving over a cluster link: Cluster Linking from a private source into Google Cloud isn't supported over Private Service Connect. Route to a specialist to design the path.")
	}

	// MSK on AWS IAM (no SASL/SCRAM) would be bridged by a jump cluster, but that
	// infrastructure only runs on AWS, so a non-AWS target needs a designed path.
	if iamCrossCloud {
		add("iam_cross_cloud_jump_cluster", iamCrossCloudWhy(targetCloud(p)),
			"MSK source on AWS IAM with no SASL/SCRAM listener, non-AWS target: IAM can't cross a cluster link and kcp's jump cluster only runs on AWS (PrivateLink VPC endpoint). Route to a specialist to design the path.")
	}

	// Declared a workload that benefits from custom sizing (Band 2 only). The banner
	// keeps it general (no tier, no specific ceiling); the internal record keeps the figures.
	if exceedsEnterpriseLimits(p) && ctx.Band > sharedTierMaxBand && ctx.Band < bandXL {
		add("exceeds_enterprise_limits",
			specialistWhy("your workload being at a scale that benefits from custom sizing",
				"we'd plan the right cluster with you rather than size it automatically."),
			"Declared a workload beyond the standard sizing bands ("+joinComma(enterpriseLimits())+"). Route to a specialist: sizing needs a human.")
	}

	// Compound topology — only on the private path, on an AWS target, where we ask
	// (connects_today is only asked when WillBePrivate(p) && TargetCloudOf(p) == "AWS").
	if requiresPrivate(p) && targetCloud(p) == "AWS" && connectsToday(p) == connectsOther {
		add("connection_other",
			specialistWhy("your answer that you connect over more than one method",
				"the right combination depends on details we can't see from here, so we'd like to go through it with you."),
			"")
	}

	// An Apache Kafka / Confluent Platform source that runs on-premises (or in
	// another environment outside any cloud network), migrating data to a private
	// Confluent Cloud target over a cluster link: Confluent Cloud's documented
	// private routes (Egress PrivateLink Endpoint, GCP egress PSC) only reach a
	// source inside a cloud VPC/VNet, so there is no self-serve path here. Public
	// targets and non-cluster-link mechanisms (Replicator, start fresh) sidestep
	// this entirely, so only the private + cluster-link combination fires it.
	if p.isOSKorCP() && p.SourceCloud == "On-prem or other" &&
		!strings.HasPrefix(ctx.NetworkingMethod, "Public") &&
		(mechanismUsesClusterLink(p, ctx.Tier, p.AnyAppNeedsDataMigration) ||
			(p.NeedsDataMigration != "" && mechanismUsesClusterLink(p, ctx.Tier, p.NeedsDataMigration))) {
		// With the data-migration answer still open, lead with the conditional promise,
		// then state the facts as they stand either way.
		answered := p.NeedsDataMigration == "Yes" || p.AnyAppNeedsDataMigration == "Yes"
		lead, design := "", " We'll design the path with you."
		if !answered {
			lead, design = "If you move existing data, we'll design the private path with you. ", ""
		}
		peering := "network peering"
		if targetCloud(p) == "AWS" {
			peering = "network peering or Transit Gateway"
		}
		// The Dedicated route reaches on-premises brokers over the hybrid link into the
		// target cloud, so name it as the Confluent Platform copy below does.
		// The Dedicated banner above already names the tier when it fires, so then this
		// sentence names none: an Enterprise cluster it doesn't recommend would contradict it.
		subject := "An Enterprise cluster"
		if ctx.Tier == TierDedicated {
			subject = "Confluent Cloud"
		}
		why := lead + subject + " can't link to Apache Kafka brokers outside a cloud network over a private endpoint." + design + " The options are Replicator running in your network, temporary public broker endpoints, or a cluster link into a Dedicated cluster over " + peering + "."
		if p.isCP() {
			// The confirm-and-design closer is a promise for a settled "Yes" only; an open
			// answer already led with the conditional promise above.
			confirm := ""
			if answered {
				confirm = " We'll confirm your version and connectivity with you and design the link together."
			}
			why = lead + "Your Confluent Platform brokers run outside a cloud network, so Confluent Cloud can't reach them over a private endpoint. The usual private route is a source-initiated cluster link: your brokers connect out to Confluent Cloud over your " + privateLinkName(targetCloud(p)) + " link. It needs Confluent Platform 7.1 or later on Confluent Server brokers." + confirm
		}
		add("onprem_private_cluster_link", why,
			"On-prem/other Apache Kafka or Confluent Platform source, private target, data moving over a cluster link: no self-serve private path (Egress PrivateLink Endpoint / GCP egress PSC only reach a source inside a cloud VPC or VNet). Confluent Platform needs a source-initiated link (7.1+ on Confluent Server); Apache Kafka has no documented private path at all (Replicator, public endpoints, or Dedicated with peering/Transit Gateway).")
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

// privateLinkName names the private connection from an on-premises network into the
// target cloud: Direct Connect (AWS), ExpressRoute (Azure), Cloud Interconnect
// (Google Cloud), or all three while the target is unknown.
func privateLinkName(cloud string) string {
	switch cloud {
	case "AWS":
		return "Direct Connect"
	case "Azure":
		return "ExpressRoute"
	case "GCP":
		return "Cloud Interconnect"
	}
	return "Direct Connect, ExpressRoute, or Cloud Interconnect"
}
