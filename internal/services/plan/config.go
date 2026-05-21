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

	EnterpriseCaps    EnterpriseCaps    `yaml:"enterprise_caps"`
	ClusterLinking    ClusterLinking    `yaml:"cluster_linking"`
	PlanInputDefaults PlanInputDefaults `yaml:"plan_input_defaults"`
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
	return nil
}
