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

// privateLinkSizingExceedsCap reports whether the plan ended up
// recommending PrivateLink for an Enterprise cluster whose sizing
// exceeds the PrivateLink eCKU cap. This combination is infeasible: the
// PrivateLink triggers (target_cloud != "aws", cc_egress_required,
// projected_pni_gateway_count ≥ 2) ignore sizing, so a customer with
// the trigger AND a > 10-eCKU workload silently gets an
// over-cap recommendation. The plan still emits PrivateLink (to keep
// the verdict deterministic), but detectOpenQuestions surfaces an OQ so
// the customer can move to Dedicated (which supports PrivateLink at
// higher CKU).
func privateLinkSizingExceedsCap(sizing types.ClusterSizing, ct types.ClusterTypeDecision, net types.NetworkingDecision, cfg *PlanConfig) bool {
	if net.Verdict != types.NetworkingPrivateLink {
		return false
	}
	if ct.Verdict != types.ClusterTypeEnterprise {
		return false
	}
	return sizing.FinalECKU > cfg.EnterpriseCaps.PrivateLinkMaxECKU
}

// DecideNetworking picks the Confluent Cloud networking product for one
// cluster.
//
//	Dedicated + target_cloud != "aws"                        → PrivateLink (PNI / TGW / VPC Peering are AWS-only)
//	Dedicated + existing_vpc_connectivity == transit_gateway → Transit Gateway
//	Dedicated + existing_vpc_connectivity == vpc_peering     → VPC Peering
//	Dedicated (otherwise)                                    → PNI
//	Enterprise + target_cloud != "aws"                       → PrivateLink (PNI is AWS-to-AWS only)
//	Enterprise + cc_egress_required                          → PrivateLink (PNI lacks native CC→customer egress)
//	Enterprise + projected_pni_gateway_count ≥ 2             → PrivateLink
//	Enterprise (otherwise)                                   → PNI (default — scales to %d eCKU vs PrivateLink's %d)
//
// `existing_vpc_connectivity` is only honored on the AWS-Dedicated path —
// Transit Gateway and VPC Peering are Dedicated-only AWS products.
func DecideNetworking(sizing types.ClusterSizing, ct types.ClusterTypeDecision, cfg *PlanConfig, inputs types.PlanInputsResolved) types.NetworkingDecision {
	target := inputs.TargetCloud
	if target == "" {
		target = "aws"
	}

	if ct.Verdict == types.ClusterTypeDedicated {
		// PNI, Transit Gateway, and VPC Peering are AWS-only networking
		// products. Cross-cloud Dedicated lands on PrivateLink (the
		// generic private-network option supported across clouds).
		if target != "aws" {
			return types.NetworkingDecision{
				ClusterID:       sizing.ClusterID,
				Verdict:         types.NetworkingPrivateLink,
				PeakBurstECKU:   sizing.PeakBurstECKU,
				PercentageOfCap: sizing.PeakBurstPctOfPLCap,
				Reason:          fmt.Sprintf("target_cloud=%q — PNI / TGW / VPC Peering are AWS-only, so cross-cloud Dedicated lands on PrivateLink", target),
			}
		}
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
		Reason:          fmt.Sprintf("PNI — default for AWS Enterprise (scales to %d eCKU vs PrivateLink's %d-eCKU cap)", cfg.EnterpriseCaps.PNIMaxECKU, cfg.EnterpriseCaps.PrivateLinkMaxECKU),
	}
}
