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
}

// ----- sizing & decisions -----

type ClusterSizing struct {
	ClusterID           string          `json:"cluster_id"`
	P95InMBps           float64         `json:"p95_in_mbps"`
	P95OutMBps          float64         `json:"p95_out_mbps"`
	PeakInMBps          float64         `json:"peak_in_mbps"`
	PeakOutMBps         float64         `json:"peak_out_mbps"`
	UserPartitions      int             `json:"user_partitions"`
	InternalPartitions  int             `json:"internal_partitions"`
	IngressRatio        float64         `json:"ingress_ratio"`
	EgressRatio         float64         `json:"egress_ratio"`
	PartitionRatio      float64         `json:"partition_ratio"`
	MaxRatio            float64         `json:"max_ratio"`
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

type ClusterTypeDecision struct {
	ClusterID string             `json:"cluster_id"`
	Verdict   ClusterType        `json:"verdict"`
	Triggers  []HardLimitTrigger `json:"triggers,omitempty"`
}

type HardLimitTrigger struct {
	RowID       string `json:"row_id"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
}

// Networking represents the per-cluster networking verdict.
type Networking string

const (
	NetworkingPrivateLink Networking = "PrivateLink"
	NetworkingPNI         Networking = "PNI"
)

func (n Networking) IsValid() bool {
	switch n {
	case NetworkingPrivateLink, NetworkingPNI:
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
	Raw *PlanInputs `json:"raw,omitempty"`

	SLATarget                  string  `json:"sla_target"`
	SizingPercentile           string  `json:"sizing_percentile"`
	HeadroomFraction           float64 `json:"headroom_fraction"`
	PrivateLinkSafetyThreshold float64 `json:"privatelink_safety_threshold"`
	SpikyWorkloadRatio         float64 `json:"spiky_workload_ratio"`
}
