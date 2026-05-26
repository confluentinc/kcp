package types

// PlanInputs is the parsed plan-inputs.yaml. Pointers distinguish "unset"
// from the zero value so the loader can echo only the fields the customer
// explicitly set.
//
// MVP scope: sizing knobs + the customer-declared hard-requirement flags
// that force Dedicated, plus the networking-topology signal that selects
// PNI / Transit Gateway / VPC Peering on the Dedicated path.
//
// Customer-declared flags that flip the cluster-type verdict
// (`EnforceSchemasAtTheBroker`, `RequiresHighThroughputRESTProduceAPI`,
// `Requires9995SLAWithinSingleZone`) are wrong-click sensitive — a
// misfired `true` quietly raises the monthly cost. The renderer
// surfaces a ⚠ cost callout on the Dedicated verdict when any of them
// is the reason.
type PlanInputs struct {
	// Sizing overrides (defaults live in plan-config.yaml).
	SLATarget          *string  `yaml:"sla_target,omitempty"          json:"sla_target,omitempty"`
	SizingPercentile   *string  `yaml:"sizing_percentile,omitempty"   json:"sizing_percentile,omitempty"`
	HeadroomFraction   *float64 `yaml:"headroom_fraction,omitempty"   json:"headroom_fraction,omitempty"`
	SpikyWorkloadRatio *float64 `yaml:"spiky_workload_ratio,omitempty" json:"spiky_workload_ratio,omitempty"`

	// Customer-declared hard requirements that force Dedicated.
	// Default `false`; only the customer (or an SE on their behalf) sets these.
	EnforceSchemasAtTheBroker            *bool `yaml:"enforce_schemas_at_the_broker,omitempty"            json:"enforce_schemas_at_the_broker,omitempty"`
	RequiresHighThroughputRESTProduceAPI *bool `yaml:"requires_high_throughput_rest_produce_api,omitempty" json:"requires_high_throughput_rest_produce_api,omitempty"`
	Requires9995SLAWithinSingleZone      *bool `yaml:"requires_99_95_sla_within_a_single_zone,omitempty"  json:"requires_99_95_sla_within_a_single_zone,omitempty"`

	// When source auth includes mTLS AND target_cloud is not `aws`,
	// Dedicated is required because only Dedicated supports mTLS on
	// Azure / GCP. MSK is AWS-only so this defaults to `aws`.
	TargetCloud *string `yaml:"target_cloud,omitempty" json:"target_cloud,omitempty"`

	// Existing VPC connectivity to MSK. When set to `transit_gateway`
	// or `vpc_peering` and the cluster is Dedicated, the same pattern
	// is recommended for Confluent Cloud. `privatelink_or_pni` (default)
	// keeps the AWS-to-AWS Enterprise default (PNI) unless one of the
	// PrivateLink triggers fires (see CCEgressRequired,
	// ProjectedPNIGatewayCount, non-AWS target_cloud).
	ExistingVPCConnectivity *string `yaml:"existing_vpc_connectivity,omitempty" json:"existing_vpc_connectivity,omitempty"`

	// CCEgressRequired = "Do CC-side workloads need to push traffic
	// back into your customer VPC?" PNI does not natively support
	// egress from CC into customer infrastructure; when true the
	// networking default flips from PNI to PrivateLink.
	CCEgressRequired *bool `yaml:"cc_egress_required,omitempty" json:"cc_egress_required,omitempty"`

	// ProjectedPNIGatewayCount — projected PNI gateway count for the
	// deployment. When ≥ 2, the recommendation flips from PNI to
	// PrivateLink. Default 1 (single gateway).
	ProjectedPNIGatewayCount *int `yaml:"projected_pni_gateway_count,omitempty" json:"projected_pni_gateway_count,omitempty"`

	// Clusters — per-cluster overrides keyed by source cluster name.
	// Heterogeneous fleets (mixed-SLA, mixed-tier) need finer-grained
	// inputs than the global flags above; without this, flipping a
	// single global flag like `requires_99_95_sla_within_a_single_zone:
	// true` would force Dedicated SZ on every cluster including
	// idle / Zookeeper / Serverless ones. Resolution: per-cluster
	// override wins; fall back to global default. Only the subset of
	// fields that makes sense per-cluster lives here.
	Clusters map[string]ClusterPlanInputs `yaml:"clusters,omitempty" json:"clusters,omitempty"`
}

// ClusterPlanInputs is the per-cluster override slice of PlanInputs.
// All fields are optional pointers so the resolver can detect "not set
// for this cluster" and fall back to the global default.
//
// **Intentionally global-only** (not in this struct):
//   - `sizing_percentile` — the Appendix A1 preamble names a single
//     percentile for the whole plan; mixing P95 + P99 in one run would
//     desync the prose. If a customer genuinely needs different
//     percentiles per cluster, run the plan twice with different
//     inputs and merge — explicit beats invisible.
type ClusterPlanInputs struct {
	SLATarget                            *string  `yaml:"sla_target,omitempty"                                 json:"sla_target,omitempty"`
	HeadroomFraction                     *float64 `yaml:"headroom_fraction,omitempty"                          json:"headroom_fraction,omitempty"`
	SpikyWorkloadRatio                   *float64 `yaml:"spiky_workload_ratio,omitempty"                       json:"spiky_workload_ratio,omitempty"`
	EnforceSchemasAtTheBroker            *bool    `yaml:"enforce_schemas_at_the_broker,omitempty"              json:"enforce_schemas_at_the_broker,omitempty"`
	RequiresHighThroughputRESTProduceAPI *bool    `yaml:"requires_high_throughput_rest_produce_api,omitempty"  json:"requires_high_throughput_rest_produce_api,omitempty"`
	Requires9995SLAWithinSingleZone      *bool    `yaml:"requires_99_95_sla_within_a_single_zone,omitempty"    json:"requires_99_95_sla_within_a_single_zone,omitempty"`
	TargetCloud                          *string  `yaml:"target_cloud,omitempty"                               json:"target_cloud,omitempty"`
	ExistingVPCConnectivity              *string  `yaml:"existing_vpc_connectivity,omitempty"                  json:"existing_vpc_connectivity,omitempty"`
	CCEgressRequired                     *bool    `yaml:"cc_egress_required,omitempty"                         json:"cc_egress_required,omitempty"`
	ProjectedPNIGatewayCount             *int     `yaml:"projected_pni_gateway_count,omitempty"                json:"projected_pni_gateway_count,omitempty"`
}
