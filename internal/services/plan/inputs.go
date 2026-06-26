package plan

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

const defaultSLATarget = "99.9"

// LoadPlanInputs reads plan-inputs.yaml. Returns nil *PlanInputs if path
// is empty (no file supplied is a valid state — the Plan still generates
// from defaults). Missing fields never fail because no field is required
// at the YAML schema level.
func LoadPlanInputs(path string) (*PlanInputs, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read plan-inputs %s: %w", path, err)
	}
	var in PlanInputs
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
func ResolvePlanInputs(in *PlanInputs, cfg *PlanConfig) PlanInputsResolved {
	defaults := cfg.PlanInputDefaults
	out := PlanInputsResolved{
		Raw:                                  in,
		SizingPercentile:                     defaults.SizingPercentile,
		HeadroomFraction:                     defaults.HeadroomFraction,
		SpikyWorkloadRatio:                   defaults.SpikyWorkloadRatio,
		SLATarget:                            defaultSLATarget,
		EnforceSchemasAtTheBroker:            defaults.EnforceSchemasAtTheBroker,
		RequiresHighThroughputRESTProduceAPI: defaults.RequiresHighThroughputRESTProduceAPI,
		Requires9995SLAWithinSingleZone:      defaults.Requires9995SLAWithinSingleZone,
		TargetCloud:                          defaults.TargetCloud,
		ExistingVPCConnectivity:              defaults.ExistingVPCConnectivity,
		CCEgressRequired:                     defaults.CCEgressRequired,
		ProjectedPNIGatewayCount:             defaults.ProjectedPNIGatewayCount,
		DowntimeTolerance:                    defaults.DowntimeTolerance,
		SubPattern:                           defaults.SubPattern,
		PreferGateway:                        defaults.PreferGateway,
		ConfluentForKubernetesStatus:         defaults.ConfluentForKubernetesStatus,
		CCGatewayLicenseStatus:               defaults.CCGatewayLicenseStatus,
		IAMPreMigrationStatus:                defaults.IAMPreMigrationStatus,
		TargetAuthMethod:                     defaults.TargetAuthMethod,
		SchemaStrategy:                       defaults.SchemaStrategy,
		ConfluentSRCPVersion:                 defaults.ConfluentSRCPVersion,
		ConfluentSRCPEdition:                 defaults.ConfluentSRCPEdition,
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
	if in.CCEgressRequired != nil {
		out.CCEgressRequired = *in.CCEgressRequired
	}
	if in.ProjectedPNIGatewayCount != nil {
		out.ProjectedPNIGatewayCount = *in.ProjectedPNIGatewayCount
	}
	if in.DowntimeTolerance != nil {
		out.DowntimeTolerance = *in.DowntimeTolerance
	}
	if in.SubPattern != nil {
		out.SubPattern = *in.SubPattern
	}
	if in.PreferGateway != nil {
		out.PreferGateway = *in.PreferGateway
	}
	if in.ConfluentForKubernetesStatus != nil {
		out.ConfluentForKubernetesStatus = *in.ConfluentForKubernetesStatus
	}
	if in.CCGatewayLicenseStatus != nil {
		out.CCGatewayLicenseStatus = *in.CCGatewayLicenseStatus
	}
	if in.IAMPreMigrationStatus != nil {
		out.IAMPreMigrationStatus = *in.IAMPreMigrationStatus
	}
	if in.TargetAuthMethod != nil {
		out.TargetAuthMethod = *in.TargetAuthMethod
	}
	if in.SchemaStrategy != nil {
		out.SchemaStrategy = *in.SchemaStrategy
	}
	if in.SourceSROutboundReachableToCC != nil {
		// Copy the value (not the pointer) so a later mutation of the
		// caller-owned PlanInputs doesn't bleed into Resolved.
		v := *in.SourceSROutboundReachableToCC
		out.SourceSROutboundReachableToCC = &v
	}
	if in.ConfluentSRCPVersion != nil {
		out.ConfluentSRCPVersion = *in.ConfluentSRCPVersion
	}
	if in.ConfluentSRCPEdition != nil {
		out.ConfluentSRCPEdition = *in.ConfluentSRCPEdition
	}
	if in.ExactlyOnceTransactionsInUse != nil {
		v := *in.ExactlyOnceTransactionsInUse
		out.ExactlyOnceTransactionsInUse = &v
	}
	if in.KafkaStreamsInUse != nil {
		v := *in.KafkaStreamsInUse
		out.KafkaStreamsInUse = &v
	}
	if in.ConsumerHistoryRequirement != nil {
		out.ConsumerHistoryRequirement = *in.ConsumerHistoryRequirement
	}
	if in.HistoricalDataStrategy != nil {
		out.HistoricalDataStrategy = *in.HistoricalDataStrategy
	}
	return out
}

// ResolvePlanInputsForCluster layers a cluster-specific override on top
// of the resolved globals. If `in.Clusters[clusterName]` is set, every
// non-nil field in it wins over the global. Otherwise the global view
// is returned unchanged. Caller uses this per-cluster during Plan
// build so heterogeneous fleets get the right verdicts without one
// global flag flipping every cluster's tier.
//
// **Hot path note:** when iterating many clusters, prefer
// `applyClusterOverride` against a pre-resolved global view — this
// function re-resolves globals on every call.
func ResolvePlanInputsForCluster(in *PlanInputs, cfg *PlanConfig, clusterName string) PlanInputsResolved {
	out := ResolvePlanInputs(in, cfg)
	return applyClusterOverride(out, in, clusterName)
}

// applyClusterOverride takes an already-resolved global `PlanInputsResolved`
// and layers any per-cluster override on top. Returns the global
// unchanged when no override applies. Used by `PlanService.Build` to
// avoid re-running the (cheap but non-trivial) global resolution
// against `in.Raw` for every cluster — the global view is computed
// once and reused.
func applyClusterOverride(out PlanInputsResolved, in *PlanInputs, clusterName string) PlanInputsResolved {
	if in == nil {
		return out
	}
	override, ok := in.Clusters[clusterName]
	if !ok {
		return out
	}
	if override.SLATarget != nil {
		out.SLATarget = *override.SLATarget
	}
	if override.HeadroomFraction != nil {
		out.HeadroomFraction = *override.HeadroomFraction
	}
	if override.SpikyWorkloadRatio != nil {
		out.SpikyWorkloadRatio = *override.SpikyWorkloadRatio
	}
	if override.EnforceSchemasAtTheBroker != nil {
		out.EnforceSchemasAtTheBroker = *override.EnforceSchemasAtTheBroker
	}
	if override.RequiresHighThroughputRESTProduceAPI != nil {
		out.RequiresHighThroughputRESTProduceAPI = *override.RequiresHighThroughputRESTProduceAPI
	}
	if override.Requires9995SLAWithinSingleZone != nil {
		out.Requires9995SLAWithinSingleZone = *override.Requires9995SLAWithinSingleZone
	}
	if override.TargetCloud != nil {
		out.TargetCloud = *override.TargetCloud
	}
	if override.ExistingVPCConnectivity != nil {
		out.ExistingVPCConnectivity = *override.ExistingVPCConnectivity
	}
	if override.CCEgressRequired != nil {
		out.CCEgressRequired = *override.CCEgressRequired
	}
	if override.ProjectedPNIGatewayCount != nil {
		out.ProjectedPNIGatewayCount = *override.ProjectedPNIGatewayCount
	}
	if override.TargetAuthMethod != nil {
		out.TargetAuthMethod = *override.TargetAuthMethod
	}
	if override.DowntimeTolerance != nil {
		out.DowntimeTolerance = *override.DowntimeTolerance
	}
	if override.SubPattern != nil {
		out.SubPattern = *override.SubPattern
	}
	return out
}
