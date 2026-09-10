package plan

import (
	"strings"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
)

// The intake question catalog. Each question maps a stable, short plan-inputs
// TOKEN (or boolean) onto the engine's internal string, decoupled from the
// customer-facing wording so a copy change never breaks a customer's
// plan-inputs.yaml. Prompts, option labels, hints and per-option details use the
// exact customer-facing intake wording; the token is the stable key and the
// engine string is internal (and may change across versions).
//
// Scan-derived facts (cluster type, Kafka version, source auth, partitions,
// tiered storage, connectors) are NOT questions — buildProfile fills them.

type disposition int

const (
	dispRequired disposition = iota
	dispOptional
)

// opt is one selectable option: its stable token, full wording, engine value, and
// an optional per-option detail line.
type opt struct {
	Token  string
	Label  string
	Engine string
	Detail string
}

type question struct {
	Key     string // plan-inputs.yaml key (also the stable API)
	Prompt  string
	Hint    string // one-line hint shown under the prompt
	Disp    disposition
	Multi   bool // value is a list of tokens
	Opts    []opt
	Default string                      // default token (optional questions)
	Applies func(p engine.Profile) bool // conditional visibility; nil = always
	// set applies the resolved engine value(s) for this question onto IntakeInputs.
	set func(in *IntakeInputs, engineVals []string)
	// Scan marks a question kcp answers from the scan; Current reads the effective
	// value (scan, or the customer's override) off the built profile so it can be
	// shown pre-selected and edited. overridden reports whether the customer
	// supplied an override for a scan fact (so it's rendered live, not commented).
	// requiredWhenMissing surfaces a scan fact as a required question when the scan
	// didn't detect it (the plan can't be completed without it).
	Scan                bool
	Current             func(p engine.Profile) []string
	overridden          func(in IntakeInputs) bool
	requiredWhenMissing bool
	// readOnly marks a scan fact the scan is authoritative on: it's shown for audit
	// but not advertised as an override in plan-inputs.yaml (the key still works if
	// hand-added). Only meaningful with Scan.
	readOnly bool
}

// yn builds a boolean question's true/false options.
func yn(trueLabel, falseLabel, engTrue, engFalse string) []opt {
	return []opt{
		{Token: "true", Label: trueLabel, Engine: engTrue},
		{Token: "false", Label: falseLabel, Engine: engFalse},
	}
}

func connectorsPresent(p engine.Profile) bool {
	return deref(p.MSKConnectPresent) == "Yes" || deref(p.SelfManagedConnectors) == "Yes"
}

// migratingData is true unless the customer explicitly chose to start fresh (no
// migration cluster link, so nothing to backfill).
func migratingData(p engine.Profile) bool { return p.NeedsDataMigration != "No" }

// clusterLinkingPath reports whether this cluster's plan could use Cluster Linking:
// data is moving to a private (Enterprise/Dedicated, size-forced included) and
// non-Serverless destination. Questions that only shape the Cluster Linking path
// (the cutover-style downtime tolerance, the IBP floor) gate on this so they don't
// block a Replicator/Standard plan whose outcome they can't change.
func clusterLinkingPath(p engine.Profile) bool {
	return migratingData(p) && engine.WillBePrivate(p) && !engine.IsServerless(p)
}

func first(vals []string) string {
	if len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// catalog is the ordered question set (reading order).
var catalog = []question{
	// ── Required ──────────────────────────────────────────────────────────────
	{Key: "use_case_breadth", Prompt: "How widely is this cluster used?", Disp: dispRequired,
		Opts: []opt{
			{Token: "one-app", Label: "One team, one application", Engine: "One team, one application"},
			{Token: "few-teams", Label: "A few teams or applications", Engine: "A few teams or applications"},
			{Token: "shared-fabric", Label: "Shared fabric across many apps and teams", Engine: "Shared fabric across many apps and teams"},
		},
		set: func(in *IntakeInputs, v []string) { in.UseCaseBreadth = first(v) }},

	{Key: "move_existing_data", Prompt: "Do you need to move your existing data?", Disp: dispRequired,
		Opts: []opt{
			{Token: "true", Label: "Yes, move my existing data", Engine: "Yes", Detail: "Brings your history along. Availability depends on your source setup."},
			{Token: "false", Label: "No, we can start fresh", Engine: "No", Detail: "Points producers and consumers at the new cluster and lets the old one retire"},
		},
		set: func(in *IntakeInputs, v []string) { in.NeedsDataMigration = first(v) }},

	{Key: "private_networking_required", Prompt: "Is private networking a hard requirement?", Disp: dispRequired,
		Hint: "Answer based on your requirement, not your current setup. Public endpoints on Confluent Cloud are authenticated and encrypted.",
		// engTrue → public_endpoints_ok "No" (private required); engFalse → "Yes".
		Opts: yn("Yes, private networking is required", "No, public endpoints are fine", "No", "Yes"),
		set:  func(in *IntakeInputs, v []string) { in.PublicEndpointsOK = first(v) }},

	{Key: "connects_today", Prompt: "How do you connect to your cluster today?", Disp: dispRequired,
		Opts: []opt{
			{Token: "same-vpc", Label: "Same VPC", Engine: "Same VPC"},
			{Token: "peered", Label: "Peered", Engine: "Peered"},
			{Token: "privatelink", Label: "PrivateLink", Engine: "PrivateLink"},
			{Token: "other", Label: "Other or more than one", Engine: "Other"},
		},
		Applies: func(p engine.Profile) bool { return engine.WillBePrivate(p) && engine.TargetCloudOf(p) == "AWS" },
		set:     func(in *IntakeInputs, v []string) { in.ConnectsToday = first(v) }},

	{Key: "cc_egress_required", Prompt: "Will any of Confluent Cloud's managed connectors or consumers need to connect into your private network?", Disp: dispRequired,
		Hint:    "For example, a connector writing to a private database, or a managed Confluent component calling a private API inside your network.",
		Opts:    yn("Yes", "No", "Yes", "No"),
		Applies: func(p engine.Profile) bool { return engine.WillBePrivate(p) },
		set:     func(in *IntakeInputs, v []string) { in.CCEgressRequired = first(v) }},

	{Key: "schema_registry", Prompt: "What Schema Registry does your source environment use?", Disp: dispRequired,
		Opts: []opt{
			{Token: "none", Label: "None", Engine: "None", Detail: "Schemaless, or schemas kept in app code"},
			{Token: "glue", Label: "AWS Glue Schema Registry", Engine: "AWS Glue Schema Registry"},
			{Token: "cp-enterprise-7.1", Label: "Confluent Schema Registry: Enterprise, 7.1 or later", Engine: "Confluent Schema Registry: Confluent Platform Enterprise 7.1 or later", Detail: "Confluent Platform Enterprise, version 7.1 or later"},
			{Token: "cp-community", Label: "Confluent Schema Registry: Community, or below 7.1", Engine: "Confluent Schema Registry: Community, or Confluent Platform below 7.1", Detail: "Community edition, or Confluent Platform below version 7.1"},
			{Token: "other", Label: "Other, not sure", Engine: "Other or not sure"},
		},
		// Asked only when the scan didn't detect a Schema Registry. Glue is derived
		// outright; a detected Confluent registry narrows to schema_registry_edition.
		Applies: func(p engine.Profile) bool { return p.SourceSRDetected == "" },
		set:     func(in *IntakeInputs, v []string) { in.SourceSRType = first(v) }},

	{Key: "schema_registry_edition", Prompt: "Which Confluent Schema Registry edition does your source run?", Disp: dispRequired,
		Hint: "Your scan detected a Confluent Schema Registry. Schema Linking needs Enterprise 7.1 or later.",
		Opts: []opt{
			{Token: "cp-enterprise-7.1", Label: "Enterprise, 7.1 or later", Engine: "Confluent Schema Registry: Confluent Platform Enterprise 7.1 or later", Detail: "Confluent Platform Enterprise, version 7.1 or later"},
			{Token: "cp-community", Label: "Community, or below 7.1", Engine: "Confluent Schema Registry: Community, or Confluent Platform below 7.1", Detail: "Community edition, or Confluent Platform below version 7.1"},
			{Token: "other", Label: "Other, not sure", Engine: "Other or not sure"},
		},
		Applies: func(p engine.Profile) bool { return p.SourceSRDetected == "confluent" },
		set:     func(in *IntakeInputs, v []string) { in.SourceSRType = first(v) }},

	{Key: "schema_strategy", Prompt: "What do you want to do with your schemas on Confluent Cloud?", Disp: dispRequired,
		Opts: []opt{
			{Token: "migrate", Label: "Migrate my existing schemas", Engine: "Migrate my existing schemas"},
			{Token: "fresh", Label: "Start fresh on Confluent Cloud", Engine: "Start fresh on Confluent Cloud"},
			{Token: "schemaless", Label: "Stay schemaless", Engine: "Stay schemaless"},
		},
		set: func(in *IntakeInputs, v []string) { in.SchemaStrategy = first(v) }},

	{Key: "schema_reachable_to_cc", Prompt: "Can your Schema Registry reach Confluent Cloud to sync schemas (outbound, port 443)?", Disp: dispRequired,
		Opts: []opt{
			{Token: "yes", Label: "Yes", Engine: "Yes"},
			{Token: "no", Label: "No", Engine: "No"},
			{Token: "unsure", Label: "Not sure", Engine: "Not sure"},
		},
		Applies: func(p engine.Profile) bool {
			return p.SourceSRType == "Confluent Schema Registry: Confluent Platform Enterprise 7.1 or later" && p.SchemaStrategy == "Migrate my existing schemas"
		},
		set: func(in *IntakeInputs, v []string) { in.SourceSROutboundReachableToCC = first(v) }},

	{Key: "downtime_tolerance", Prompt: "What is your target downtime window at cutover?", Disp: dispRequired,
		Opts: []opt{
			{Token: "window-all", Label: "A scheduled window, all at once", Engine: "A scheduled window, all at once"},
			{Token: "window-sequential", Label: "A scheduled window, one service at a time", Engine: "A scheduled window, one service at a time"},
			{Token: "minutes", Label: "Minutes per service", Engine: "Minutes per service"},
			{Token: "seconds", Label: "Seconds per service", Engine: "Seconds per service"},
			{Token: "zero", Label: "Zero downtime", Engine: "Zero downtime"},
		},
		// Only the Cluster Linking cutover reads downtime tolerance; a Replicator or
		// start-fresh plan is decided without it, so don't require it there.
		Applies: func(p engine.Profile) bool { return clusterLinkingPath(p) },
		set:     func(in *IntakeInputs, v []string) { in.DowntimeTolerance = first(v) }},

	// ── Optional (default pre-selected) ────────────────────────────────────────
	{Key: "exceeds_standard_limits", Prompt: engine.StandardLimitsQuestion(), Disp: dispOptional, Default: "false",
		Hint:    "If you exceed any of these, we'll plan for an Enterprise cluster instead of Standard. Enterprise runs on private networking.",
		Opts:    yn("Yes", "No", "Yes", "No"),
		Applies: func(p engine.Profile) bool { return engine.Band(p) <= engine.SharedTierMaxBand },
		set:     func(in *IntakeInputs, v []string) { in.ExceedsStandardLimits = first(v) }},

	{Key: "exceeds_enterprise_limits", Prompt: engine.EnterpriseLimitsQuestion(), Disp: dispOptional, Default: "false",
		Opts: yn("Yes", "No", "Yes", "No"),
		Applies: func(p engine.Profile) bool {
			b := engine.Band(p)
			return b > engine.SharedTierMaxBand && b < engine.BandXL
		},
		set: func(in *IntakeInputs, v []string) { in.ExceedsEnterpriseLimits = first(v) }},

	{Key: "target_cloud", Prompt: "Which cloud should your new Confluent Cloud cluster run on?", Disp: dispOptional, Default: "aws",
		Hint: "This is independent of your source cloud. Confluent supports cross-cloud migrations.",
		Opts: []opt{
			{Token: "aws", Label: "AWS", Engine: "AWS"},
			{Token: "azure", Label: "Azure", Engine: "Azure"},
			{Token: "gcp", Label: "GCP", Engine: "GCP"},
		},
		set: func(in *IntakeInputs, v []string) { in.TargetCloud = first(v) }},

	{Key: "target_auth", Prompt: "Which authentication methods should your Confluent Cloud cluster support? Select all that apply.", Disp: dispOptional, Multi: true,
		Hint: "A cluster can support several at once. On AWS we keep your existing mTLS as-is; on Azure and GCP, mTLS needs a Dedicated cluster, which we plan with you. OAuth and API keys are always set up new.",
		Opts: []opt{
			{Token: "api-keys", Label: "API keys (SASL/PLAIN)", Engine: "API keys (SASL/PLAIN)"},
			{Token: "oauth", Label: "OAuth", Engine: "OAuth"},
			{Token: "mtls", Label: "mTLS", Engine: "mTLS"},
		},
		set: func(in *IntakeInputs, v []string) { in.TargetIdentityModel = v }},

	{Key: "client_coordination", Prompt: "How much coordination will it take to cut over all your clients and apps at the same time?", Disp: dispOptional, Default: "moderate",
		Opts: []opt{
			{Token: "easy", Label: "Low (few clients, one team)", Engine: "Easy (few clients, one team)"},
			{Token: "moderate", Label: "Moderate", Engine: "Moderate"},
			{Token: "hard", Label: "High (many clients and teams)", Engine: "Hard (many clients and teams)"},
		},
		Applies: func(p engine.Profile) bool { return !engine.IsServerless(p) },
		set:     func(in *IntakeInputs, v []string) { in.ClientCoordinationBurden = first(v) }},

	{Key: "eos_streams", Prompt: "Do any applications use exactly-once transactions and/or Kafka Streams?", Disp: dispOptional, Multi: true,
		Opts: []opt{
			{Token: "eos", Label: "Exactly-once or transactions", Engine: "Exactly-once or transactions"},
			{Token: "kstreams", Label: "Kafka Streams", Engine: "Kafka Streams"},
		},
		Applies: func(p engine.Profile) bool { return !engine.IsServerless(p) },
		set:     func(in *IntakeInputs, v []string) { in.EosStreams = v }},

	{Key: "connector_destination", Prompt: "Do you want to keep your connectors self-managed, or move them to Confluent-managed?", Disp: dispOptional, Default: "confluent-managed",
		Opts: []opt{
			{Token: "self-managed", Label: "Keep self-managed", Engine: "Keep self-managed", Detail: "Continue to run your own Connect cluster, which is new infrastructure to stand up if you're moving off MSK Connect"},
			{Token: "confluent-managed", Label: "Move to Confluent-managed", Engine: "Move to Confluent-managed", Detail: "Confluent runs the connector for you, with no Connect cluster to stand up, scale, or patch"},
		},
		Applies: connectorsPresent,
		set:     func(in *IntakeInputs, v []string) { in.ConnectorDestination = first(v) }},

	{Key: "consumer_history_requirement", Prompt: "Do your consumers need historical data available after migration?", Disp: dispOptional, Default: "true",
		Hint:    "Only relevant when there's history to carry (tiered storage or long retention) and you're moving existing data.",
		Opts:    yn("Yes", "No", "Required", "Not required"),
		Applies: func(p engine.Profile) bool { return engine.HasBackfillableHistory(p) && migratingData(p) },
		set:     func(in *IntakeInputs, v []string) { in.ConsumerHistoryRequirement = first(v) }},
}

// scanCatalog holds the facts kcp answers from the scan, shown pre-selected and
// editable so a customer can correct or supply them. Current reads the effective
// value off the built profile (which already applies any override).
var scanCatalog = []question{
	{Key: "source_cluster_type", Prompt: "Source cluster type", Scan: true, readOnly: true, requiredWhenMissing: true,
		Opts: []opt{
			{Token: "provisioned", Label: "Provisioned", Engine: "Provisioned"},
			{Token: "serverless", Label: "Serverless", Engine: "Serverless"},
		},
		Current:    func(p engine.Profile) []string { return nonEmpty(p.MSKClusterType) },
		set:        func(in *IntakeInputs, v []string) { in.OvClusterType = first(v) },
		overridden: func(in IntakeInputs) bool { return in.OvClusterType != "" }},

	{Key: "kafka_version", Prompt: "What Kafka version does your source cluster run?", Scan: true, readOnly: true, requiredWhenMissing: true,
		Hint: "Below Kafka 2.4, Cluster Linking isn't available, so we use Confluent Replicator instead.",
		Opts: []opt{
			{Token: "3.0-plus", Label: "3.0 or newer", Engine: "3.0 or newer"},
			{Token: "2.4-2.9", Label: "2.4-2.9", Engine: "2.4-2.9"},
			{Token: "older", Label: "Older than 2.4", Engine: "Older than 2.4"},
		},
		Current:    func(p engine.Profile) []string { return nonEmpty(p.KafkaVersion) },
		set:        func(in *IntakeInputs, v []string) { in.OvKafkaVersion = first(v) },
		overridden: func(in IntakeInputs) bool { return in.OvKafkaVersion != "" }},

	// Inter-broker protocol relative to the 2.8 Cluster Linking floor. Derived from
	// the scanned config when available; this surfaces only when it wasn't AND the
	// Kafka version is on the 2.4-2.9 band, the one place IBP changes the mechanism.
	{Key: "inter_broker_protocol", Prompt: "Is your inter-broker protocol (IBP) 2.8 or later?", Scan: true, requiredWhenMissing: true,
		Hint: "This only matters when your Kafka version is 2.4-2.9. An inter-broker protocol below 2.8 blocks Cluster Linking even if your Kafka version qualifies.",
		// Only relevant when the plan could actually use Cluster Linking: the 2.4-2.9
		// band, data is moving, and the destination is private (Enterprise/Dedicated,
		// not Serverless). A public Standard cluster goes to Replicator regardless, so
		// IBP can't change its outcome and must not gate its plan.
		Applies: func(p engine.Profile) bool {
			return p.KafkaVersion == "2.4-2.9" && p.InterBrokerProtocol == "" && clusterLinkingPath(p)
		},
		Opts:       yn("Yes, 2.8 or later", "No, below 2.8", "Yes", "No"),
		Current:    func(p engine.Profile) []string { return nonEmpty(p.InterBrokerProtocol) },
		set:        func(in *IntakeInputs, v []string) { in.OvInterBrokerProtocol = first(v) },
		overridden: func(in IntakeInputs) bool { return in.OvInterBrokerProtocol != "" }},

	// Partition count anchors sizing (and, with the exceeds-limits questions, the
	// Standard -> Enterprise -> Dedicated escalation). The scan supplies the exact
	// count, so this band question surfaces only when it didn't (and never for
	// Serverless, which is capped at the smallest band). Engine values must match the
	// sizing band labels exactly.
	{Key: "partition_band", Prompt: "Roughly how many partitions do you have, not counting replicas?", Scan: true, requiredWhenMissing: true,
		Applies: func(p engine.Profile) bool { return p.PartitionsExact == nil && !engine.IsServerless(p) },
		Opts: []opt{
			{Token: "under-2500", Label: "Under 2,500", Engine: "Under 2,500"},
			{Token: "2500-30000", Label: "2,500 to 30,000", Engine: "2,500–30,000"},
			{Token: "over-30000", Label: "Over 30,000", Engine: "Over 30,000"},
		},
		Current:    func(p engine.Profile) []string { return nonEmpty(p.PartitionBand) },
		set:        func(in *IntakeInputs, v []string) { in.OvPartitionBand = first(v) },
		overridden: func(in IntakeInputs) bool { return in.OvPartitionBand != "" }},

	{Key: "source_auth", Prompt: "How your Kafka clients authenticate today", Scan: true, Multi: true, requiredWhenMissing: true,
		Opts: []opt{
			{Token: "iam", Label: "AWS IAM", Engine: "AWS IAM"},
			{Token: "scram", Label: "SASL/SCRAM", Engine: "SASL/SCRAM"},
			{Token: "mtls", Label: "TLS client certificates (mTLS)", Engine: "TLS client certificates (mTLS)"},
			{Token: "unauth", Label: "None / plaintext", Engine: "None / plaintext"},
		},
		Current:    func(p engine.Profile) []string { return p.SourceAuthTypes },
		set:        func(in *IntakeInputs, v []string) { in.OvSourceAuth = v },
		overridden: func(in IntakeInputs) bool { return len(in.OvSourceAuth) > 0 }},

	{Key: "tiered_storage", Prompt: "Do your topics use tiered storage?", Scan: true, readOnly: true, requiredWhenMissing: true,
		Hint:       "Tiered data is stored separately from your active topics, in Amazon S3, so retrieving it during cutover takes extra time and can add cost.",
		Applies:    func(p engine.Profile) bool { return !engine.IsServerless(p) }, // Serverless has no tiered storage
		Opts:       yn("Yes", "No", "Yes", "No"),
		Current:    func(p engine.Profile) []string { return nonEmpty(deref(p.StorageMode)) },
		set:        func(in *IntakeInputs, v []string) { in.OvTiered = first(v) },
		overridden: func(in IntakeInputs) bool { return in.OvTiered != "" }},

	{Key: "topics_have_custom_settings", Prompt: "Do any of your topics use non-default settings?", Scan: true, readOnly: true, requiredWhenMissing: true,
		Hint:       "This includes retention over 7 days, a replication factor other than 3, a max message size over 2 MB, or a cleanup policy that combines compact and delete.",
		Opts:       yn("Yes", "No", "Yes", "No"),
		Current:    func(p engine.Profile) []string { return nonEmpty(deref(p.TopicRemediationFlag)) },
		set:        func(in *IntakeInputs, v []string) { in.OvTopicSettings = first(v) },
		overridden: func(in IntakeInputs) bool { return in.OvTopicSettings != "" }},

	{Key: "msk_connect_present", Prompt: "Do you use MSK Connect?", Scan: true, readOnly: true, requiredWhenMissing: true,
		Opts:       yn("Yes", "No", "Yes", "No"),
		Current:    func(p engine.Profile) []string { return nonEmpty(deref(p.MSKConnectPresent)) },
		set:        func(in *IntakeInputs, v []string) { in.OvMSKConnect = first(v) },
		overridden: func(in IntakeInputs) bool { return in.OvMSKConnect != "" }},

	{Key: "self_managed_connectors", Prompt: "Do you run self-managed Kafka Connect?", Scan: true, requiredWhenMissing: true,
		Opts:       yn("Yes", "No", "Yes", "No"),
		Current:    func(p engine.Profile) []string { return nonEmpty(deref(p.SelfManagedConnectors)) },
		set:        func(in *IntakeInputs, v []string) { in.OvSelfManaged = first(v) },
		overridden: func(in IntakeInputs) bool { return in.OvSelfManaged != "" }},
}

// ResolvedQuestion is a catalog question evaluated against the current profile
// and declared inputs — for display in plan.md.
type ResolvedQuestion struct {
	Key        string   `json:"key"`
	Prompt     string   `json:"prompt"`
	Hint       string   `json:"hint,omitempty"`
	Legend     []string `json:"legend,omitempty"` // "token → wording — detail" lines
	Tokens     []string `json:"tokens,omitempty"` // raw option tokens (for terse inline hints)
	Required   bool     `json:"required"`
	Status     string   `json:"status"`               // "open_required" | "open_optional" | "answered" | "scan"
	Value      string   `json:"value"`                // token(s): the answer, or the default for an unanswered optional (display form)
	ValueList  []string `json:"value_list,omitempty"` // multi questions only: the tokens as a typed array for machine consumers
	Multi      bool     `json:"multi,omitempty"`
	Scan       bool     `json:"scan,omitempty"`       // answered from the scan (editable override)
	Overridden bool     `json:"overridden,omitempty"` // a scan fact the customer overrode (render live, not commented)
	ReadOnly   bool     `json:"read_only,omitempty"`  // a scan fact the scan is authoritative on: shown for audit, not advertised as editable
	Source     string   `json:"source,omitempty"`     // canonical source code: app | cluster | all_clusters | built_in_default | scan | override
}

func (q question) legend() []string {
	out := make([]string, len(q.Opts))
	for i, o := range q.Opts {
		line := o.Token + " → " + o.Label
		if o.Detail != "" {
			line += ": " + o.Detail
		}
		if o.Token == q.Default {
			line += "  (default)"
		}
		out[i] = line
	}
	return out
}

// resolveQuestions evaluates the declared catalog (applicable questions only) and
// the scan catalog against one cluster's profile and declared inputs.
func resolveQuestions(p engine.Profile, in IntakeInputs) []ResolvedQuestion {
	var out []ResolvedQuestion
	for _, q := range catalog {
		if q.Applies != nil && !q.Applies(p) {
			continue
		}
		out = append(out, resolveOne(q, p, in))
	}
	for _, q := range scanCatalog {
		if q.Applies != nil && !q.Applies(p) {
			continue
		}
		out = append(out, resolveOne(q, p, in))
	}
	return out
}

// resolveOne classifies a question's status. Scan questions read their value off
// the profile (scan or override); declared questions read it off IntakeInputs.
func resolveOne(q question, p engine.Profile, in IntakeInputs) ResolvedQuestion {
	var toks []string
	if q.Scan {
		toks = mapEngineTokens(q, q.Current(p))
	} else {
		toks = mapEngineTokens(q, q.engineValues(in))
	}
	rq := ResolvedQuestion{Key: q.Key, Prompt: q.Prompt, Hint: q.Hint, Legend: q.legend(), Tokens: q.tokens(), Required: q.Disp == dispRequired && !q.Scan, Multi: q.Multi, Scan: q.Scan, ReadOnly: q.readOnly}
	if q.Scan && q.overridden != nil {
		rq.Overridden = q.overridden(in)
	}
	switch {
	case q.Scan:
		rq.Value = joinList(toks)
		// An undetected scan fact the plan needs becomes a required question the
		// customer must supply; otherwise it stays a (derived) scan fact.
		if rq.Value == "" && q.requiredWhenMissing {
			rq.Status = "open_required"
			rq.Required = true
		} else {
			rq.Status = "scan"
		}
	case len(toks) > 0:
		rq.Status = "answered"
		rq.Value = joinList(toks)
	case q.Disp == dispOptional:
		rq.Status = "open_optional"
		rq.Value = q.Default
	default:
		rq.Status = "open_required"
	}
	// Multi questions carry a typed token array so consumers never parse the joined
	// string; [] when empty.
	if q.Multi {
		rq.ValueList = []string{}
		if rq.Value != "" {
			rq.ValueList = strings.Split(rq.Value, ", ")
		}
	}
	return rq
}

// provenance returns a short, truthful phrase for where a resolved question's
// effective value came from. It is the single source of this wording, shared by
// plan.md and plan-inputs.yaml so the two can never disagree (plan.json carries
// the structured scan/overridden/source flags instead).
//
// A scan fact the customer supplied or changed reads "you set this" — never
// "from scan", because for several scan facts (MSK Connect, self-managed Connect,
// custom topic settings) the scan has no detector at all, and any requiredWhenMissing
// fact the scan misses is likewise customer-supplied. An untouched scan value reads
// "from scan"; a declared answer names its layer (all clusters, cluster, app,
// built-in default).
func (q ResolvedQuestion) provenance() string {
	switch {
	case q.Scan && q.Overridden:
		return "you set this"
	case q.Scan:
		return "from scan"
	case q.Source != "":
		return "from " + sourceLabel(q.Source)
	default:
		return ""
	}
}

// sourceLabel maps a canonical snake_case answer-source code (plan.json) to its
// human phrase (plan.md). Codes without a spaced form render as-is.
func sourceLabel(code string) string {
	switch code {
	case sourceAllClusters:
		return "all clusters"
	case sourceBuiltInDefault:
		return "built-in default"
	default:
		return code // "cluster", "app"
	}
}

// mapEngineTokens maps engine value(s) back to their token(s) via the option table.
func mapEngineTokens(q question, engVals []string) []string {
	var toks []string
	for _, ev := range engVals {
		for _, o := range q.Opts {
			if o.Engine == ev {
				toks = append(toks, o.Token)
				break
			}
		}
	}
	return toks
}

// engineValues reads the resolved engine value(s) for this question off IntakeInputs.
func (q question) engineValues(in IntakeInputs) []string {
	switch q.Key {
	case "private_networking_required":
		return nonEmpty(in.PublicEndpointsOK)
	case "use_case_breadth":
		return nonEmpty(in.UseCaseBreadth)
	case "move_existing_data":
		return nonEmpty(in.NeedsDataMigration)
	case "connects_today":
		return nonEmpty(in.ConnectsToday)
	case "cc_egress_required":
		return nonEmpty(in.CCEgressRequired)
	case "downtime_tolerance":
		return nonEmpty(in.DowntimeTolerance)
	case "schema_registry":
		return nonEmpty(in.SourceSRType)
	case "schema_registry_edition":
		return nonEmpty(in.SourceSRType)
	case "schema_strategy":
		return nonEmpty(in.SchemaStrategy)
	case "schema_reachable_to_cc":
		return nonEmpty(in.SourceSROutboundReachableToCC)
	case "exceeds_standard_limits":
		return nonEmpty(in.ExceedsStandardLimits)
	case "exceeds_enterprise_limits":
		return nonEmpty(in.ExceedsEnterpriseLimits)
	case "target_cloud":
		return nonEmpty(in.TargetCloud)
	case "target_auth":
		return in.TargetIdentityModel
	case "client_coordination":
		return nonEmpty(in.ClientCoordinationBurden)
	case "eos_streams":
		return in.EosStreams
	case "connector_destination":
		return nonEmpty(in.ConnectorDestination)
	case "consumer_history_requirement":
		return nonEmpty(in.ConsumerHistoryRequirement)
	}
	return nil
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func joinList(xs []string) string {
	s := ""
	for i, x := range xs {
		if i > 0 {
			s += ", "
		}
		s += x
	}
	return s
}
