package types

import "time"

// Plan is the deterministic Migration Plan emitted by `kcp report plan`.
// Current scope: source-environment summary, sizing, cluster-type,
// networking, cutover (fleet-wide), and auth (per-cluster). Red flags,
// tiered storage, cost reconciliation, and the rest of the design-doc
// surface land in follow-up PRs.
type Plan struct {
	Header              PlanHeader            `json:"header"`
	Inputs              PlanInputsResolved    `json:"inputs"`
	SourceEnvironment   SourceEnvironment     `json:"source_environment"`
	Sizing              []ClusterSizing       `json:"sizing"`
	ClusterTypeDecision []ClusterTypeDecision `json:"cluster_type_decision"`
	NetworkingDecision  []NetworkingDecision  `json:"networking_decision"`
	// Cutover is fleet-wide — one Plan can't ship two cutover styles.
	// Nil when no clusters were found in the state file.
	Cutover *CutoverDecision `json:"cutover,omitempty"`
	// Auth is per-cluster — source auth methods differ across MSK clusters
	// in the same fleet, so each gets its own source→target mapping.
	Auth []AuthDecision `json:"auth,omitempty"`
	// Schema is fleet-wide (the schema registry is one shared system,
	// not per-cluster). Nil when the section is omitted — currently the
	// `schemaless` path (`sr_detected == none` AND
	// `schema_strategy == no_schemas`).
	Schema *SchemaDecision `json:"schema,omitempty"`
	// RedFlags surfaces the boolean trigger rows over the Plan + state
	// file. Triggered rows are items to discuss with the SE; each row
	// carries its own evidence (field path + value) so the discussion
	// is grounded in the scan, not on inference.
	RedFlags *RedFlagsSection `json:"red_flags,omitempty"`
	// EffortSignals is the list of quantitative signals the customer's
	// PM consumes to scope migration effort. Counts only, no
	// day-estimate.
	EffortSignals *EffortSignalsSection `json:"effort_signals,omitempty"`
	// TieredStorage is a per-cluster section describing the
	// three-dimension trade-off (mechanism / duration / cost direction)
	// for clusters with MSK tiered storage enabled. Nil when no source
	// cluster has TIERED storage.
	TieredStorage *TieredStorageSection `json:"tiered_storage,omitempty"`
	// CostReconciliation lists MSK clusters that show up in the AWS
	// cost report but were NOT discovered by `kcp discover`. Sorted by
	// TotalSpend desc. Nil when cost data is empty or the diff is
	// clean.
	CostReconciliation *CostReconciliationSection `json:"cost_reconciliation,omitempty"`
	SizingAppendix     []SizingMathDetail         `json:"sizing_appendix"`
	OpenQuestions      []OpenQuestion             `json:"open_questions,omitempty"`
}

// OpenQuestion is a per-cluster (or plan-level) gap the customer needs
// to close before the Plan recommendation is fully reliable. State-file
// gaps (missing metrics, missing ACLs) resolve by re-running a `kcp scan`
// command; inferred-signal questions (spiky workload) resolve by
// acknowledging or overriding the input.
type OpenQuestion struct {
	ID         string `json:"id"`
	ClusterID  string `json:"cluster_id,omitempty"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	HowToClose string `json:"how_to_close"`
}

type PlanHeader struct {
	Source        string    `json:"source"`
	StateFilePath string    `json:"state_file_path"`
	KCPVersion    string    `json:"kcp_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	// StateGeneratedAt is the timestamp the source state file was
	// produced (state.Timestamp). Surfaced in the rendered Plan so a
	// reviewer can see how fresh the underlying scan is — important
	// for negative-evidence claims like "0 ACLs" in Appendix A2.
	StateGeneratedAt time.Time `json:"state_generated_at,omitempty"`

	// PlanSchemaVersion is a string while the JSON shape is still
	// shifting: top-level keys (auth_approach, switchover_approach,
	// red_flags, …) are landing in follow-up PRs. Hub consumers should
	// treat "1-experimental" as "additive changes only; renames may
	// happen". Bumps to "1" once §4 of the design ships in full.
	PlanSchemaVersion string `json:"plan_schema_version"`
}

type SourceEnvironment struct {
	Clusters     []SourceClusterSummary `json:"clusters"`
	TotalRegions int                    `json:"total_regions"`
}

type SourceClusterSummary struct {
	ClusterID    string `json:"cluster_id"`
	Region       string `json:"region"`
	BrokerCount  int    `json:"broker_count"`
	TopicCount   int    `json:"topic_count"`
	KafkaVersion string `json:"kafka_version,omitempty"`
	// IsServerless flags MSK Serverless source clusters. Set to true
	// when MskClusterConfig.ClusterType == "SERVERLESS"; the renderer
	// uses it to suppress Provisioned-only framing (broker counts,
	// "incomplete scan" guidance) that doesn't apply to Serverless.
	IsServerless bool `json:"is_serverless,omitempty"`
	// SourceAuths lists the auth methods enabled on the source cluster
	// (stable enum tokens: scram, iam, mtls, unauth). Drawn from the
	// same MSK ClientAuthentication signals AuthDecision reads — surfaced here in
	// §1 so a reader scanning the Source Environment table sees the
	// auth posture alongside brokers / topics before the §4 mapping.
	SourceAuths []string `json:"source_auths,omitempty"`
}

// ----- sizing & decisions -----

type ClusterSizing struct {
	ClusterID string `json:"cluster_id"`
	// SizedInMBps / SizedOutMBps hold the throughput at the configured
	// `sizing_percentile` (default p95; alternates p99, max). Read
	// `plan.inputs.sizing_percentile` to see which percentile was used.
	SizedInMBps        float64 `json:"sized_in_mbps"`
	SizedOutMBps       float64 `json:"sized_out_mbps"`
	PeakInMBps         float64 `json:"peak_in_mbps"`
	PeakOutMBps        float64 `json:"peak_out_mbps"`
	UserPartitions     int     `json:"user_partitions"`
	InternalPartitions int     `json:"internal_partitions"`
	IngressRatio       float64 `json:"ingress_ratio"`
	EgressRatio        float64 `json:"egress_ratio"`
	PartitionRatio     float64 `json:"partition_ratio"`
	MaxRatio           float64 `json:"max_ratio"`
	// MaxRatioDriver names which of the three dimensions produced MaxRatio:
	// "ingress", "egress", or "partitions". Tracked at compute time so the
	// renderer never has to do a float-equality comparison to recover it.
	MaxRatioDriver      string          `json:"max_ratio_driver,omitempty"`
	SizedECKU           int             `json:"sized_ecku"`
	SLAFloorECKU        int             `json:"sla_floor_ecku"`
	FinalECKU           int             `json:"final_ecku"`
	PeakBurstInRatio    float64         `json:"peak_burst_in_ratio"`
	PeakBurstOutRatio   float64         `json:"peak_burst_out_ratio"`
	PeakBurstECKU       int             `json:"peak_burst_ecku"`
	PeakBurstPctOfPLCap float64         `json:"peak_burst_pct_of_pl_cap"`
	SpikyIngress        bool            `json:"spiky_ingress"`
	SpikyEgress         bool            `json:"spiky_egress"`
	Citations           []FieldCitation `json:"citations"`

	// Degraded is true when throughput metrics were missing from the state
	// file. Numbers fall back to the SLA floor and the renderer surfaces
	// the gap rather than silently asserting a sized eCKU.
	Degraded       bool   `json:"degraded,omitempty"`
	DegradedReason string `json:"degraded_reason,omitempty"`

	// InputsMissing names load-bearing scan signals that weren't
	// available when sizing was computed (e.g. `"topics"`, `"acls"`,
	// `"brokers"`). The verdict from downstream decisions may still be
	// solid if driven by customer-declared flags; the renderer reads
	// this list to mark sizing as provisional rather than blanket-
	// deferring the cluster.
	InputsMissing []string `json:"inputs_missing,omitempty"`
}

// ClusterType represents the Confluent Cloud cluster verdict.
// Freight is in the Confluent product but not produced by DecideClusterType
// today — when a rule that emits Freight lands, add the constant then.
type ClusterType string

const (
	ClusterTypeEnterprise ClusterType = "Enterprise"
	ClusterTypeDedicated  ClusterType = "Dedicated"
)

func (c ClusterType) IsValid() bool {
	switch c {
	case ClusterTypeEnterprise, ClusterTypeDedicated:
		return true
	default:
		return false
	}
}

// Topology distinguishes Multi-Zone (MZ) from Single-Zone (SZ) Dedicated
// clusters. Only meaningful when Verdict == Dedicated; Enterprise clusters
// have no topology dimension at this layer.
type Topology string

const (
	TopologyNotApplicable Topology = ""
	TopologyMultiZone     Topology = "MultiZone"
	TopologySingleZone    Topology = "SingleZone"
)

type ClusterTypeDecision struct {
	ClusterID string             `json:"cluster_id"`
	Verdict   ClusterType        `json:"verdict"`
	Triggers  []HardLimitTrigger `json:"triggers,omitempty"`
	// Topology is populated for Dedicated verdicts (MZ default; SZ when the
	// 99.95% single-zone SLA rule fires). Empty for Enterprise.
	Topology Topology `json:"topology,omitempty"`
	// FinalCKU mirrors the sizing's FinalECKU under the Dedicated unit
	// (Confluent Kafka Unit). Set only when Verdict == Dedicated.
	FinalCKU *int `json:"final_cku,omitempty"`
	// InputsMissing names load-bearing scan / plan-input signals that
	// weren't available when this decision was computed (e.g. `"topics"`,
	// `"acls"`). **Observational only** — set post-hoc in
	// `PlanService.Build` after `DecideClusterType` has already run; the
	// Verdict itself was computed against whatever inputs WERE present.
	// Markdown reads this to flag the sizing column as provisional;
	// JSON consumers can branch on the list.
	InputsMissing []string `json:"inputs_missing,omitempty"`
	// EvaluatedRules is the full audit trail for the hard-limit catalog
	// against this cluster — every rule's outcome (fired / not_fired /
	// skipped) with evidence. Read-only audit shape; the renderer
	// surfaces it as a collapsed appendix.
	EvaluatedRules []RuleEvaluation `json:"evaluated_rules,omitempty"`
}

// RuleOutcome enumerates the possible outcomes of evaluating one
// hard-limit rule against a cluster. Persisted in the audit trail so a
// reviewer can replay "what would the rules engine have said given
// this state" without re-running the tool.
//
// The string values below ("fired" / "not_fired" / "skipped") are
// **stable** and intended for downstream consumers to match on by
// equality. Don't rename without a coordinated migration of consumers
// + a plan_schema_version bump.
type RuleOutcome string

const (
	RuleFired    RuleOutcome = "fired"     // rule fired — its evidence is recorded
	RuleNotFired RuleOutcome = "not_fired" // rule evaluated and explicitly didn't fire
	RuleSkipped  RuleOutcome = "skipped"   // rule was not evaluated (missing inputs)
)

// RuleEvaluation is one row of the per-cluster hard-limit audit trail.
// Triggers (above) carries only the fired rules — this carries every
// evaluation including not-fired and skipped, so the rendered appendix
// can show negative evidence ("ACL cap not exceeded: 47 < 4000") and
// skip rationales ("AclsScanned == false; rule inconclusive").
type RuleEvaluation struct {
	RowID       string      `json:"row_id"`
	Description string      `json:"description"`
	Outcome     RuleOutcome `json:"outcome"`
	Evidence    string      `json:"evidence,omitempty"`
	SkipReason  string      `json:"skip_reason,omitempty"`
}

type HardLimitTrigger struct {
	RowID       string `json:"row_id"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
	// CustomerDeclared marks rules whose only signal is a customer-set
	// `plan-inputs.yaml` flag. Renderer surfaces a cost callout on these
	// so a wrong `true` doesn't quietly flip the verdict from Enterprise
	// to Dedicated (Dedicated has a higher monthly cost).
	CustomerDeclared bool `json:"customer_declared,omitempty"`
}

// Networking represents the per-cluster networking verdict.
type Networking string

const (
	NetworkingPrivateLink    Networking = "PrivateLink"
	NetworkingPNI            Networking = "PNI"
	NetworkingTransitGateway Networking = "TransitGateway"
	NetworkingVPCPeering     Networking = "VPCPeering"
)

func (n Networking) IsValid() bool {
	switch n {
	case NetworkingPrivateLink, NetworkingPNI, NetworkingTransitGateway, NetworkingVPCPeering:
		return true
	default:
		return false
	}
}

type NetworkingDecision struct {
	ClusterID       string     `json:"cluster_id"`
	Verdict         Networking `json:"verdict"`
	PeakBurstECKU   int        `json:"peak_burst_ecku"`
	PercentageOfCap float64    `json:"percentage_of_cap"`
	Reason          string     `json:"reason"`
	// InputsMissing — see ClusterTypeDecision.InputsMissing. Populated
	// in lockstep so JSON consumers see the same gating signal on both
	// decisions for an affected cluster. **JSON-consumer-only**: the
	// markdown renderer reads the sister field on `ClusterTypeDecision`
	// (and the same list on `ClusterSizing`) — this copy is here so
	// downstream JSON branching doesn't need a cross-reference lookup.
	InputsMissing []string `json:"inputs_missing,omitempty"`
}

// ----- citations & appendix -----

type FieldCitation struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
}

type SizingMathDetail struct {
	ClusterID         string          `json:"cluster_id"`
	Formula           string          `json:"formula"`
	IntermediateSteps []string        `json:"intermediate_steps"`
	Citations         []FieldCitation `json:"citations,omitempty"`
}

// PlanInputsResolved is the customer's PlanInputs merged with pinned
// defaults from plan-config.yaml. Raw preserves the original customer
// input (nil pointers for fields they didn't set); the flat fields below
// are the merged values PlanService computes against.
type PlanInputsResolved struct {
	// Raw preserves the original customer-supplied `PlanInputs`. **Note:**
	// the flat fields below carry the *global* resolved view; per-cluster
	// overrides (`Raw.Clusters[<name>]`) are layered on top per cluster
	// inside `PlanService.Build` via `ResolvePlanInputsForCluster` — they
	// are NOT applied to the values stored here. JSON consumers wanting
	// the per-cluster view should re-run the resolver against `Raw`.
	Raw *PlanInputs `json:"raw,omitempty"`

	SLATarget          string  `json:"sla_target"`
	SizingPercentile   string  `json:"sizing_percentile"`
	HeadroomFraction   float64 `json:"headroom_fraction"`
	SpikyWorkloadRatio float64 `json:"spiky_workload_ratio"`

	// Customer-declared hard requirements. Booleans (not *bool) — defaults
	// resolve to false, which is the safe verdict (no escalation to Dedicated).
	EnforceSchemasAtTheBroker            bool `json:"enforce_schemas_at_the_broker"`
	RequiresHighThroughputRESTProduceAPI bool `json:"requires_high_throughput_rest_produce_api"`
	Requires9995SLAWithinSingleZone      bool `json:"requires_99_95_sla_within_a_single_zone"`

	// Target cloud + existing VPC connectivity (Dedicated-path networking).
	TargetCloud             string `json:"target_cloud"`
	ExistingVPCConnectivity string `json:"existing_vpc_connectivity"`

	// Networking triggers that flip the AWS-Enterprise default from PNI
	// to PrivateLink. CCEgressRequired and ProjectedPNIGatewayCount are
	// workload properties, not state-derived.
	CCEgressRequired         bool `json:"cc_egress_required"`
	ProjectedPNIGatewayCount int  `json:"projected_pni_gateway_count"`

	// Cutover — `downtime_tolerance` drives the style; the others
	// govern gateway eligibility + opt-out.
	DowntimeTolerance            string `json:"downtime_tolerance"`
	SubPattern                   string `json:"sub_pattern"`
	PreferGateway                bool   `json:"prefer_gateway"`
	ConfluentForKubernetesStatus string `json:"confluent_for_kubernetes_status"`
	CCGatewayLicenseStatus       string `json:"cc_gateway_license_status"`
	IAMPreMigrationStatus        string `json:"iam_pre_migration_status"`

	// Auth — target verdict; cluster-level override flows through
	// `applyClusterOverride` per the heterogeneous-fleet rule.
	TargetAuthMethod string `json:"target_auth_method"`

	// Schema migration. Strings/bools resolved from the customer
	// input with `unknown` / nil-tri-state semantics: SchemaStrategy
	// defaults to `unknown` so first-run plans don't fire spurious
	// schemaless verdicts. The CP version + edition + outbound
	// reachability stay nil-tri-state because "false" and "unknown"
	// produce different OQs.
	SchemaStrategy                string `json:"schema_strategy"`
	SourceSROutboundReachableToCC *bool  `json:"source_sr_outbound_reachable_to_cc,omitempty"`
	ConfluentSRCPVersion          string `json:"confluent_sr_cp_version,omitempty"`
	ConfluentSRCPEdition          string `json:"confluent_sr_cp_edition,omitempty"`

	// Red Flags customer flags — nil tri-state.
	ExactlyOnceTransactionsInUse *bool `json:"exactly_once_transactions_in_use,omitempty"`
	KafkaStreamsInUse            *bool `json:"kafka_streams_in_use,omitempty"`

	// Tiered Storage knobs. Strings normalized to lowercase tokens by
	// the resolver; empty means "no preference declared" (treated as
	// `unknown` by the detector so the section surfaces the OQ).
	ConsumerHistoryRequirement string `json:"consumer_history_requirement,omitempty"`
	HistoricalDataStrategy     string `json:"historical_data_strategy,omitempty"`
}

// ----- cutover -----

// CutoverStyle is the strategic shape of the cutover.
// Mapped 1:1 from `downtime_tolerance` in plan-inputs.yaml.
type CutoverStyle string

const (
	// String values match the customer-facing tokens (hyphenated).
	// Go identifiers can't contain hyphens; the renderer translates these
	// to display labels via cutoverStyleName.
	CutoverStopRestartRepeat CutoverStyle = "Stop-Restart-Repeat"
	CutoverStopWaitRestart   CutoverStyle = "Stop-Wait-Restart"
	CutoverRestartAllAtOnce  CutoverStyle = "Restart-All-At-Once"
	CutoverBlueGreen         CutoverStyle = "Blue/Green"
)

// CutoverSubPattern is the team-topology choice within Stop-Restart-Repeat.
// Empty for any other CutoverStyle.
type CutoverSubPattern string

const (
	SubPatternUnset        CutoverSubPattern = ""
	SubPatternAppByApp     CutoverSubPattern = "app-by-app"
	SubPatternTopicByTopic CutoverSubPattern = "topic-by-topic"
)

// GatewayMediated is tri-state: true / false / not_applicable. The
// last value fires only on Blue/Green, where the gateway doesn't sit
// on the cutover step at all.
type GatewayMediated string

const (
	GatewayMediatedTrue          GatewayMediated = "true"
	GatewayMediatedFalse         GatewayMediated = "false"
	GatewayMediatedNotApplicable GatewayMediated = "not_applicable"
)

// RecommendationStatus signals how confident the plan is in the
// recommendation, and what (if anything) the customer should resolve to
// move it forward.
type RecommendationStatus string

const (
	// RecommendationCanonical: customer is gateway-eligible and the
	// Stop-Restart-Repeat + Gateway pairing is being recommended.
	RecommendationCanonical RecommendationStatus = "canonical"
	// RecommendationCustomerChoice: customer either opted out of the
	// gateway (prefer_gateway: false) or picked Blue/Green.
	RecommendationCustomerChoice RecommendationStatus = "customer_choice"
	// RecommendationDegradedAwaitingOQ: prefer_gateway is still default
	// and all three gateway prereqs are not_started — customer hasn't
	// engaged with the gateway question. Plan falls back to plain CL.
	RecommendationDegradedAwaitingOQ RecommendationStatus = "degraded_awaiting_oq"
	// RecommendationDegradedPrereqsPending: prefer_gateway is true but
	// at least one prereq is still at not_started. Some engagement,
	// not finished.
	RecommendationDegradedPrereqsPending RecommendationStatus = "degraded_prereqs_pending"
)

// PrereqStatus mirrors the customer-facing plan-inputs status values
// (`not_started` → blocked, `in_progress` → in-progress, `complete` →
// met) plus an `unconfirmed` fallback for prereqs whose source data
// isn't pinned (e.g. Express tier compatibility per release).
type PrereqStatus string

const (
	PrereqMet         PrereqStatus = "met"
	PrereqInProgress  PrereqStatus = "in_progress"
	PrereqBlocked     PrereqStatus = "blocked"
	PrereqUnconfirmed PrereqStatus = "unconfirmed"
)

// Prereq is one item in the rendered Prerequisites table on the
// cutover section. Description is the human-readable label; the
// renderer doesn't transform it.
type Prereq struct {
	Description string       `json:"description"`
	Status      PrereqStatus `json:"status"`
}

// CutoverDecision is the fleet-wide cutover plan.
type CutoverDecision struct {
	Style                CutoverStyle         `json:"style"`
	SubPattern           CutoverSubPattern    `json:"sub_pattern,omitempty"` // only when Style == StopRestartRepeat
	GatewayMediated      GatewayMediated      `json:"gateway_mediated"`
	RecommendationStatus RecommendationStatus `json:"recommendation_status"`
	// AlternativesShown lists the styles the renderer explains for
	// trust but doesn't recommend — gives the reader the full pattern
	// set without forcing a deeper decision tree.
	AlternativesShown []CutoverStyle `json:"alternatives_shown_for_trust,omitempty"`
	Prereqs           []Prereq       `json:"prereqs,omitempty"`
}

// ----- auth -----

// AuthDecision is the per-cluster source→target auth mapping.
// SourceAuths holds the methods detected on the source MSK cluster as
// stable enum tokens ("scram", "iam", "mtls", "unauth"); the plan does
// NOT pick one when multiple are enabled — it shows all options.
type AuthDecision struct {
	ClusterID      string           `json:"cluster_id"`
	SourceAuths    []string         `json:"source_auths_detected"`
	TargetMappings []AuthMappingRow `json:"target_mappings,omitempty"`
}

// ----- schema -----

// SchemaSource is the detected source-side schema registry, derived
// from the scanner's `state.schema_registries`. `none` means the
// scanner ran but found neither a Confluent SR nor a Glue registry.
// `confluent_and_glue` covers the rare both-registries deployment —
// the decision applies each path independently.
type SchemaSource string

const (
	SchemaSourceNone             SchemaSource = "none"
	SchemaSourceConfluent        SchemaSource = "confluent"
	SchemaSourceGlue             SchemaSource = "glue"
	SchemaSourceConfluentAndGlue SchemaSource = "confluent_and_glue"
)

// SchemaPath is the recommended migration path:
//   - `schema_linking` — Schema Linking from source Confluent SR → CC SR
//     (zero-data-loss mirror; all three eligibility constraints hold).
//   - `kcp_migrate_schemas_glue` — `kcp create-asset migrate-schemas
//     --glue-registry` generates Terraform that imports every Glue
//     schema into CC SR in one apply.
//   - `defer_to_account_team` — Confluent SR detected but Schema-Linking
//     eligibility fails (CP < 7.0, Community edition, or no outbound
//     reachability). REST API export/import is technically possible but
//     not deterministically described by kcp.
//   - `schemaless` — no source SR + customer declared `no_schemas`.
//   - `unknown` — fallback when an Open Question must close before the
//     path is decidable (typically `schema_strategy: unknown`).
type SchemaPath string

const (
	SchemaPathSchemaLinking  SchemaPath = "schema_linking"
	SchemaPathMigrateGlue    SchemaPath = "kcp_migrate_schemas_glue"
	SchemaPathDeferToAccount SchemaPath = "defer_to_account_team"
	SchemaPathSchemaless     SchemaPath = "schemaless"
	SchemaPathUnknown        SchemaPath = "unknown"
)

// SchemaDecision is the fleet-wide Schema Migration recommendation.
// Source describes what was scanned; Paths describes every verdict
// that applies — usually a single-element slice (one source → one
// path), but the dual `confluent_and_glue` case carries two so JSON
// consumers branching on a single path-slot don't miss the second
// arm.
//
// The eligibility flags are populated only when Source includes
// `confluent` so the renderer can show why Schema Linking was or
// wasn't chosen.
type SchemaDecision struct {
	Source SchemaSource `json:"source"`
	// Paths lists every recommended verdict that applies, in
	// rendering order. Single-source cases have len(Paths)==1; the
	// dual-source `confluent_and_glue` case has 2 entries (Glue
	// first — it's the automatable path; Confluent path second).
	//
	// **JSON-consumer note for dual-source.** When Source is
	// `confluent_and_glue` and len(Paths)==1 (only the Glue arm
	// landed), the Confluent arm is in one of two states:
	//   (a) eligibility flags undeclared — pending. Disambiguate by
	//       reading the OpenQuestions for `schema_linking_eligibility_unknown`.
	//   (b) all three eligibility flags resolved AND at least one is
	//       false — verified ineligible. The OQ
	//       `schema_linking_ineligible` will be present.
	// Reading `MeetsCPVersionFloor` / `MeetsCPEditionRequirement` /
	// `SourceSROutboundReachable` tri-states gives the same signal.
	Paths []SchemaPath `json:"paths"`
	// Schema-Linking eligibility constraints — all three must hold for
	// SchemaPathSchemaLinking. Populated only when Source includes
	// `confluent`; the renderer prints a 3-row eligibility table from
	// these flags. *KnownAsTrue distinguishes "verified true" from
	// "verified false" vs "unknown" — when any one is unknown the
	// resulting Paths include `unknown` and the OQ asks the customer
	// to confirm rather than guessing.
	MeetsCPVersionFloor       *bool `json:"meets_cp_version_floor,omitempty"`
	MeetsCPEditionRequirement *bool `json:"meets_cp_edition_requirement,omitempty"`
	SourceSROutboundReachable *bool `json:"source_sr_outbound_reachable,omitempty"`
	// GlueRegistries lists the Glue registry names detected on the
	// source side — surfaced in the Terraform command the renderer
	// emits for SchemaPathMigrateGlue (one apply per registry).
	GlueRegistries []string `json:"glue_registries,omitempty"`
	// ConfluentSRURLs lists the Confluent SR URLs detected. Surfaced
	// in the eligibility table header so a reader knows which SR the
	// verdict applies to.
	ConfluentSRURLs []string `json:"confluent_sr_urls,omitempty"`
}

// AuthMappingRow describes one source→target option. TransparentSwap
// fires when the CC Gateway can swap credentials without a producer
// restart (e.g. SCRAM → SASL/PLAIN with API key). GatewayCompatible is
// false for IAM — IAM clients cannot connect to the gateway and must
// pre-migrate to SCRAM or mTLS first.
type AuthMappingRow struct {
	SourceAuth        string `json:"source_auth"`
	EffectiveTarget   string `json:"effective_target"`
	GatewayCompatible bool   `json:"gateway_compatible"`
	TransparentSwap   bool   `json:"transparent_swap"`
	Note              string `json:"note,omitempty"`
	// Source + LastVerified carry per-row provenance from the
	// auth_mapping table in plan-config.yaml — surfaced in the
	// rendered Plan as a footnote so the reviewer can audit where the
	// recommendation comes from.
	Source       string `json:"source,omitempty"`
	LastVerified string `json:"last_verified,omitempty"`
}

// ----- red flags -----

// RedFlagStatus is the tri-state verdict for one Red Flag row:
//
//   - `triggered` — the boolean predicate over the state file is true.
//   - `not_triggered` — the predicate is false and we have enough
//     scan data to say so with confidence.
//   - `unknown` — the underlying signal isn't available (scan didn't
//     run, customer-declared flag wasn't set, etc.). The rendered
//     Plan surfaces it as "not scanned" rather than silently
//     defaulting to `not_triggered`.
type RedFlagStatus string

const (
	RedFlagTriggered    RedFlagStatus = "triggered"
	RedFlagNotTriggered RedFlagStatus = "not_triggered"
	RedFlagUnknown      RedFlagStatus = "unknown"
)

// RedFlag is one row in §Red Flags. Title is the customer-facing
// label; Evidence is the field path + value that drove the verdict so
// the SE-customer discussion can ground in scan facts. ClusterID is
// populated only for per-cluster rows; fleet-level rows leave it
// empty.
type RedFlag struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Status    RedFlagStatus `json:"status"`
	Evidence  string        `json:"evidence,omitempty"`
	ClusterID string        `json:"cluster_id,omitempty"`
}

// RedFlagsSection is the fleet-wide Red Flags decision output. Rows is
// the full set evaluated in row order; the renderer leads with
// triggered rows and collapses not-triggered/unknown into a tail
// summary.
type RedFlagsSection struct {
	Rows []RedFlag `json:"rows"`
}

// ----- effort signals -----

// EffortSignal is one quantitative input the customer's PM consumes to
// scope migration effort. Count is the raw integer the signal
// produced (e.g. number of IAM-auth clients). Note carries any caveat
// the spec calls out (e.g. MM2 `IdentityReplicationPolicy` undercounts
// checkpoint topics).
type EffortSignal struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Count int    `json:"count"`
	Note  string `json:"note,omitempty"`
}

// EffortSignalsSection is the fleet-wide list of effort signals.
type EffortSignalsSection struct {
	Signals []EffortSignal `json:"signals"`
}

// ----- tiered storage -----

// TieredStorageCluster is the per-cluster tiered-storage view: which
// cluster has TIERED storage, the GB volume from CloudWatch
// (`RemoteLogSizeBytes` average — informational, not the basis for a
// dollar estimate), and whether the customer's
// `consumer_history_requirement` indicates the data must be carried
// forward.
type TieredStorageCluster struct {
	ClusterID   string `json:"cluster_id"`
	StorageMode string `json:"storage_mode"`
	// RemoteLogSizeBytes is the average from CloudWatch metrics. Zero
	// when the metric wasn't collected or the cluster doesn't have
	// tiered data yet.
	RemoteLogSizeBytes float64 `json:"remote_log_size_bytes,omitempty"`
}

// TieredStorageSection surfaces the three-dimension trade-off
// (mechanism / duration / cost direction) for fleets with at least one
// TIERED-storage cluster. Customer-decision shaped: kcp does not pick
// a path, it makes the trade-off legible.
type TieredStorageSection struct {
	Clusters                   []TieredStorageCluster `json:"clusters"`
	ConsumerHistoryRequirement string                 `json:"consumer_history_requirement"`
	HistoricalDataStrategy     string                 `json:"historical_data_strategy"`
}

// ----- cost reconciliation -----

// HiddenClusterCandidate is one MSK instance type that shows up in the
// AWS cost report but was NOT discovered by `kcp discover`. Sorted by
// TotalSpend desc; the customer (FinOps / cloud lead) decides which
// candidates are real.
type HiddenClusterCandidate struct {
	Region         string  `json:"region"`
	InstanceType   string  `json:"instance_type"`
	TotalSpend     float64 `json:"total_spend"`
	MonthsObserved int     `json:"months_observed,omitempty"`
	DaysObserved   int     `json:"days_observed,omitempty"`
}

// CostReconciliationSection lists the candidate hidden MSK clusters
// per region. Nil when cost data is empty or the diff is clean. When
// cost data IS empty, the section nils and the detector emits an OQ
// pointing at `kcp report costs`.
type CostReconciliationSection struct {
	Candidates []HiddenClusterCandidate `json:"candidates"`
}
