package plan

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
)

// DecideNetworking applies the safety threshold for PrivateLink vs PNI.
//
//	Dedicated → PNI
//	Else PrivateLink if peak_burst_eCKU < threshold * privatelink_max_eCKU
//	Else PNI
func DecideNetworking(sizing types.ClusterSizing, ct types.ClusterTypeDecision, cfg *PlanConfig, inputs types.PlanInputsResolved) types.NetworkingDecision {
	caps := cfg.EnterpriseCaps
	threshold := inputs.PrivateLinkSafetyThreshold

	if ct.Verdict == types.ClusterTypeDedicated {
		return types.NetworkingDecision{
			ClusterID:       sizing.ClusterID,
			Verdict:         types.NetworkingPNI,
			PeakBurstECKU:   sizing.PeakBurstECKU,
			PercentageOfCap: sizing.PeakBurstPctOfPLCap,
			Reason:          "Dedicated cluster — PNI required",
		}
	}

	thresholdECKU := threshold * float64(caps.PrivateLinkMaxECKU)
	if float64(sizing.PeakBurstECKU) < thresholdECKU {
		return types.NetworkingDecision{
			ClusterID:       sizing.ClusterID,
			Verdict:         types.NetworkingPrivateLink,
			PeakBurstECKU:   sizing.PeakBurstECKU,
			PercentageOfCap: sizing.PeakBurstPctOfPLCap,
			Reason:          fmt.Sprintf("Peak burst %d eCKU below %.0f%% of %d eCKU PrivateLink cap", sizing.PeakBurstECKU, threshold*100, caps.PrivateLinkMaxECKU),
		}
	}
	return types.NetworkingDecision{
		ClusterID:       sizing.ClusterID,
		Verdict:         types.NetworkingPNI,
		PeakBurstECKU:   sizing.PeakBurstECKU,
		PercentageOfCap: sizing.PeakBurstPctOfPLCap,
		Reason:          fmt.Sprintf("Peak burst %d eCKU at %.0f%% of PrivateLink cap — exceeds %.0f%% safety threshold", sizing.PeakBurstECKU, sizing.PeakBurstPctOfPLCap, threshold*100),
	}
}
