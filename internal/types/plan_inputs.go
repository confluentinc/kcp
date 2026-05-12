package types

// PlanInputs is the parsed plan-inputs.yaml. Pointers distinguish "unset"
// from the zero value so the loader can echo only the fields the customer
// explicitly set.
//
// MVP scope: only the fields that drive sizing. Customer-self-reported
// hard-requirement flags (VPC peering, REST Produce v3, single-zone SLA)
// were intentionally excluded — the goal is "deterministic from the
// state file"; reintroduce them only alongside state-file-derived
// detection so a YAML toggle can't flip the verdict without evidence.
type PlanInputs struct {
	// Sizing overrides (defaults live in plan-config.yaml).
	SLATarget                  *string  `yaml:"sla_target,omitempty"`
	SizingPercentile           *string  `yaml:"sizing_percentile,omitempty"`
	HeadroomFraction           *float64 `yaml:"headroom_fraction,omitempty"`
	PrivateLinkSafetyThreshold *float64 `yaml:"privatelink_safety_threshold,omitempty"`
	SpikyWorkloadRatio         *float64 `yaml:"spiky_workload_ratio,omitempty"`
}
