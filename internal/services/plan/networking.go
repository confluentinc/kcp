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

// pniGatewayBreakeven is the projected-gateway count at which the
// recommendation flips from PNI to PrivateLink.
const pniGatewayBreakeven = 2

// DecideNetworking picks the Confluent Cloud networking product for one
// cluster.
//
//	Dedicated + existing_vpc_connectivity == transit_gateway → Transit Gateway
//	Dedicated + existing_vpc_connectivity == vpc_peering     → VPC Peering
//	Dedicated (otherwise)                                    → PNI
//	Enterprise + target_cloud != "aws"                       → PrivateLink (PNI is AWS-to-AWS only)
//	Enterprise + cc_egress_required                          → PrivateLink (PNI lacks native CC→customer egress)
//	Enterprise + projected_pni_gateway_count ≥ 2             → PrivateLink
//	Enterprise (otherwise)                                   → PNI (AWS-Enterprise default)
//
// `existing_vpc_connectivity` is only honored on the Dedicated path —
// Transit Gateway and VPC Peering are Dedicated-only products.
func DecideNetworking(sizing types.ClusterSizing, ct types.ClusterTypeDecision, cfg *PlanConfig, inputs types.PlanInputsResolved) types.NetworkingDecision {
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

	// Enterprise path: PNI is the default for AWS-to-AWS workloads. The
	// flip to PrivateLink only fires on one of three explicit triggers.
	target := inputs.TargetCloud
	if target == "" {
		target = "aws"
	}
	if target != "aws" {
		return types.NetworkingDecision{
			ClusterID:       sizing.ClusterID,
			Verdict:         types.NetworkingPrivateLink,
			PeakBurstECKU:   sizing.PeakBurstECKU,
			PercentageOfCap: sizing.PeakBurstPctOfPLCap,
			Reason:          fmt.Sprintf("target_cloud=%q — PNI is AWS-to-AWS only, so cross-cloud lands on PrivateLink", target),
		}
	}
	if inputs.CCEgressRequired {
		return types.NetworkingDecision{
			ClusterID:       sizing.ClusterID,
			Verdict:         types.NetworkingPrivateLink,
			PeakBurstECKU:   sizing.PeakBurstECKU,
			PercentageOfCap: sizing.PeakBurstPctOfPLCap,
			Reason:          "cc_egress_required=true — PNI does not natively support egress from CC into customer infrastructure",
		}
	}
	if inputs.ProjectedPNIGatewayCount >= pniGatewayBreakeven {
		return types.NetworkingDecision{
			ClusterID:       sizing.ClusterID,
			Verdict:         types.NetworkingPrivateLink,
			PeakBurstECKU:   sizing.PeakBurstECKU,
			PercentageOfCap: sizing.PeakBurstPctOfPLCap,
			Reason:          fmt.Sprintf("projected_pni_gateway_count=%d (≥ %d) — flip to PrivateLink", inputs.ProjectedPNIGatewayCount, pniGatewayBreakeven),
		}
	}
	return types.NetworkingDecision{
		ClusterID:       sizing.ClusterID,
		Verdict:         types.NetworkingPNI,
		PeakBurstECKU:   sizing.PeakBurstECKU,
		PercentageOfCap: sizing.PeakBurstPctOfPLCap,
		Reason:          "AWS-Enterprise default — PNI",
	}
}
