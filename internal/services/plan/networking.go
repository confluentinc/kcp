package plan

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
)

// Customer-set values of `existing_vpc_connectivity` that select a
// Dedicated-only networking option matching the customer's current MSK
// topology (path of least resistance).
const (
	vpcConnectivityTransitGateway = "transit_gateway"
	vpcConnectivityVPCPeering     = "vpc_peering"
)

// DecideNetworking picks the Confluent Cloud networking product for one
// cluster.
//
//	Dedicated + existing_vpc_connectivity == transit_gateway → Transit Gateway
//	Dedicated + existing_vpc_connectivity == vpc_peering     → VPC Peering
//	Dedicated (otherwise)                                    → PNI
//	Enterprise + peak_burst < threshold × privatelink_max_eCKU → PrivateLink
//	Enterprise (otherwise)                                   → PNI
//
// `existing_vpc_connectivity` is only honored on the Dedicated path —
// Transit Gateway and VPC Peering are Dedicated-only products. On
// Enterprise the PrivateLink-vs-PNI flip remains driven by the safety
// threshold.
func DecideNetworking(sizing types.ClusterSizing, ct types.ClusterTypeDecision, cfg *PlanConfig, inputs types.PlanInputsResolved) types.NetworkingDecision {
	caps := cfg.EnterpriseCaps
	threshold := inputs.PrivateLinkSafetyThreshold

	if ct.Verdict == types.ClusterTypeDedicated {
		switch inputs.ExistingVPCConnectivity {
		case vpcConnectivityTransitGateway:
			return types.NetworkingDecision{
				ClusterID:       sizing.ClusterID,
				Verdict:         types.NetworkingTransitGateway,
				PeakBurstECKU:   sizing.PeakBurstECKU,
				PercentageOfCap: sizing.PeakBurstPctOfPLCap,
				Reason:          "Dedicated cluster + existing_vpc_connectivity=transit_gateway — match the customer's source topology",
			}
		case vpcConnectivityVPCPeering:
			return types.NetworkingDecision{
				ClusterID:       sizing.ClusterID,
				Verdict:         types.NetworkingVPCPeering,
				PeakBurstECKU:   sizing.PeakBurstECKU,
				PercentageOfCap: sizing.PeakBurstPctOfPLCap,
				Reason:          "Dedicated cluster + existing_vpc_connectivity=vpc_peering — match the customer's source topology",
			}
		}
		return types.NetworkingDecision{
			ClusterID:       sizing.ClusterID,
			Verdict:         types.NetworkingPNI,
			PeakBurstECKU:   sizing.PeakBurstECKU,
			PercentageOfCap: sizing.PeakBurstPctOfPLCap,
			Reason:          "Dedicated cluster — PNI required (no TGW / VPC Peering override)",
		}
	}

	thresholdECKU := threshold * float64(caps.PrivateLinkMaxECKU)
	if float64(sizing.PeakBurstECKU) < thresholdECKU {
		return types.NetworkingDecision{
			ClusterID:       sizing.ClusterID,
			Verdict:         types.NetworkingPrivateLink,
			PeakBurstECKU:   sizing.PeakBurstECKU,
			PercentageOfCap: sizing.PeakBurstPctOfPLCap,
			Reason:          fmt.Sprintf("peak burst %d eCKU = %.0f%% of PrivateLink's %d eCKU cap (safety threshold %.0f%%)", sizing.PeakBurstECKU, sizing.PeakBurstPctOfPLCap, caps.PrivateLinkMaxECKU, threshold*100),
		}
	}
	return types.NetworkingDecision{
		ClusterID:       sizing.ClusterID,
		Verdict:         types.NetworkingPNI,
		PeakBurstECKU:   sizing.PeakBurstECKU,
		PercentageOfCap: sizing.PeakBurstPctOfPLCap,
		Reason:          fmt.Sprintf("peak burst %d eCKU = %.0f%% of PrivateLink's %d eCKU cap, over the %.0f%% safety threshold", sizing.PeakBurstECKU, sizing.PeakBurstPctOfPLCap, caps.PrivateLinkMaxECKU, threshold*100),
	}
}
