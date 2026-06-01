package plan

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

//go:embed plan-config.yaml
var embeddedPlanConfig []byte

// ExpectedSchemaVersion is the schema_version this loader understands.
// Bump in lockstep with breaking YAML structure changes.
const ExpectedSchemaVersion = 1

// PlanConfig is the deserialized plan-config.yaml. The embedded copy is
// the default; an admin-supplied override file replaces only the fields
// it specifies (standard yaml unmarshal semantics — slices replace whole,
// maps merge by key).
type PlanConfig struct {
	SchemaVersion            int    `yaml:"schema_version"`
	LastVerified             string `yaml:"last_verified"`
	KCPVersionAtVerification string `yaml:"kcp_version_at_verification"`

	EnterpriseCaps    EnterpriseCaps         `yaml:"enterprise_caps"`
	ClusterLinking    ClusterLinking         `yaml:"cluster_linking"`
	SchemaLinking     SchemaLinking          `yaml:"schema_linking"`
	PlanInputDefaults PlanInputDefaults      `yaml:"plan_input_defaults"`
	AuthMapping       map[string]AuthMapping `yaml:"auth_mapping"`
	Thresholds        Thresholds             `yaml:"thresholds"`
}

// Thresholds collects numeric cutoffs that the rule engine and the
// renderer need but that aren't customer-facing knobs — pulled out
// so an admin can tune them without code edits if a tenant has a
// genuinely different operating envelope.
type Thresholds struct {
	// StaleStateDays — emit the state-file-stale OQ when the state
	// snapshot is older than this many days.
	StaleStateDays int `yaml:"stale_state_days"`
	// PNIGatewayBreakeven — projected PNI gateway count at or above
	// which the recommendation flips from PNI to PrivateLink.
	PNIGatewayBreakeven int `yaml:"pni_gateway_breakeven"`
}

// AuthMapping is one row in the source→target auth lookup table
// keyed by source-auth token (scram / iam / mtls / unauth). Defaults
// resolve via this table when the customer doesn't override via
// `target_auth_method`. Source + LastVerified carry the row's
// provenance (which Confluent doc the values come from, and when an
// engineer last checked it).
type AuthMapping struct {
	Target            string `yaml:"target"`
	GatewayCompatible bool   `yaml:"gateway_compatible"`
	TransparentSwap   bool   `yaml:"transparent_swap"`
	Note              string `yaml:"note"`
	Source            string `yaml:"source"`
	LastVerified      string `yaml:"last_verified"`
}

type EnterpriseCaps struct {
	PerECKUIngressMBps   int    `yaml:"per_eCKU_ingress_mbps"`
	PerECKUEgressMBps    int    `yaml:"per_eCKU_egress_mbps"`
	PerECKUPartitionRate int    `yaml:"per_eCKU_partition_rate"`
	PrivateLinkMaxECKU   int    `yaml:"privatelink_max_eCKU"`
	PNIMaxECKU           int    `yaml:"pni_max_eCKU"`
	ACLCountCap          int    `yaml:"acl_count_cap"`
	Source               string `yaml:"source"`
}

type ClusterLinking struct {
	SourceMinKafkaVersion string `yaml:"source_min_kafka_version"`
	ExpressTierSupported  string `yaml:"express_tier_supported"`
	Source                string `yaml:"source"`
}

type PlanInputDefaults struct {
	SizingPercentile   string         `yaml:"sizing_percentile"`
	HeadroomFraction   float64        `yaml:"headroom_fraction"`
	SpikyWorkloadRatio float64        `yaml:"spiky_workload_ratio"`
	SLAFloorECKU       map[string]int `yaml:"sla_floor_eCKU"`

	// Customer-declared hard requirements (all default false).
	EnforceSchemasAtTheBroker            bool `yaml:"enforce_schemas_at_the_broker"`
	RequiresHighThroughputRESTProduceAPI bool `yaml:"requires_high_throughput_rest_produce_api"`
	Requires9995SLAWithinSingleZone      bool `yaml:"requires_99_95_sla_within_a_single_zone"`

	// Target cloud + existing VPC connectivity (Dedicated-path networking).
	TargetCloud             string `yaml:"target_cloud"`
	ExistingVPCConnectivity string `yaml:"existing_vpc_connectivity"`

	// Networking triggers (PNI→PrivateLink) — see PlanInputsResolved.
	CCEgressRequired         bool `yaml:"cc_egress_required"`
	ProjectedPNIGatewayCount int  `yaml:"projected_pni_gateway_count"`

	// Cutover defaults.
	DowntimeTolerance            string `yaml:"downtime_tolerance"`
	SubPattern                   string `yaml:"sub_pattern"`
	PreferGateway                bool   `yaml:"prefer_gateway"`
	ConfluentForKubernetesStatus string `yaml:"confluent_for_kubernetes_status"`
	CCGatewayLicenseStatus       string `yaml:"cc_gateway_license_status"`
	IAMPreMigrationStatus        string `yaml:"iam_pre_migration_status"`

	// Auth defaults.
	TargetAuthMethod string `yaml:"target_auth_method"`

	// Schema-migration defaults. Defaults below resolve to
	// `unknown` so first-run plans always ask the customer to declare
	// strategy + reachability + CP version/edition rather than silently
	// picking a path.
	SchemaStrategy       string `yaml:"schema_strategy"`
	ConfluentSRCPVersion string `yaml:"confluent_sr_cp_version,omitempty"`
	ConfluentSRCPEdition string `yaml:"confluent_sr_cp_edition,omitempty"`
}

// SchemaLinking pins the version + edition floor that source Confluent
// SR must clear for the Schema Linking path. Customers below these
// floors get the `defer_to_account_team` verdict (REST API export /
// import is technically possible but kcp doesn't drive it). The values
// come from the [Schema Linking on CP docs](https://docs.confluent.io/platform/current/schema-registry/schema-linking-cp.html);
// last-verified date below tracks when an engineer cross-checked them.
type SchemaLinking struct {
	MinCPVersion      string `yaml:"min_cp_version"`
	RequiresCPEdition string `yaml:"requires_cp_edition"`
	Source            string `yaml:"source"`
	LastVerified      string `yaml:"last_verified"`
}

// LoadPlanConfig returns the embedded plan-config.yaml, optionally
// overridden by the file at overridePath. Pass an empty string to skip
// the override.
func LoadPlanConfig(overridePath string) (*PlanConfig, error) {
	cfg := &PlanConfig{}
	if err := yaml.Unmarshal(embeddedPlanConfig, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse embedded plan-config.yaml: %w", err)
	}

	if overridePath != "" {
		data, err := os.ReadFile(overridePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read plan-config override %s: %w", overridePath, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse plan-config override %s: %w", overridePath, err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *PlanConfig) Validate() error {
	if c.SchemaVersion != ExpectedSchemaVersion {
		return fmt.Errorf("plan-config schema_version %d does not match expected %d", c.SchemaVersion, ExpectedSchemaVersion)
	}
	caps := c.EnterpriseCaps
	if caps.PerECKUIngressMBps <= 0 {
		return fmt.Errorf("plan-config enterprise_caps.per_eCKU_ingress_mbps must be > 0")
	}
	if caps.PerECKUEgressMBps <= 0 {
		return fmt.Errorf("plan-config enterprise_caps.per_eCKU_egress_mbps must be > 0")
	}
	if caps.PerECKUPartitionRate <= 0 {
		return fmt.Errorf("plan-config enterprise_caps.per_eCKU_partition_rate must be > 0")
	}
	if caps.PrivateLinkMaxECKU <= 0 {
		return fmt.Errorf("plan-config enterprise_caps.privatelink_max_eCKU must be > 0")
	}
	// pni_max_eCKU is load-bearing for the Enterprise→Dedicated rule; a 0
	// here would force every non-zero FinalECKU to trip the cap.
	if caps.PNIMaxECKU <= 0 {
		return fmt.Errorf("plan-config enterprise_caps.pni_max_eCKU must be > 0")
	}
	defaults := c.PlanInputDefaults
	if defaults.HeadroomFraction < 0 || defaults.HeadroomFraction > 1 {
		return fmt.Errorf("plan-config plan_input_defaults.headroom_fraction must be in [0, 1] (got %v)", defaults.HeadroomFraction)
	}
	if defaults.SpikyWorkloadRatio <= 1 {
		return fmt.Errorf("plan-config plan_input_defaults.spiky_workload_ratio must be > 1 (got %v) — a spike must be larger than the baseline", defaults.SpikyWorkloadRatio)
	}
	if defaults.ProjectedPNIGatewayCount < 1 {
		return fmt.Errorf("plan-config plan_input_defaults.projected_pni_gateway_count must be >= 1 (got %v)", defaults.ProjectedPNIGatewayCount)
	}
	if c.SchemaLinking.MinCPVersion == "" {
		return fmt.Errorf("plan-config schema_linking.min_cp_version must be non-empty")
	}
	if c.SchemaLinking.RequiresCPEdition == "" {
		return fmt.Errorf("plan-config schema_linking.requires_cp_edition must be non-empty")
	}
	if c.SchemaLinking.Source == "" {
		return fmt.Errorf("plan-config schema_linking.source must be non-empty (provenance is mandatory)")
	}
	if c.SchemaLinking.LastVerified == "" {
		return fmt.Errorf("plan-config schema_linking.last_verified must be non-empty (provenance is mandatory)")
	}
	if c.Thresholds.StaleStateDays < 1 {
		return fmt.Errorf("plan-config thresholds.stale_state_days must be >= 1 (got %v)", c.Thresholds.StaleStateDays)
	}
	if c.Thresholds.PNIGatewayBreakeven < 1 {
		return fmt.Errorf("plan-config thresholds.pni_gateway_breakeven must be >= 1 (got %v)", c.Thresholds.PNIGatewayBreakeven)
	}
	// Every auth_mapping entry MUST carry Target + provenance (Source +
	// LastVerified). The fields exist so the rendered Plan can audit
	// where each recommendation came from — a silently-empty mapping
	// row would propagate as a blank Plan footnote.
	requiredSources := []string{"scram", "iam", "mtls", "unauth"}
	for _, s := range requiredSources {
		row, ok := c.AuthMapping[s]
		if !ok {
			return fmt.Errorf("plan-config auth_mapping is missing required entry %q", s)
		}
		if row.Target == "" {
			return fmt.Errorf("plan-config auth_mapping[%s].target must be non-empty", s)
		}
		if row.Source == "" {
			return fmt.Errorf("plan-config auth_mapping[%s].source must be non-empty (provenance is mandatory)", s)
		}
		if row.LastVerified == "" {
			return fmt.Errorf("plan-config auth_mapping[%s].last_verified must be non-empty (provenance is mandatory)", s)
		}
	}
	return nil
}
