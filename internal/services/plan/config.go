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
	SizingPercentile           string         `yaml:"sizing_percentile"`
	HeadroomFraction           float64        `yaml:"headroom_fraction"`
	PrivateLinkSafetyThreshold float64        `yaml:"privatelink_safety_threshold"`
	SpikyWorkloadRatio         float64        `yaml:"spiky_workload_ratio"`
	SLAFloorECKU               map[string]int `yaml:"sla_floor_eCKU"`
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
	if c.EnterpriseCaps.PerECKUIngressMBps == 0 {
		return fmt.Errorf("plan-config enterprise_caps.per_eCKU_ingress_mbps is required")
	}
	if c.EnterpriseCaps.PerECKUEgressMBps == 0 {
		return fmt.Errorf("plan-config enterprise_caps.per_eCKU_egress_mbps is required")
	}
	if c.EnterpriseCaps.PerECKUPartitionRate == 0 {
		return fmt.Errorf("plan-config enterprise_caps.per_eCKU_partition_rate is required")
	}
	if c.EnterpriseCaps.PrivateLinkMaxECKU == 0 {
		return fmt.Errorf("plan-config enterprise_caps.privatelink_max_eCKU is required")
	}
	return nil
}
