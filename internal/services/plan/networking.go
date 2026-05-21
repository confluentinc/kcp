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

// netDecision constructs a NetworkingDecision from the sizing context
// (peak burst, percentage-of-PL-cap) plus the verdict and reason. Used
// to keep the 8 decision branches below uniform — every branch must
// surface peak-burst data for the renderer / OQ detector to consume.
func netDecision(sizing types.ClusterSizing, verdict types.Networking, reason string) types.NetworkingDecision {
	return types.NetworkingDecision{
		ClusterID:       sizing.ClusterID,
		Verdict:         verdict,
		PeakBurstECKU:   sizing.PeakBurstECKU,
		PercentageOfCap: sizing.PeakBurstPctOfPLCap,
		Reason:          reason,
	}
}

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
//	Enterprise (otherwise)                                   → PNI (default — scales to 32 eCKU vs PrivateLink's 10)
//
// `existing_vpc_connectivity` is only honored on the AWS-Dedicated path —
// Transit Gateway and VPC Peering are Dedicated-only AWS products.
func DecideNetworking(sizing types.ClusterSizing, ct types.ClusterTypeDecision, cfg *PlanConfig, inputs types.PlanInputsResolved) types.NetworkingDecision {
	target := targetCloud(inputs)

	if ct.Verdict == types.ClusterTypeDedicated {
		// PNI, Transit Gateway, and VPC Peering are AWS-only networking
		// products. Cross-cloud Dedicated lands on PrivateLink (the
		// generic private-network option supported across clouds).
		if target != defaultTargetCloud {
			return netDecision(sizing, types.NetworkingPrivateLink,
				fmt.Sprintf("target_cloud=%q — PNI / TGW / VPC Peering are AWS-only, so cross-cloud Dedicated lands on PrivateLink (set `target_cloud: aws` in `plan-inputs.yaml` to undo)", target))
		}
		switch inputs.ExistingVPCConnectivity {
		case vpcConnectivityTransitGateway:
			return netDecision(sizing, types.NetworkingTransitGateway,
				"Dedicated cluster + existing_vpc_connectivity=transit_gateway — match the customer's source topology")
		case vpcConnectivityVPCPeering:
			return netDecision(sizing, types.NetworkingVPCPeering,
				"Dedicated cluster + existing_vpc_connectivity=vpc_peering — match the customer's source topology")
		}
		return netDecision(sizing, types.NetworkingPNI,
			"Dedicated cluster — PNI default (set `existing_vpc_connectivity: transit_gateway` or `vpc_peering` in plan-inputs.yaml to switch to TGW / VPC Peering)")
	}

	// Enterprise path: PNI is the default for AWS-to-AWS workloads. The
	// flip to PrivateLink only fires on one of three explicit triggers.
	if target != defaultTargetCloud {
		return netDecision(sizing, types.NetworkingPrivateLink,
			fmt.Sprintf("target_cloud=%q — PNI is AWS-to-AWS only, so cross-cloud lands on PrivateLink (set `target_cloud: aws` in `plan-inputs.yaml` to undo)", target))
	}
	if inputs.CCEgressRequired {
		return netDecision(sizing, types.NetworkingPrivateLink,
			"cc_egress_required=true — PNI does not natively support egress from CC into customer infrastructure (set `cc_egress_required: false` in `plan-inputs.yaml` if this wasn't intentional)")
	}
	if inputs.ProjectedPNIGatewayCount >= pniGatewayBreakeven {
		return netDecision(sizing, types.NetworkingPrivateLink,
			fmt.Sprintf("projected_pni_gateway_count=%d (≥ %d) — flip to PrivateLink (lower `projected_pni_gateway_count` in `plan-inputs.yaml` to stay on PNI)", inputs.ProjectedPNIGatewayCount, pniGatewayBreakeven))
	}
	return netDecision(sizing, types.NetworkingPNI,
		fmt.Sprintf("PNI — default for AWS Enterprise (scales to %d eCKU vs PrivateLink's %d-eCKU cap)", cfg.EnterpriseCaps.PNIMaxECKU, cfg.EnterpriseCaps.PrivateLinkMaxECKU))
}
