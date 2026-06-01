package types

// PlanInputs is the parsed plan-inputs.yaml. Pointers distinguish "unset"
// from the zero value so the loader can echo only the fields the customer
// explicitly set.
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

	// ----- cutover -----

	// DowntimeTolerance maps 1:1 to a cutover style. Enum values:
	//   zero                          → Blue/Green
	//   seconds_per_service           → Stop-Restart-Repeat (gateway REQUIRED)
	//   minutes_per_service           → Stop-Restart-Repeat (gateway optional)
	//   scheduled_window_sequential   → Stop-Wait-Restart
	//   scheduled_window_all_at_once  → Restart-All-At-Once
	//   let_confluent_choose          → Stop-Restart-Repeat (Confluent default)
	DowntimeTolerance *string `yaml:"downtime_tolerance,omitempty" json:"downtime_tolerance,omitempty"`

	// SubPattern is only consulted when the resolved style is
	// Stop-Restart-Repeat. Values: `app-by-app` (default) | `topic-by-topic`.
	SubPattern *string `yaml:"sub_pattern,omitempty" json:"sub_pattern,omitempty"`

	// PreferGateway lets the customer opt out of the gateway-mediated
	// recommendation even when they're eligible. Default true mirrors
	// Confluent's canonical recommendation; set false for plain Cluster
	// Linking.
	PreferGateway *bool `yaml:"prefer_gateway,omitempty" json:"prefer_gateway,omitempty"`

	// Gateway prereq statuses. Each is one of `not_started` |
	// `in_progress` | `complete`. Eligibility for the canonical
	// recommendation requires all three to be at `in_progress` or
	// `complete` (with the IAM prereq only relevant when IAM is
	// detected on any source cluster).
	ConfluentForKubernetesStatus *string `yaml:"confluent_for_kubernetes_status,omitempty" json:"confluent_for_kubernetes_status,omitempty"`
	CCGatewayLicenseStatus       *string `yaml:"cc_gateway_license_status,omitempty"       json:"cc_gateway_license_status,omitempty"`
	IAMPreMigrationStatus        *string `yaml:"iam_pre_migration_status,omitempty"        json:"iam_pre_migration_status,omitempty"`

	// ----- auth -----

	// TargetAuthMethod overrides the default target auth (looked up
	// per source auth in `auth_mapping`). Enum:
	// `confluent_cloud_api_keys` (default) | `mtls` | `oauth`.
	// Per-cluster override available via `clusters[name].target_auth_method`.
	TargetAuthMethod *string `yaml:"target_auth_method,omitempty" json:"target_auth_method,omitempty"`

	// ----- schema migration -----

	// SchemaStrategy declares the customer's intent for schemas. Enum:
	//   unknown                          → emit OQ (default; first-run safety)
	//   no_schemas                       → schemaless path (omit §Schema)
	//   adopt_schemas_during_migration   → start with no source SR, adopt CC SR
	//   migrate_existing_schema_registry → run the SR-detected path
	SchemaStrategy *string `yaml:"schema_strategy,omitempty" json:"schema_strategy,omitempty"`

	// SourceSROutboundReachableToCC is the customer's network-reachability
	// declaration: can the source Confluent SR reach the CC SR endpoint
	// outbound? Schema Linking's Schema Exporter is one-directional from
	// source SR → CC SR, so without this the gateway-eligible verdict
	// can't hold. Default nil = unknown.
	SourceSROutboundReachableToCC *bool `yaml:"source_sr_outbound_reachable_to_cc,omitempty" json:"source_sr_outbound_reachable_to_cc,omitempty"`

	// ConfluentSRCPVersion is the customer-declared Confluent Platform
	// version of the source Schema Registry. Schema Linking requires
	// CP 7.0 or later. Default nil = unknown — the scanner does not
	// populate this today, so the customer declares it via plan-inputs.
	// Accepted shape: "7.5.1" / "7.0" / "6.2" — string compare against
	// the configured floor.
	ConfluentSRCPVersion *string `yaml:"confluent_sr_cp_version,omitempty" json:"confluent_sr_cp_version,omitempty"`

	// ConfluentSRCPEdition is the customer-declared Confluent Platform
	// edition. Schema Linking requires `enterprise` (CP 7.0 Community
	// does not include it). Enum: `enterprise` | `community`.
	ConfluentSRCPEdition *string `yaml:"confluent_sr_cp_edition,omitempty" json:"confluent_sr_cp_edition,omitempty"`

	// ----- red flags -----

	// ExactlyOnceTransactionsInUse declares whether the source has
	// EOS / Kafka transactions enabled. There is no detectable state
	// signal for this; customer-only. Default nil = unknown — the
	// Red Flag row surfaces as "not scanned" rather than firing false.
	ExactlyOnceTransactionsInUse *bool `yaml:"exactly_once_transactions_in_use,omitempty" json:"exactly_once_transactions_in_use,omitempty"`

	// KafkaStreamsInUse declares whether Kafka Streams apps consume
	// from the source. The Red Flag detector also runs a
	// topic-pattern scan (`-changelog` / `-repartition`); this flag
	// lets the customer affirm even when topic patterns miss.
	KafkaStreamsInUse *bool `yaml:"kafka_streams_in_use,omitempty" json:"kafka_streams_in_use,omitempty"`

	// ----- tiered storage -----

	// ConsumerHistoryRequirement declares whether downstream consumers
	// actually need to replay historical (tiered) data. Enum:
	//   required (default)  → backfill is required; weigh the 3
	//                         dimensions (mechanism / duration / cost)
	//   not_required        → real-time-only consumers; defer to the
	//                         account team for the cascade of
	//                         per-topic + consumer-offset decisions
	//   unknown             → customer hasn't decided yet
	ConsumerHistoryRequirement *string `yaml:"consumer_history_requirement,omitempty" json:"consumer_history_requirement,omitempty"`

	// HistoricalDataStrategy is the customer's preferred path when
	// tiered-storage backfill IS required. Enum:
	//   keep_msk_running_until_data_expires
	//   bulk_load_historical_via_external_tool
	//   defer_to_account_team   (default for the not_required cascade)
	HistoricalDataStrategy *string `yaml:"historical_data_strategy,omitempty" json:"historical_data_strategy,omitempty"`

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
	// TargetAuthMethod is the per-cluster override for the target
	// auth verdict. Same enum as the global field.
	TargetAuthMethod *string `yaml:"target_auth_method,omitempty" json:"target_auth_method,omitempty"`
	// DowntimeTolerance is the per-cluster override for the cutover
	// style. Same enum as the global field. Use when heterogeneous
	// fleets need different cutover styles per cluster — e.g. a
	// latency-sensitive service on Blue/Green while batch jobs run
	// Stop-Restart-Repeat. Without this, the customer would have to
	// slice the state file and run kcp once per cluster subset.
	DowntimeTolerance *string `yaml:"downtime_tolerance,omitempty" json:"downtime_tolerance,omitempty"`
	// SubPattern is the per-cluster override; only meaningful when the
	// resolved style is Stop-Restart-Repeat. Mirrors the global field.
	SubPattern *string `yaml:"sub_pattern,omitempty" json:"sub_pattern,omitempty"`
}
