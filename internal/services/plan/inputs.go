package plan

import (
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/goccy/go-yaml"
)

const defaultSLATarget = "99.9"

// LoadPlanInputs reads plan-inputs.yaml. Returns nil *PlanInputs if path
// is empty (no file supplied is a valid state — the Plan still generates
// from defaults). Missing fields never fail because no field is required
// at the YAML schema level.
func LoadPlanInputs(path string) (*types.PlanInputs, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read plan-inputs %s: %w", path, err)
	}
	var in types.PlanInputs
	if err := yaml.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("failed to parse plan-inputs %s: %w", path, err)
	}
	return &in, nil
}

// ResolvePlanInputs merges customer-supplied PlanInputs with pinned
// defaults from PlanConfig. Customer-set fields win; everything else
// falls back to PlanConfig.PlanInputDefaults. Raw is preserved so
// downstream consumers can detect which sizing fields the customer
// explicitly set (HeadroomFraction, SLATarget, SizingPercentile, etc.).
func ResolvePlanInputs(in *types.PlanInputs, cfg *PlanConfig) types.PlanInputsResolved {
	defaults := cfg.PlanInputDefaults
	out := types.PlanInputsResolved{
		Raw:                                  in,
		SizingPercentile:                     defaults.SizingPercentile,
		HeadroomFraction:                     defaults.HeadroomFraction,
		PrivateLinkSafetyThreshold:           defaults.PrivateLinkSafetyThreshold,
		SpikyWorkloadRatio:                   defaults.SpikyWorkloadRatio,
		SLATarget:                            defaultSLATarget,
		EnforceSchemasAtTheBroker:            defaults.EnforceSchemasAtTheBroker,
		RequiresHighThroughputRESTProduceAPI: defaults.RequiresHighThroughputRESTProduceAPI,
		Requires9995SLAWithinSingleZone:      defaults.Requires9995SLAWithinSingleZone,
		TargetCloud:                          defaults.TargetCloud,
		ExistingVPCConnectivity:              defaults.ExistingVPCConnectivity,
	}
	if in == nil {
		return out
	}
	if in.SLATarget != nil {
		out.SLATarget = *in.SLATarget
	}
	if in.SizingPercentile != nil {
		out.SizingPercentile = *in.SizingPercentile
	}
	if in.HeadroomFraction != nil {
		out.HeadroomFraction = *in.HeadroomFraction
	}
	if in.PrivateLinkSafetyThreshold != nil {
		out.PrivateLinkSafetyThreshold = *in.PrivateLinkSafetyThreshold
	}
	if in.SpikyWorkloadRatio != nil {
		out.SpikyWorkloadRatio = *in.SpikyWorkloadRatio
	}
	if in.EnforceSchemasAtTheBroker != nil {
		out.EnforceSchemasAtTheBroker = *in.EnforceSchemasAtTheBroker
	}
	if in.RequiresHighThroughputRESTProduceAPI != nil {
		out.RequiresHighThroughputRESTProduceAPI = *in.RequiresHighThroughputRESTProduceAPI
	}
	if in.Requires9995SLAWithinSingleZone != nil {
		out.Requires9995SLAWithinSingleZone = *in.Requires9995SLAWithinSingleZone
	}
	if in.TargetCloud != nil {
		out.TargetCloud = *in.TargetCloud
	}
	if in.ExistingVPCConnectivity != nil {
		out.ExistingVPCConnectivity = *in.ExistingVPCConnectivity
	}
	return out
}
