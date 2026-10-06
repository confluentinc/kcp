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
	clHow = "Cluster Linking is Confluent Cloud's built-in mirror: it copies your topics across continuously, and consumer offsets too once you enable consumer offset sync on the link and list the consumer groups to migrate."
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

// gatewayAuthOK — the Gateway swaps one mechanism per route, passes SASL through
// only, and signs in to the cluster with PLAIN, OAUTHBEARER or NONE (no GSSAPI). So
// with a source that has methods, it is offered only when there is exactly one and
// it is SASL/SCRAM, SASL/PLAIN or mTLS. A source with no methods answered, or
// plaintext only, keeps the path it always had.
func gatewayAuthOK(p Profile) bool {
	m := sourceAuthMethods(p)
	if len(m) == 0 {
		return true
	}
	if len(m) != 1 {
		return false
	}
	switch m[0] {
	case authSCRAM, authSASLPlain, authMTLS, authUnauth:
		return true
	}
	return false
}

// gatewayUsable — the Confluent Gateway cannot accept AWS IAM clients (and a
// Serverless source is IAM-only), it needs every client on one supported
// sign-in method, and it isn't safe for Kafka Streams applications, so a
// Gateway-dependent style is unavailable otherwise.
func gatewayUsable(p Profile) bool {
	return gatewayAuthSupported(p) && !usesKafkaStreams(p)
}

// usesKafkaStreams reports whether the customer selected the Kafka Streams option. Its
// local state can't follow a Gateway switchover; exactly-once alone doesn't gate it.
func usesKafkaStreams(p Profile) bool {
	for _, v := range p.EosStreams {
		if v == "Kafka Streams" {
			return true
		}
	}
	return false
}

// gatewayAuthSupported reports whether the clients' sign-in methods allow the Gateway.
func gatewayAuthSupported(p Profile) bool {
	return !authHas(p, authAWSIAM) && !p.isServerless() && gatewayAuthOK(p)
}

// gatewayMethodNames is how the fallback sentence names each source auth method.
var gatewayMethodNames = map[string]string{
	authSCRAM:     "SASL/SCRAM",
	authSASLPlain: "SASL/PLAIN",
	authMTLS:      "mTLS",
	authKerberos:  "Kerberos (GSSAPI)",
	authAWSIAM:    "AWS IAM",
	authUnauth:    "no authentication",
}

func gatewayMethodList(p Profile) string {
	var n []string
	for _, m := range sourceAuthMethods(p) {
		if name, ok := gatewayMethodNames[m]; ok {
			n = append(n, name)
		} else {
			n = append(n, m)
		}
	}
	if len(n) <= 2 {
		return strings.Join(n, " and ")
	}
	return strings.Join(n[:len(n)-1], ", ") + ", and " + n[len(n)-1]
}

// gatewayFallbackNote says why the Gateway path is not planned. IAM and Serverless
// keep their own note; any other source gets the one-sign-in-method sentence.
func gatewayFallbackNote(p Profile, style string) string {
	if authHas(p, authAWSIAM) || p.isServerless() {
		return gatewayIAMNote
	}
	if gatewayAuthSupported(p) {
		return "Confluent Cloud Gateway client switchover isn't safe for Kafka Streams applications, so this plan uses a Cluster Linking cutover without the Gateway instead." + gatewayFallbackClose(style)
	}
	return "Zero downtime through Confluent Cloud Gateway, as this plan sets it up with one route, needs every client on one sign-in method the Gateway supports (SASL/SCRAM, SASL/PLAIN, mTLS, or no authentication). Your clients use " + gatewayMethodList(p) + ", so this plan uses a Cluster Linking cutover without the Gateway instead." + gatewayFallbackClose(style)
}

// gatewayFallbackRecommend opens the cutover sentence after the fallback note. The auth and
// Kafka Streams notes already say the plan uses a Cluster Linking cutover, so only the IAM note,
// which doesn't, keeps the recommendation.
func gatewayFallbackRecommend(p Profile) string {
	if authHas(p, authAWSIAM) || p.isServerless() {
		return " We recommend a Cluster Linking cutover, so you cut"
	}
	return " You cut"
}

// gatewayFallbackClose ends the fallback sentence to suit the cutover style. The
// minutes-per-service steps already say how long each service is down.
func gatewayFallbackClose(style string) string {
	switch style {
	case styleAllAtOnce:
		return " Clients pause briefly while you cut them over together."
	case styleNoGateway:
		return ""
	}
	return " Each service pauses briefly at its cutover."
}

// gatewayFallbackLead opens the IAM fallback by naming what the customer asked for.
func gatewayFallbackLead(p Profile) string {
	if authHas(p, authAWSIAM) || p.isServerless() {
		return "You asked for a cutover faster than we can run on AWS IAM. "
	}
	return ""
}

const gatewayIAMNote = "A zero-downtime or seconds-level cutover runs through the Confluent Gateway, " +
	"which needs a client authentication method it can accept, and AWS IAM is not one. Moving your " +
	"clients to SASL/SCRAM or mTLS first opens that path, and we can plan that with you."

// clOffsetSync is the offset-sync instruction for a plain Cluster Linking cutover. The
// generated link doesn't enable it, so the customer does before cutover.
const clOffsetSync = "Before you cut over, enable consumer offset sync on the cluster link: set consumer.offset.sync.enable=true and list the consumer groups to migrate in consumer.offset.group.filters (it defaults to none), so consumer offsets come across too."

// serviceByServiceSteps is the cutover sequence for the two one-service-at-a-time styles.
// Consumer groups move first, then the mirror topics are promoted once nothing on the
// source still uses them. A jump-cluster plan syncs offsets across two links, so one
// interval is not enough: it waits at least two, checks the groups' offsets on Confluent
// Cloud, then drops them from the Confluent Cloud link's filter. A single-link plan keeps
// one interval.
func serviceByServiceSteps(jump bool) string {
	move := "stop them on the source, wait one consumer offset sync interval, remove the groups from consumer.offset.group.filters, and restart the consumers against Confluent Cloud. "
	if jump {
		move = "stop them on the source, wait at least two consumer offset sync intervals (offsets cross both links), check the groups' offsets on Confluent Cloud, remove the groups from consumer.offset.group.filters on the Confluent Cloud link, and restart the consumers against Confluent Cloud. "
	}
	return "Cut over one service at a time. For each service, move its consumer groups first: " + move +
		"Promote a mirror topic only after every producer and consumer that uses it on the source has stopped, then restart its producers against Confluent Cloud."
}

// clCutoverSteps spells out the plain (non-Gateway) Cluster Linking cutover so every
// plain-CL reason ends with the same concrete steps. The stop-and-restart steps follow
// the style the customer picked: an all-at-once label with service-by-service steps, or
// the reverse, would read as two different plans.
func clCutoverSteps(style string, jump bool) string {
	return clOffsetSync + " " + cutoverStepsText(style, jump)
}

// cutoverStepsText is the stop, wait, promote and restart sequence for the picked style.
func cutoverStepsText(style string, jump bool) string {
	var steps string
	switch style {
	case styleMap["A scheduled window, one service at a time"]:
		steps = serviceByServiceSteps(jump)
	case styleMap["Minutes per service"]:
		steps = serviceByServiceSteps(jump) + " Each service is down only for the minutes this takes."
	default:
		wait := "one consumer offset sync interval"
		if jump {
			wait = "two consumer offset sync intervals (offsets cross both links)"
		}
		steps = "At cutover, stop your producers and consumers, wait for the mirror to catch up (lag zero) and " + wait + ", promote the mirror topics so they accept writes, then restart your clients against Confluent Cloud."
	}
	return steps
}

// jumpOffsetSync is the offset-sync instruction for a jump-cluster plan, whose data
// crosses two cluster links (MSK to the jump cluster, then the jump cluster to
// Confluent Cloud).
const jumpOffsetSync = "Enable consumer offset sync on both cluster links (from your MSK cluster to the jump cluster, and from the jump cluster to Confluent Cloud): set consumer.offset.sync.enable=true and list the consumer groups to migrate in consumer.offset.group.filters on each, so consumer offsets come across."

// OffsetSyncFiltersExample is the shape of the consumer.offset.group.filters value, which
// is JSON, so the plan's example commands pass both offset-sync properties from a file.
const OffsetSyncFiltersExample = `{"groupFilters":[{"name":"<consumer-group>","patternType":"LITERAL","filterType":"INCLUDE"}]}`

// ClCutoverStepsMarkdown is the plan.md form of clCutoverSteps: it names the commands
// that turn on offset sync, with their docs links. A jump-cluster plan has two links, so
// it gets one command per hop (firstHop is the MSK-to-jump link, syncCmd the link to
// Confluent Cloud). Both commands read the two offset-sync properties from a file.
func ClCutoverStepsMarkdown(style string, jump bool, syncCmd, syncDoc, firstHopCmd, firstHopDoc string) string {
	steps := clCutoverSteps(style, jump)
	props := "In the properties file, set `consumer.offset.sync.enable=true` and `consumer.offset.group.filters=" + OffsetSyncFiltersExample + "`."
	if jump {
		return strings.Replace(steps, clOffsetSync, jumpOffsetSync+" "+props+" On the jump cluster's link from MSK, run `"+firstHopCmd+"` ([docs]("+firstHopDoc+")). On its link to Confluent Cloud, run `"+syncCmd+"` ([docs]("+syncDoc+")).", 1)
	}
	return strings.Replace(steps, clOffsetSync, clOffsetSync+" "+props+" Then run `"+syncCmd+"` ([docs]("+syncDoc+")).", 1)
}

// IsAllAtOnceStyle reports whether a switchover CutoverStyle is the all-at-once style.
func IsAllAtOnceStyle(style string) bool { return style == styleAllAtOnce }

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
	// CutoverStyle is the Cluster Linking cutover style (a styleMap value) the steps
	// follow. Not serialized, so plan.json stays byte-identical.
	CutoverStyle string       `json:"-"`
	KCPResource  *KCPResource `json:"kcp_resource,omitempty"`

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
		Reason: why + " We recommend Confluent Replicator to move your existing data. You run the Connect worker yourself for the migration." + licenseLine +
			" Consumer offsets do not carry over on their own: translating them needs the Confluent timestamp interceptor added to every consumer before you start, and the interceptor works for Java clients only.",
		How: "Confluent Replicator is a Confluent tool that runs on Kafka Connect and copies topics between clusters.",
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
			Reason: startFreshCutover + " No replication tooling to run.",
		}
	}
	return r
}

// jumpReasonLead explains why the plan runs a jump cluster: Serverless is IAM-only;
// a Provisioned IAM source gets one because it changes nothing on production.
func jumpReasonLead(p Profile) string {
	if p.isServerless() {
		return "MSK Serverless supports only AWS IAM, and Confluent Cloud Cluster Linking cannot authenticate to an IAM source, so we bridge it with a jump cluster."
	}
	return basis(srcOr(p.AuthAnswered, "AWS IAM authentication")) + "your MSK cluster uses AWS IAM, and Confluent Cloud Cluster Linking cannot authenticate to an IAM source, so we recommend a jump cluster: a temporary Kafka cluster in the middle that reaches your MSK cluster with IAM, so nothing changes on your production cluster."
}

// jumpCutover picks the cutover style for a jump-cluster plan from the downtime
// answer, which the plan's cutover steps follow. The jump path reads an IAM source,
// which the Gateway cannot accept, so a Gateway-dependent answer falls back to the best
// non-Gateway style and the reason says why. An unanswered question yields no style.
func jumpCutover(p Profile) (style, reason string) {
	mapped, ok := styleMap[p.DowntimeTolerance]
	if !ok {
		return "", ""
	}
	if mapped == styleZeroDowntime || mapped == styleGatewayRequired {
		return styleNoGateway, " You asked for a cutover faster than we can run on AWS IAM. " + gatewayIAMNote
	}
	return mapped, ""
}

func jumpClusterPlan(p Profile, tier Tier) SwitchoverResult {
	cutoverStyle, cutoverReason := jumpCutover(p)
	return SwitchoverResult{
		Value:        "Cluster Linking via a jump cluster",
		MM2:          false,
		CutoverStyle: cutoverStyle,
		Reason:       jumpReasonLead(p) + " Confluent's migration tool (kcp) generates the setup for the jump cluster and both cluster links; you apply it and run the jump cluster for the length of the migration. " + jumpOffsetSync + cutoverReason,
		How:          "A jump cluster is a temporary Kafka cluster in your AWS account. It mirrors your MSK topics over AWS IAM, then links out to Confluent Cloud through a PrivateLink VPC endpoint, so nothing changes on your production cluster.",
		Pros: []string{
			"Kafka-native, byte-for-byte replication, with consumer offset sync available on each link",
			"kcp generates the migration infrastructure for the jump cluster and both cluster links",
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
		Action:      strptr("Set up a PrivateLink endpoint for the jump cluster"),
		KCPResource: kcpResource(p, tier),
	}
}

// startFreshTopics is said once, so every start-fresh path (the plan, the Replicator
// alternative, and the Topics verdict) agrees.
const startFreshTopics = "Create your topics on the new cluster before you move producers, because automatic topic creation is off by default on Confluent Cloud."

// startFreshCutover is the start-fresh cutover sequence: producers move first so nothing
// new lands on the old cluster, consumers drain it, then start from the earliest offset.
const startFreshCutover = startFreshTopics + " Move your producers to the new cluster first, let your consumers finish reading the old cluster (lag zero), then move them, starting from the earliest offset. The old cluster then ages out over its retention window."

func startFreshPlan(p Profile, tier Tier) SwitchoverResult {
	tierName := "new"
	if tier != "" {
		tierName = string(tier)
	}
	return SwitchoverResult{
		Value:      "Start fresh",
		StartFresh: true,
		MM2:        false,
		Reason: basis(ans("no data migration")) + "you don't need to carry existing data across, so this is the simplest path: create the " +
			tierName + " cluster. " + startFreshCutover + " " +
			"No mirroring and no replication tooling to run. If you later need some history, say so and we will plan a mirrored cutover instead.",
		Alternative: &Alternative{Value: "Mirror your data across first", Reason: startFreshAlternative(p, tier)},
		Action:      nil,
	}
}

// serverlessTierName is the destination tier named in the "serverless-tier"
// Replicator copy: the resolved tier, or Standard when none is known yet.
func serverlessTierName(tier Tier) string {
	if tier == "" {
		return "Standard"
	}
	return string(tier)
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
			why = "Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a " + serverlessTierName(tier) + " cluster."
		case "tier":
			why = "Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a " + string(tier) + " cluster."
		case "ibp":
			why = "Your inter-broker protocol is below 2.8, so Cluster Linking is not available even though your Kafka version qualifies."
		default:
			why = "Your source is below the Cluster Linking floor: " + ClusterLinkingFloor(p.SourceType) + "."
		}
		return lead + "Confluent Replicator to copy them across, then cut clients over. " + why + tail
	}
	return lead + "Cluster Linking to mirror topics and offsets continuously, then cut clients over once they have caught up." + tail
}

// kcpGeneratesLink reports whether kcp's migration-infra can build the link. It
// builds AWS infrastructure only, so a source outside AWS or a non-AWS target is out
// of reach. An unanswered source cloud is not treated as non-AWS.
func kcpGeneratesLink(p Profile) bool {
	sourceNonAWS := !p.isMSK() && (p.SourceCloud == "Azure" || p.SourceCloud == "GCP" || p.SourceCloud == "On-prem or other")
	tc := targetCloud(p)
	return !sourceNonAWS && (tc == "" || tc == "AWS")
}

// kcpResource links the KCP migration infrastructure where an MSK source
// migrates over Cluster Linking, with a caveat when the source lacks SASL/SCRAM
// (the link signs in over SASL/SCRAM).
func kcpResource(p Profile, tier Tier) *KCPResource {
	if tier == "" || !clusterLinkingAvailable(tier) || !kcpGeneratesLink(p) {
		return nil
	}
	r := &KCPResource{URL: "https://confluentinc.github.io/kcp/latest/command-reference/create-asset/migration-infra/"}
	// The caveat applies to a source whose existing method the link can't use (mTLS,
	// SASL/PLAIN, or Kerberos) with no SASL/SCRAM, and not when the link runs through a
	// jump cluster (the migration-infra decision) instead of a listener on the source.
	linkUnusable := authHas(p, authMTLS) || authHas(p, authSASLPlain) || authHas(p, authKerberos)
	if linkUnusable && !authHas(p, authSCRAM) && !MigrationInfraDecision(p, tier).JumpCluster {
		onlyIf := "if your source is mTLS-only"
		if !p.isMSK() {
			onlyIf = "if your source is mTLS-, SASL/PLAIN-, or Kerberos-only"
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
		jump.Reason += " " + cutoverStepsText(jump.CutoverStyle, true) + eosCaveat(p)
		return jump
	}

	if m.Mechanism == "replicator" {
		mustMoveData := p.NeedsDataMigration == "Yes"
		switch m.Why {
		case "gov":
			return replicatorPlan(p, "Confluent Cloud for Government doesn't offer fully managed Cluster Linking, so we move your existing data with Confluent Replicator instead.", mustMoveData)
		case "serverless-tier":
			return replicatorPlan(p, basis(srcOr(p.SourceClusterTypeAnswered, "source is Serverless"))+"Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a "+serverlessTierName(tier)+" cluster.", mustMoveData)
		case "tier":
			return replicatorPlan(p, "Cluster Linking needs an Enterprise or Dedicated destination, so it is not available into a "+string(tier)+" cluster.", mustMoveData)
		case "ibp":
			return replicatorPlan(p, basis(srcOr(p.IBPAnswered, "inter-broker protocol below 2.8"))+"Cluster Linking is not available even though your Kafka version qualifies.", mustMoveData)
		default:
			kafkaFinding := "source below the Cluster Linking floor"
			if p.KafkaVersion != "" {
				kafkaFinding = "Kafka " + p.KafkaVersion
			}
			return replicatorPlan(p, basis(srcOr(p.KafkaVersionAnswered, kafkaFinding))+"your source is below the Cluster Linking floor: "+ClusterLinkingFloor(p.SourceType)+".", mustMoveData)
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
			Reason: gatewayFallbackLead(p) + gatewayFallbackNote(p, styleNoGateway) +
				gatewayFallbackRecommend(p) + " clients over when you are ready " +
				"rather than all at once. " + clCutoverSteps(styleNoGateway, false) + eosCaveat(p),
			How:          clHow,
			Action:       strptr("Create cluster link"),
			MM2:          false,
			CutoverStyle: styleNoGateway,
			KCPResource:  kcpResource(p, tier),
		}
	}

	var style, reason, how string
	// The "Based on …" lead credits the downtime answer, except where a Gateway-mediated
	// escalation is driven by something else (set below).
	leadDrivers := []driver{ans(strings.ToLower(p.DowntimeTolerance))}
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
				reason = "We recommend a Cluster Linking cutover, moving all your clients over together in one scheduled window. " +
					gatewayFallbackNote(p, mapped) + " " + clCutoverSteps(mapped, false)
			} else {
				reason = "We recommend a Cluster Linking cutover, so you " +
					"cut clients over when you are ready rather than all at once. " + gatewayFallbackNote(p, mapped) +
					" " + clCutoverSteps(mapped, false)
			}
			how = clHow
		case blastRadiusHigh || hardCoordination:
			gatewayMediated = true
			var whyParts []string
			leadDrivers = nil
			if hardCoordination {
				whyParts = append(whyParts, "your client-coordination burden is hard")
				leadDrivers = append(leadDrivers, ans("client coordination"))
			}
			if blastRadiusHigh {
				whyParts = append(whyParts, "your blast radius is high")
				leadDrivers = append(leadDrivers, srcOr(p.PartitionsAnswered, "partition count"))
			}
			style = gatewayLicenseNeeded(mapped + ", Gateway-mediated")
			reason = "We recommend a Cluster Linking cutover, moved to a Gateway-mediated approach because " + joinAnd(whyParts) + ". The Gateway needs a Confluent Cloud Gateway license and Confluent for Kubernetes. Talk to us and we will work out what that takes for you."
			how = clHow
		default:
			// Same backwards-lead fix as the escalation branch above: style is still
			// `mapped` here.
			if mapped == styleAllAtOnce {
				reason = "We recommend a Cluster Linking cutover, moving all your clients over together in one scheduled window. " + clCutoverSteps(mapped, false)
			} else {
				reason = "We recommend a Cluster Linking cutover, so you cut clients over when you are ready " +
					"rather than all at once. " + clCutoverSteps(mapped, false)
			}
			how = clHow
		}
	}

	// The cutover style follows the downtime window the customer gave us; a high
	// blast radius (scanned sizing band) escalates it, noted inline where it applies.
	reason = basis(leadDrivers...) + lowerFirst(reason)
	reason += eosCaveat(p)

	return SwitchoverResult{
		Value: style, CutoverStyle: mapped, MM2: false, GatewayMediated: gatewayMediated, TechAssist: techAssist,
		Reason: reason, How: how, Action: &action, KCPResource: kcpResource(p, tier),
	}
}
