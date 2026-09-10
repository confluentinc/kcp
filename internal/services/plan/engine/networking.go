package engine

import "strings"

// Block 3 — Networking: the connection method, from the private requirement.
// Cluster type gates the menu; a genuine requirement escalates cluster type via
// ForcesDedicated (folded back in computePlan). Never the reverse.

// methodTradeoff is the qualitative pros/cons/fit for one connection method.
// Cost is a tier, never a dollar figure.
type methodTradeoff struct {
	Pros []string
	Cons []string
	Fit  string
}

// methodTradeoffs — the flat (cloud-independent) methods. VPC Peering and
// Transit Gateway are Dedicated-only, and a Dedicated plan is always withheld
// before a networking method renders, so those methods never surface here.
var methodTradeoffs = map[string]methodTradeoff{
	"Public endpoint": {
		Pros: []string{"Fastest to set up, with no networking configuration"},
		Cons: []string{
			"Your brokers are reachable over the internet, so authentication and network policy are doing all the work",
			"Moving to private networking later means moving to a different cluster type",
		},
		Fit: "A quick evaluation, not production",
	},
	"PNI": {
		Pros: []string{"Scales to the full 32 eCKU, the top of this tier", "Traffic stays inside the Availability Zone when your clients are zone-aligned", "Private, one-way access"},
		Cons: []string{"Available on AWS only", "Not available on Dedicated clusters", "More complex to set up"},
		Fit:  "AWS deployments that want the most room to grow",
	},
}

// privateLinkTradeoffs — PrivateLink is cloud-keyed: the private-endpoint story
// differs by cloud (AWS caps Enterprise at 10 eCKU; Azure/GCP reach 32). AWS is
// the fallback.
var privateLinkTradeoffs = map[string]methodTradeoff{
	"AWS": {
		Pros: []string{"Recognized AWS-native standard", "Works even when your IP ranges overlap", "Available on both Enterprise and Dedicated"},
		Cons: []string{"Capped at 10 eCKU on this tier, so a larger cluster has to move to a different tier", "Adds an AWS-managed endpoint in the traffic path", "Needs a separate endpoint for outbound traffic"},
		Fit:  "Teams with a named requirement for PrivateLink, overlapping IP ranges, or moderate throughput",
	},
	"Azure": {
		Pros: []string{"Uses Azure Private Link, the native private connection on Azure", "Works even when your IP ranges overlap", "Scales to the full 32 eCKU on an Enterprise cluster"},
		Cons: []string{"Adds an Azure-managed endpoint in the traffic path", "Needs a separate endpoint for outbound traffic"},
		Fit:  "Any workload that needs private networking on Azure",
	},
	"GCP": {
		Pros: []string{"Uses Private Service Connect (PSC), the native private connection on Google Cloud", "Works even when your IP ranges overlap", "Scales to the full 32 eCKU on an Enterprise cluster"},
		Cons: []string{"Adds a Google Cloud-managed endpoint in the traffic path", "Needs a separate endpoint for outbound traffic"},
		Fit:  "Any workload that needs private networking on Google Cloud",
	},
}

// NetworkingResult is the connection-method verdict.
type NetworkingResult struct {
	Value   string  `json:"value"`
	Pending bool    `json:"pending,omitempty"` // set at JSON marshal when a deciding question is unanswered; Value is blanked
	Reason  string  `json:"reason"`
	Why     string  `json:"why,omitempty"`
	How     string  `json:"how,omitempty"`
	Action  *string `json:"action"` // nil for a public endpoint — nothing of its own to set up

	// HasEgressEndpoint is set when an Egress PrivateLink Endpoint is folded into the
	// method (for Confluent-managed egress and/or the migration cluster link). Render
	// reads it to gate the "the endpoint is added in the link step below" note; a
	// typed flag rather than matching the display Value. Not serialized.
	HasEgressEndpoint bool `json:"-"`
	// EgressForConnectors is set when the Egress PrivateLink Endpoint was added for the
	// runtime cc_egress_required need (managed connectors/consumers reaching into the
	// customer network), as opposed to purely for the migration link. Render reads it so
	// the migration-steps note reconciles the single endpoint with the connector
	// rationale in the networking "Why" rather than justifying it two different ways.
	// Not serialized.
	EgressForConnectors bool `json:"-"`

	ForcesDedicated              bool   `json:"forces_dedicated"`
	ForcesDedicatedReason        string `json:"forces_dedicated_reason,omitempty"`
	ForcesDedicatedEncouragement string `json:"forces_dedicated_encouragement,omitempty"`

	Pros []string `json:"pros,omitempty"`
	Cons []string `json:"cons,omitempty"`
	Fit  string   `json:"fit,omitempty"`

	// crossed-to-private public fallback: the tier a public endpoint would need.
	PublicFallback        *Tier `json:"public_fallback,omitempty"`
	PublicFallbackCertain bool  `json:"public_fallback_certain,omitempty"`

	Alternative *NetworkingResult `json:"alternative,omitempty"`

	Source string `json:"source,omitempty"`
}

func strptr(s string) *string { return &s }

// attachTradeoffs fills pros/cons/fit from the method the value names.
func attachTradeoffs(net *NetworkingResult, cloud string) *NetworkingResult {
	v := net.Value
	base := ""
	switch {
	case strings.HasPrefix(v, "PNI"):
		base = "PNI"
	case strings.HasPrefix(v, "PrivateLink"):
		base = "PrivateLink"
	case strings.HasPrefix(v, "Private Service Connect"):
		base = "PrivateLink"
	case strings.HasPrefix(v, "Public"):
		base = "Public endpoint"
	}
	if base == "PrivateLink" {
		t, ok := privateLinkTradeoffs[cloud]
		if !ok {
			t = privateLinkTradeoffs["AWS"]
		}
		net.Pros, net.Cons, net.Fit = t.Pros, t.Cons, t.Fit
	} else if t, ok := methodTradeoffs[base]; ok {
		net.Pros, net.Cons, net.Fit = t.Pros, t.Cons, t.Fit
	}
	return net
}

// withEgress adds an outbound-reachability endpoint when Confluent-managed
// connectors/consumers must reach into the customer network — independent of the
// ingress method.
func withEgress(p Profile, net *NetworkingResult, tc string) *NetworkingResult {
	if p.CCEgressRequired != "Yes" {
		return net
	}
	switch {
	case strings.HasPrefix(net.Value, "PNI"):
		net.Value = "PNI + Egress PrivateLink Endpoint"
		net.Reason += " Your Confluent-managed connectors and consumers also need to connect into your network, so we add an Egress PrivateLink Endpoint alongside PNI."
		if net.Why != "" {
			net.Why += " The Egress PrivateLink Endpoint lets Confluent-managed connectors and consumers connect into your private network."
		}
	case tc == "Azure":
		net.Value = "PrivateLink + Egress PrivateLink Endpoint"
		net.Reason += " Your Confluent-managed connectors and consumers also need to connect into your network, so we add an Egress PrivateLink Endpoint alongside PrivateLink."
		if net.Why != "" {
			net.Why += " The Egress PrivateLink Endpoint lets Confluent-managed connectors and consumers connect into your private network."
		}
	default:
		net.Reason += " Confluent Cloud connects into your network through an Egress PrivateLink Endpoint."
	}
	if strings.Contains(net.Value, "Egress PrivateLink Endpoint") {
		net.HasEgressEndpoint = true
		net.EgressForConnectors = true
	}
	return net
}

// withMigrationEgress adds the Egress PrivateLink Endpoint the migration cluster
// link needs to reach a privately-reached source, when any application pulls data
// over the link. This is distinct from withEgress (the runtime cc_egress_required
// managed-connector path): here it is the migration-time link reaching OUT to the
// source cluster. It fires only for the private inbound methods that don't
// themselves carry link egress — AWS PNI and Azure Enterprise PrivateLink — and
// only on the assembled call (ctx.ApplyMigrationEgress); the guard against an
// existing "Egress PrivateLink" keeps it from stacking on withEgress.
func withMigrationEgress(p Profile, net *NetworkingResult, tc string, tier Tier, apply bool) *NetworkingResult {
	if !apply || !clusterLinkNeedsPrivateEgress(p, tier, p.AnyAppNeedsDataMigration) || strings.Contains(net.Value, "Egress PrivateLink") {
		return net
	}
	if strings.HasPrefix(net.Value, "PNI") {
		net.Value = "PNI + Egress PrivateLink Endpoint"
		net.Reason += " Confluent Cloud pulls your data over the cluster link, reaching out to your source cluster. PNI doesn't carry Cluster Linking traffic, so the link needs an Egress PrivateLink Endpoint alongside PNI."
		if net.Why != "" {
			net.Why += " The Egress PrivateLink Endpoint is added so the migration cluster link can reach your source cluster, which PNI doesn't carry."
		}
	} else if tc == "Azure" && tier == TierEnterprise {
		net.Value = "PrivateLink + Egress PrivateLink Endpoint"
		net.Reason += " Confluent Cloud pulls your data over the cluster link, and PrivateLink is inbound only, so the link needs an Egress PrivateLink Endpoint alongside PrivateLink to reach your source cluster."
		if net.Why != "" {
			net.Why += " The Egress PrivateLink Endpoint is added so the migration cluster link can reach your source cluster, since PrivateLink is inbound only."
		}
	}
	if strings.Contains(net.Value, "Egress PrivateLink Endpoint") {
		net.HasEgressEndpoint = true
	}
	return net
}

// netCtx is the context networkingDecision needs: the target cloud, the sized
// band, the (settled or tentative) tier, and whether cluster type crossed a
// public-acceptable customer to private.
type netCtx struct {
	TC               string
	Band             int
	Tier             Tier // when "", derived from Dedicated
	Dedicated        bool
	CrossedToPrivate bool
	// ApplyMigrationEgress asks networkingDecision to fold in the migration
	// cluster-link's Egress PrivateLink Endpoint (see withMigrationEgress). Only
	// the assembled ComputePlan call sets it — the tentative first-pass calls (and
	// the networking unit-test wiring) leave it false, so the escalation lands
	// exactly once, on the settled result.
	ApplyMigrationEgress bool
}

func networkingDecision(p Profile, ctx netCtx) NetworkingResult {
	tc := ctx.TC
	tier := ctx.Tier
	if tier == "" {
		if ctx.Dedicated {
			tier = TierDedicated
		} else {
			tier = TierEnterprise
		}
	}
	dedicated := tier == TierDedicated

	// GCP needs an OUTBOUND private connection when a data migration pulls over a
	// Cluster Link, or managed connectors/consumers must reach into the customer
	// network. GCP's egress PSC is Dedicated-only, so either need forces Dedicated
	// — computed here so it also suppresses the public early-return.
	//
	// cc_egress_required is a private-path-only refinement: it must never push a
	// public-OK workload onto private/Dedicated, so it only counts once the plan is
	// already private (a real requirement, or a size-driven cross). The migration-link
	// egress driver is a separate condition and stands on its own.
	ccEgressOnPrivate := p.CCEgressRequired == "Yes" && (requiresPrivate(p) || ctx.CrossedToPrivate)
	gcpOutboundNeeded := tc == "GCP" &&
		(ccEgressOnPrivate || clusterLinkNeedsPrivateEgress(p, tier, p.AnyAppNeedsDataMigration))

	// The "Based on your answer (private networking required)," lead, credited only
	// when the public/private fork was actually answered (defaulted-private claims no
	// answer). Shared by the Azure/GCP and AWS private returns; the AWS crossed-to-
	// private path refines it further below.
	privLead := ""
	if privateAnsweredField(p) {
		privLead = basis(ans("private networking required"))
	}

	// Public path. Basic/Standard/Dedicated can serve a public endpoint; Enterprise cannot.
	if !requiresPrivate(p) && !ctx.CrossedToPrivate && !gcpOutboundNeeded {
		return *attachTradeoffs(&NetworkingResult{
			Value:           "Public endpoint",
			Reason:          basis(ans("private networking not required")) + "a public endpoint is the simplest way in. Moving to private networking later means moving to a different cluster type.",
			Why:             "No private networking to set up.",
			Action:          nil,
			ForcesDedicated: false,
		}, tc)
	}

	// Non-AWS clouds: endpoint methods only (no PNI off AWS).
	if tc == "Azure" || tc == "GCP" {
		if gcpOutboundNeeded {
			gcpOutboundCause := "your need for an outbound private connection on GCP"
			if !ccEgressOnPrivate {
				// The outbound need here is the migration link reaching your source, not a
				// runtime cc_egress requirement (which the customer did not ask for), so name
				// the real driver. Opened in the shared "your answer that …" specialist shape
				// (the other triggers use it) since the driver is the data-migration answer.
				gcpOutboundCause = "your answer that your data moves over the migration link's outbound private connection into your source on GCP"
			}
			publicInbound := !requiresPrivate(p) && !ctx.CrossedToPrivate
			reason := "On GCP an outbound private connection is only available on a Dedicated cluster, so we recommend Dedicated and plan it with you."
			if publicInbound {
				reason = "Your clients can reach Confluent Cloud over a public endpoint, but you also need an outbound private connection into your network. " + reason
			}
			return *attachTradeoffs(&NetworkingResult{
				Value:                        "Private Service Connect (moves to Dedicated)",
				Reason:                       reason,
				Why:                          "Needed because outbound private connections on GCP require a Dedicated cluster.",
				Action:                       strptr("Set up Private Service Connect"),
				ForcesDedicated:              true,
				ForcesDedicatedReason:        gcpOutboundCause,
				ForcesDedicatedEncouragement: "Dedicated supports outbound private connections on GCP.",
			}, tc)
		}
		if tc == "GCP" {
			gcpReason := "On GCP, we recommend Private Service Connect (PSC) for your private connection, which fits an Enterprise cluster."
			if privLead != "" {
				gcpReason = privLead + lowerFirst(gcpReason)
			}
			return *attachTradeoffs(&NetworkingResult{
				Value:  "Private Service Connect (PSC)",
				Reason: gcpReason,
				How:    "Private Service Connect (PSC) is the private connection on GCP (PNI is available on AWS only). It supports up to 32 eCKU.",
				Why:    "Private Service Connect (PSC) scales to the full 32 eCKU (elastic Confluent Unit for Kafka), so it grows with you.",
				Action: strptr("Set up Private Service Connect (PSC)"),
			}, tc)
		}
		azureReason := "On " + tc + ", we recommend PrivateLink for your private connection, which fits an Enterprise cluster."
		if privLead != "" {
			azureReason = privLead + lowerFirst(azureReason)
		}
		return *withMigrationEgress(p, attachTradeoffs(withEgress(p, &NetworkingResult{
			Value:  "PrivateLink",
			Reason: azureReason,
			How:    "PrivateLink is the private connection on " + tc + " (PNI is available on AWS only). It supports up to 32 eCKU.",
			Why:    "PrivateLink scales to the full 32 eCKU (elastic Confluent Unit for Kafka), so it grows with you.",
			Action: strptr("Set up PrivateLink"),
		}, tc), tc), tc, tier, ctx.ApplyMigrationEgress)
	}

	// ── AWS, private source ──
	// On a Dedicated cluster PNI is not available (Enterprise/Freight only), so
	// PrivateLink is the endpoint path.
	if dedicated {
		return *attachTradeoffs(withEgress(p, &NetworkingResult{
			Value:  "PrivateLink",
			Reason: "On a Dedicated cluster, PrivateLink is the private connection. PNI is available on Enterprise clusters rather than Dedicated.",
			Why:    "PNI isn't available on a Dedicated cluster, so PrivateLink is used instead.",
			Action: strptr("Set up PrivateLink"),
		}, tc), tc)
	}

	// Enterprise on AWS. PNI leads, always. We do not ask a PNI-vs-PrivateLink
	// preference; when the customer already connects over PrivateLink we surface
	// both with tradeoffs.
	pniScalesWhy := "PNI (Private Network Interface) scales to the full " + itoa(pniCapECKU) + " eCKU (elastic Confluent Unit for Kafka), so it grows with you."
	// The Reason names PNI in full just before this tail, so it says "It" rather than
	// repeating the expansion; the standalone Why (table cell) keeps the full form.
	pniScalesTail := "It scales to the full " + itoa(pniCapECKU) + " eCKU (elastic Confluent Unit for Kafka), so it grows with you."
	// privLead is set above (credited only on an answered public/private fork; a
	// defaulted-private plan leaves it empty and opens with a capitalized "On AWS …").
	// The crossed-to-private path refines it to the honest cross cause.
	if !requiresPrivate(p) && ctx.CrossedToPrivate {
		// The cross to private has three causes with different provenance: a scanned
		// size band (scan), or a customer answer (mTLS, or a declared Standard-limit
		// breach). Lead with the honest source rather than always crediting the scan.
		switch {
		case mtlsNeeded(p):
			privLead = basis(ans("mTLS authentication"))
		case exceedsStandardLimits(p):
			privLead = basis(ans("workload exceeds Standard limits"))
		default:
			privLead = basis(srcOr(p.PartitionsAnswered, "workload needs private networking"))
		}
	}
	// With a "Based on …," lead the clause continues lower-case; with no lead
	// (private defaulted) it opens the sentence, so it takes a capital.
	pniOpen := "on AWS, we recommend PNI (Private Network Interface) for your private connection. "
	if privLead == "" {
		pniOpen = "On AWS, we recommend PNI (Private Network Interface) for your private connection. "
	}
	net := attachTradeoffs(withEgress(p, &NetworkingResult{
		Value:  "PNI",
		Reason: privLead + pniOpen + pniScalesTail,
		How: "PNI is Confluent Cloud's recommended private connection on AWS: Confluent Cloud attaches interfaces directly into your VPC, so traffic never crosses the public internet, and it scales to the full " +
			itoa(pniCapECKU) + " eCKU, the top of the Enterprise tier.",
		Why:    pniScalesWhy,
		Action: strptr("Set up PNI"),
	}, tc), tc)

	// Crossed over from "public is acceptable": name the public fallback tier.
	// A cross driven by mTLS or a declared Standard-ceiling breach rules Standard out
	// entirely — Standard can't authenticate mTLS, and can't hold a declared over-limit
	// workload — so there's no public swap to offer, regardless of the size band. Only a
	// pure size-band cross leaves Standard as a possible public fallback.
	if ctx.CrossedToPrivate {
		appendPublicFallbackReason(p, net)
	}

	// Already on PrivateLink: keep it as a real alternative, with tradeoffs.
	if connectsToday(p) == connectsPrivateLink {
		net.Alternative = privateLinkAlternative(ctx.Band, tc)
	}

	// The migration cluster-link's outbound endpoint is the last egress mutation,
	// after any crossed-to-private tail, so its sentence reads at the end of Reason.
	withMigrationEgress(p, net, tc, tier, ctx.ApplyMigrationEgress)

	return *net
}

// appendPublicFallbackReason extends the PNI reason for a workload that crossed
// from "public is acceptable" to private. mTLS or a declared over-limit breach
// rules Standard out entirely (Standard can't authenticate mTLS and can't hold a
// declared over-limit workload); only a pure size-band cross leaves Standard as a
// public swap the customer could take instead, so it also records the fallback tier.
func appendPublicFallbackReason(p Profile, net *NetworkingResult) {
	switch {
	case mtlsNeeded(p):
		net.Reason += " You told us public networking is acceptable, but mTLS authentication needs an Enterprise cluster, which runs on private networking, so there is no public option here."
	case exceedsStandardLimits(p):
		net.Reason += " You told us public networking is acceptable, but your workload is above what a Standard cluster holds, and Standard is the largest public self-serve tier, so staying public is not an option at this size."
	default:
		fit := tierFit(p, TierStandard)
		if fit != "no" {
			std := TierStandard
			net.PublicFallback = &std
		}
		net.PublicFallbackCertain = fit == "yes"
		switch fit {
		case "yes":
			net.Reason += " You told us public networking is acceptable. We planned private because it is the better fit at this size, but a Standard cluster on a public endpoint will also carry this workload if staying public matters more to you. That is a straight swap you can do yourself."
		case "maybe":
			net.Reason += " You told us public networking is acceptable. We planned private because it is the better fit at this size. Staying public would mean a Standard cluster, and whether one can carry this workload depends on where you sit in the range. Give us your exact numbers and we can tell you."
		default:
			net.Reason += " You told us public networking is acceptable, but at this size private networking is the better fit, so we have planned for it. Staying public would mean a Dedicated cluster at this scale, which we plan with you rather than automatically."
		}
	}
}

// privateLinkAlternative builds the "PrivateLink also stays an option" alternative
// for a customer already on PrivateLink; over the Enterprise cap it notes the move
// to Dedicated.
func privateLinkAlternative(band int, tc string) *NetworkingResult {
	altValue := "PrivateLink"
	altReason := "You are already using PrivateLink, which can matter more than the technical differences if adopting something new needs a long change-control cycle."
	altWhy := "PrivateLink also stays as an option because you already use it."
	if band >= privateLinkCapBand {
		altValue = "PrivateLink (would move to Dedicated)"
		altReason = "PrivateLink is capped at " + itoa(privateLinkCapECKU) + " eCKU on Enterprise, so at your size it would move you to a Dedicated cluster."
		altWhy = "PrivateLink also stays as an option, though at your size it would move you to a Dedicated cluster."
	}
	return attachTradeoffs(&NetworkingResult{
		Value:  altValue,
		Reason: altReason,
		Why:    altWhy,
		Action: strptr("Set up PrivateLink"),
	}, tc)
}
