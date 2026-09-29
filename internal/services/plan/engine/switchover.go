package engine

import "strings"

// Block 5 — Switchover: Cluster Linking is the default; the cutover style comes
// from downtime tolerance. Replicator is the fallback when Cluster Linking is
// unavailable; a Serverless source bridges IAM with a jump cluster; "start
// fresh" skips data movement entirely.

// styleZeroDowntime is a sentinel, not customer copy — the zero-downtime answer
// picks between two styles further down.
const styleZeroDowntime = "gateway_or_bluegreen"

// "How it works" glossary notes, single-owned so every plan states them identically.
const (
	clHow = "Cluster Linking is Confluent Cloud's built-in mirror: it copies your topics and offsets across continuously."
	gwHow = "The Confluent Gateway is a proxy that holds client connections steady while the cluster behind it changes."
)

// styleMap keys ARE the downtime_tolerance answer vocabulary, in reading order
// (most downtime first, zero downtime last).
var styleMap = map[string]string{
	"A scheduled window, all at once":           "Cluster Linking, all at once (Restart-All-At-Once)",
	"A scheduled window, one service at a time": "Cluster Linking, one service at a time (Stop-Wait-Restart)",
	"Minutes per service":                       "Cluster Linking, service by service (Stop-Restart-Repeat)",
	"Seconds per service":                       "Cluster Linking, near-zero downtime (Stop-Restart-Repeat via Gateway)",
	"Zero downtime":                             styleZeroDowntime,
}

// The two Gateway-dependent styles, and the best style that runs without one.
var (
	styleGatewayRequired = styleMap["Seconds per service"]
	styleNoGateway       = styleMap["Minutes per service"]
)

// styleAllAtOnce is named so the cutover lead below can tell the all-at-once
// style apart from the others: "cut clients over when you are ready rather
// than all at once" is backwards when this is the style.
var styleAllAtOnce = styleMap["A scheduled window, all at once"]

// gatewayUsable — the Confluent Gateway cannot accept AWS IAM clients (and a
// Serverless source is IAM-only), so a Gateway-dependent style is unavailable to
// either.
func gatewayUsable(p Profile) bool {
	return !authHas(p, authAWSIAM) && !p.isServerless()
}

const gatewayIAMNote = "A zero-downtime or seconds-level cutover runs through the Confluent Gateway, " +
	"which needs a client authentication method it can accept, and AWS IAM is not one. Moving your " +
	"clients to SASL/SCRAM or mTLS first opens that path, and we can plan that with you."

// clCutoverSteps spells out the plain (non-Gateway) Cluster Linking cutover so every
// plain-CL reason ends with the same concrete steps.
const clCutoverSteps = "Turn on consumer.offset.sync.enable when you create the link, so your consumer " +
	"offsets come across too. At cutover, stop your producers, wait for the mirror to catch up (lag " +
	"zero), promote the mirror topics so they accept writes, then restart your clients against Confluent Cloud."

// Alternative is the "also an option" shown under a switchover verdict.
type Alternative struct {
	Value      string `json:"value"`
	Reason     string `json:"reason"`
	Standalone bool   `json:"standalone,omitempty"`
	Source     string `json:"source,omitempty"`

	// Replicator is the stable typed identity of a Replicator alternative, so the
	// citation wiring keys on it rather than matching the display Value copy.
	// Not serialized (json:"-") to keep plan.json byte-identical.
	Replicator bool `json:"-"`
}

// KCPResource links the KCP-generated migration infrastructure for a Cluster
// Linking plan.
type KCPResource struct {
	URL    string `json:"url"`
	Caveat string `json:"caveat,omitempty"`
}

// CredHandling is one source-auth-method note: how the customer's existing
// credentials are handled during data movement.
type CredHandling struct {
	Method string `json:"method"`
	Note   string `json:"note"`
}

// SwitchoverResult is the data-movement verdict.
type SwitchoverResult struct {
	Value   string `json:"value"`
	Pending bool   `json:"pending,omitempty"` // set at JSON marshal when a deciding question is unanswered; Value is blanked
	MM2     bool   `json:"mm2"`
	// Replicator and StartFresh are the stable typed identities of the two
	// non–Cluster-Linking mechanisms, so the assembler, renderer, and auth logic
	// key on them rather than matching the display Value copy. StartFresh is not
	// serialized (json:"-") to keep plan.json byte-identical; Replicator predates
	// this and stays on the JSON contract.
	Replicator      bool         `json:"replicator,omitempty"`
	StartFresh      bool         `json:"-"`
	Held            bool         `json:"held,omitempty"`
	Reason          string       `json:"reason"`
	How             string       `json:"how,omitempty"`
	Action          *string      `json:"action"`
	Pros            []string     `json:"pros,omitempty"`
	Cons            []string     `json:"cons,omitempty"`
	Fit             string       `json:"fit,omitempty"`
	Alternative     *Alternative `json:"alternative,omitempty"`
	TechAssist      bool         `json:"tech_assist,omitempty"`
	GatewayMediated bool         `json:"gateway_mediated,omitempty"`
	KCPResource     *KCPResource `json:"kcp_resource,omitempty"`

	// Attached by computePlan.
	SourceCredentialHandling []CredHandling `json:"source_credential_handling,omitempty"`
	Source                   string         `json:"source,omitempty"`
}

func replicatorPlan(p Profile, why string, mustMoveData bool) SwitchoverResult {
	// Replicator is a Confluent Platform Enterprise component. A source already on
	// Confluent Platform Enterprise already runs it, so we don't tell them to acquire
	// something they have; every other source gets the standard "get a license" line.
	// No inline "contact us" here — the data-migration Why appends a single
	// "[Talk to a person]" CTA (withTechAssistCTA), so a second one would be redundant.
	licenseLine := " It needs a license (Confluent Platform Enterprise)."
	if p.isCP() {
		licenseLine = " It needs a Confluent Platform Enterprise license: if you already run Confluent Platform Enterprise you have Replicator."
	}
	r := SwitchoverResult{
		Value: "Confluent Replicator", MM2: false, Replicator: true,
		Reason: why + " We recommend Confluent Replicator to move your existing data. You run the Connect worker yourself for the migration." + licenseLine,
		How:    "Confluent Replicator is a Confluent tool that runs on Kafka Connect and copies topics between clusters.",
		Pros: []string{
			"Works where Cluster Linking cannot reach your source",
			"Runs in your own account, using the credentials your clients already use",
		},
		Cons: []string{
			"Part of Confluent Platform Enterprise, so it needs a license",
			"You run and operate the Connect worker for the length of the migration",
			"Consumer offsets do not carry over on their own. Translating them needs the Confluent timestamp interceptor added to every consumer before you start, and it is supported on Java clients only — non-Java consumers have no automatic offset-translation path and resume per auto.offset.reset (reprocess or skip)",
		},
		TechAssist: true,
		Action:     strptr("Set up Confluent Replicator"),
	}
	// "Start fresh" (drop existing data) is only a real alternative when the customer
	// hasn't committed to moving their data. When they told us they must move it,
	// offering start-fresh contradicts that stated requirement, so we don't show it.
	if !mustMoveData {
		r.Alternative = &Alternative{
			Value:  "Start fresh",
			Reason: "Point your applications at the new cluster and let your existing cluster age out over its retention window. No replication tooling to run.",
		}
	}
	return r
}

func jumpClusterPlan(p Profile, tier Tier) SwitchoverResult {
	return SwitchoverResult{
		Value:  "Cluster Linking via a jump cluster",
		MM2:    false,
		Reason: "MSK Serverless supports only AWS IAM, and Confluent Cloud Cluster Linking cannot authenticate to an IAM source, so we bridge it with a jump cluster. Confluent's migration tool (kcp) generates the setup for the jump cluster and the link; you apply it and run the jump cluster for the length of the migration.",
		How:    "A jump cluster is a Confluent Platform cluster in your own account, with the aws-msk-iam-auth library, that reads MSK over IAM and re-exposes your topics on SASL/SCRAM. Cluster Linking then mirrors from the jump cluster byte for byte, with consumer offset sync.",
		Pros: []string{
			"Kafka-native, byte-for-byte replication with consumer offset sync",
			"KCP generates the migration infrastructure for the jump cluster and the link",
			"Fully managed on the Confluent side once the link is running",
		},
		Cons: []string{
			"You run the jump cluster in your own account for the length of the migration",
			"The jump cluster is a self-managed Confluent Platform cluster, so an additional Confluent Platform Enterprise license may be required",
		},
		Alternative: &Alternative{
			Value:      "Confluent Replicator",
			Reason:     "If you would rather not run a jump cluster, Confluent Replicator runs on Kafka Connect in your own account and reads MSK over IAM directly. Since it also runs on Confluent Platform, an additional Confluent Platform Enterprise license may be required, and consumer offsets do not carry over on their own.",
			Standalone: true,
			Replicator: true,
		},
		TechAssist:  true,
		Action:      strptr("Create cluster link"),
		KCPResource: kcpResource(p, tier),
	}
}

func startFreshPlan(p Profile, tier Tier) SwitchoverResult {
	tierName := "new"
	if tier != "" {
		tierName = string(tier)
	}
	return SwitchoverResult{
		Value:      "Start fresh",
		StartFresh: true,
		MM2:        false,
		Reason: basis(ans("no data migration")) + "you don't need to carry existing data across, so this is the simplest path. Create the " +
			tierName + " cluster, point your producers and consumers at it, and let the old cluster age out over its retention window. " +
			"No mirroring, no replication tooling, no cutover window. If you later need some history, say so and we will plan a mirrored cutover instead.",
		Alternative: &Alternative{Value: "Mirror your data across first", Reason: startFreshAlternative(p, tier)},
		Action:      nil,
	}
}

// startFreshAlternative names whichever mechanism the customer would actually
// get if they flipped needs_data_migration to Yes, built from resolveMechanism
// with the same profile and tier rather than guessed from isServerless alone —
// a Standard destination, a below-Cluster-Linking-floor source, or an
// Enterprise-destination Serverless source (which resolves to a jump cluster,
// neither Cluster Linking nor Replicator) all name the wrong mechanism if
// guessed. Forcing needsDataAnswer to "Yes" means this can never drift from
// what switchoverDecision would actually plan if the answer changed.
func startFreshAlternative(p Profile, tier Tier) string {
	m := resolveMechanism(p, tier, "Yes")
	// Flipping to Yes would fire the onprem_private_cluster_link handoff, so promise
	// a designed private link rather than a self-serve mechanism.
	if p.isOSKorCP() && p.SourceCloud == "On-prem or other" && willBePrivate(p) && mechanismUsesClusterLink(p, tier, "Yes") {
		return "If you need your existing messages on the new cluster, we'd design the private link with you, since your brokers run outside a cloud network. Change your answer above and we will plan that instead."
	}
	lead := "If you need your existing messages on the new cluster, we would use "
	tail := " Change your answer above and we will plan that instead."
	if m.Mechanism == "jump-cluster" {
		return lead + "Cluster Linking through a temporary jump cluster to mirror topics and offsets " +
			"continuously, then cut clients over once they have caught up. The jump cluster is there " +
			"because AWS IAM credentials cannot cross a cluster link directly." + tail
	}
	if m.Mechanism == "replicator" {
		var why string
		switch m.Why {
		case "gov":
			why = "Confluent Cloud for Government doesn't offer fully managed Cluster Linking."
		case "serverless-tier":
			why = "Cluster Linking is not an option from MSK Serverless, which only supports AWS IAM authentication."
		case "tier":
			why = "Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a " + string(tier) + " cluster."
		case "ibp":
			why = "Your inter-broker protocol is below 2.8, so Cluster Linking is not available even though your Kafka version qualifies."
		default:
			why = "Your source is below the Cluster Linking floor: Kafka 2.4, Confluent Platform 5.4, inter-broker protocol (IBP) 2.8."
		}
		return lead + "Confluent Replicator to copy them across, then cut clients over. " + why + tail
	}
	return lead + "Cluster Linking to mirror topics and offsets continuously, then cut clients over once they have caught up." + tail
}

// kcpResource links the KCP migration infrastructure where an MSK source
// migrates over Cluster Linking, with a caveat when the source lacks SASL/SCRAM
// (the link signs in over SASL/SCRAM).
func kcpResource(p Profile, tier Tier) *KCPResource {
	if tier == "" || !clusterLinkingAvailable(tier) {
		return nil
	}
	r := &KCPResource{URL: "https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migration-infra/"}
	if !authHas(p, authSCRAM) {
		onlyIf := "if your source is mTLS-only"
		if !p.isMSK() {
			onlyIf = "if your source is mTLS-, SASL/PLAIN- or Kerberos-only"
		}
		r.Caveat = "The link kcp generates signs in over SASL/SCRAM, so " + onlyIf + ", add a SASL/SCRAM listener and a SCRAM user for the link. Your clients keep their current authentication on the source."
	}
	return r
}

// eosCaveat is appended to every Cluster Linking plan; empty when the customer
// declared no exactly-once / Kafka Streams state.
func eosCaveat(p Profile) string {
	if len(p.EosStreams) == 0 {
		return ""
	}
	return " One caveat: Cluster Linking does not carry " + joinComma(p.EosStreams) + ". Handle that at cutover."
}

func switchoverDecision(p Profile, sizing SizingResult, tier Tier) SwitchoverResult {
	m := resolveMechanism(p, tier, "")

	if m.Mechanism == "start-fresh" {
		return startFreshPlan(p, tier)
	}

	if m.Mechanism == "jump-cluster" {
		jump := jumpClusterPlan(p, tier)
		jump.Reason += eosCaveat(p)
		return jump
	}

	if m.Mechanism == "replicator" {
		mustMoveData := p.NeedsDataMigration == "Yes"
		switch m.Why {
		case "gov":
			return replicatorPlan(p, "Confluent Cloud for Government doesn't offer fully managed Cluster Linking, so we move your existing data with Confluent Replicator instead.", mustMoveData)
		case "serverless-tier":
			t := "Standard"
			if tier != "" {
				t = string(tier)
			}
			return replicatorPlan(p, basis(srcOr(p.SourceClusterTypeAnswered, "source is Serverless"))+"Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a "+t+" cluster.", mustMoveData)
		case "tier":
			return replicatorPlan(p, "Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a "+string(tier)+" cluster.", mustMoveData)
		case "ibp":
			return replicatorPlan(p, basis(srcOr(p.IBPAnswered, "inter-broker protocol below 2.8"))+"Cluster Linking is not available even though your Kafka version qualifies.", mustMoveData)
		default:
			kafkaFinding := "source below the Cluster Linking floor"
			if p.KafkaVersion != "" {
				kafkaFinding = "Kafka " + p.KafkaVersion
			}
			return replicatorPlan(p, basis(srcOr(p.KafkaVersionAnswered, kafkaFinding))+"your source is below the Cluster Linking floor: Kafka 2.4, Confluent Platform 5.4, inter-broker protocol (IBP) 2.8.", mustMoveData)
		}
	}

	// m.Mechanism == "cluster-linking": style logic only from here.

	// downtime_tolerance is required, with no default. Held open until answered.
	if _, ok := styleMap[p.DowntimeTolerance]; p.DowntimeTolerance == "" || !ok {
		return SwitchoverResult{
			Value: "Pending", MM2: false, Held: true,
			Reason: "Tell us how much downtime you can tolerate and we'll recommend your cutover style. This is required, with no default.",
			Action: strptr("Answer downtime tolerance"),
		}
	}

	mapped := styleMap[p.DowntimeTolerance]
	techAssist, gatewayMediated := false, false
	action := "Create cluster link"
	gatewayLicenseNeeded := func(baseStyle string) string {
		techAssist, gatewayMediated = true, true
		action = "Set up the Confluent Gateway"
		return baseStyle + ": Gateway license required"
	}

	// An IAM source cannot use the Gateway, so neither Gateway-dependent style is
	// available. Fall back to the best non-Gateway style.
	if !gatewayUsable(p) && (mapped == styleZeroDowntime || mapped == styleGatewayRequired) {
		return SwitchoverResult{
			Value: styleNoGateway,
			Reason: "You asked for a cutover faster than we can run on AWS IAM. " + gatewayIAMNote +
				" So we recommend a Cluster Linking cutover, so you cut clients over when you are ready " +
				"rather than all at once. " + clCutoverSteps,
			How:         clHow,
			Action:      strptr("Create cluster link"),
			MM2:         false,
			KCPResource: kcpResource(p, tier),
		}
	}

	var style, reason, how string
	switch mapped {
	case styleZeroDowntime:
		gatewayMediated = true
		style = gatewayLicenseNeeded("Gateway cutover (no downtime)")
		reason = "Zero-downtime cutover needs the Confluent Gateway. The Gateway needs a Confluent Cloud Gateway license and Confluent for Kubernetes, the operator that runs it in your cluster. Talk to us and we will work out what that takes for you."
		how = gwHow
	case styleGatewayRequired:
		gatewayMediated = true
		style = gatewayLicenseNeeded(mapped)
		reason = "A seconds-per-service Stop-Restart-Repeat cutover runs through the Confluent Gateway. The Gateway needs a Confluent Cloud Gateway license and Confluent for Kubernetes, the operator that runs it in your cluster. Talk to us and we will work out what that takes for you."
		how = gwHow
	default:
		style = mapped
		blastRadiusHigh := sizing.Band >= privateLinkCapBand
		hardCoordination := p.ClientCoordinationBurden == "Hard (many clients and teams)"
		switch {
		case (blastRadiusHigh || hardCoordination) && !gatewayUsable(p):
			// The lead says "rather than all at once", which is backwards when the style
			// IS the all-at-once style: mapped can still be that style here even after
			// the escalation check above (style stays `mapped`, unchanged).
			if mapped == styleAllAtOnce {
				reason = "So we recommend a Cluster Linking cutover, moving all your clients over together in one scheduled window. " +
					gatewayIAMNote + " " + clCutoverSteps
			} else {
				reason = "We recommend a Cluster Linking cutover, so you " +
					"cut clients over when you are ready rather than all at once. " + gatewayIAMNote +
					" " + clCutoverSteps
			}
			how = clHow
		case blastRadiusHigh || hardCoordination:
			gatewayMediated = true
			var whyParts []string
			if hardCoordination {
				whyParts = append(whyParts, "your client-coordination burden is hard")
			}
			if blastRadiusHigh {
				whyParts = append(whyParts, "your blast radius is high")
			}
			style = gatewayLicenseNeeded(mapped + ", Gateway-mediated")
			reason = "We recommend a Cluster Linking cutover, moved to a Gateway-mediated approach because " + joinAnd(whyParts) + ". The Gateway needs a Confluent Cloud Gateway license and Confluent for Kubernetes. Talk to us and we will work out what that takes for you."
			how = clHow
		default:
			// Same backwards-lead fix as the escalation branch above: style is still
			// `mapped` here.
			if mapped == styleAllAtOnce {
				reason = "We recommend a Cluster Linking cutover, moving all your clients over together in one scheduled window. " + clCutoverSteps
			} else {
				reason = "We recommend a Cluster Linking cutover, so you cut clients over when you are ready " +
					"rather than all at once. " + clCutoverSteps
			}
			how = clHow
		}
	}

	// The cutover style follows the downtime window the customer gave us; a high
	// blast radius (scanned sizing band) escalates it, noted inline where it applies.
	reason = basis(ans(strings.ToLower(p.DowntimeTolerance))) + lowerFirst(reason)
	reason += eosCaveat(p)

	return SwitchoverResult{
		Value: style, MM2: false, GatewayMediated: gatewayMediated, TechAssist: techAssist,
		Reason: reason, How: how, Action: &action, KCPResource: kcpResource(p, tier),
	}
}
