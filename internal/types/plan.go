package types

import "time"

// Plan is the deterministic Migration Plan emitted by `kcp report plan`.
// This MVP scope covers source-environment summary, sizing, cluster-type
// decision, and networking decision only. Auth approach, switchover,
// red flags, cost reconciliation, and the rest of the §4 surface in the
// design doc land in follow-up PRs.
type Plan struct {
	Header              PlanHeader            `json:"header"`
	Inputs              PlanInputsResolved    `json:"inputs"`
	SourceEnvironment   SourceEnvironment     `json:"source_environment"`
	Sizing              []ClusterSizing       `json:"sizing"`
	ClusterTypeDecision []ClusterTypeDecision `json:"cluster_type_decision"`
	NetworkingDecision  []NetworkingDecision  `json:"networking_decision"`
	SizingAppendix      []SizingMathDetail    `json:"sizing_appendix"`
	OpenQuestions       []OpenQuestion        `json:"open_questions,omitempty"`
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
}
