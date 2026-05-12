package plan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestDecideNetworking_DedicatedAlwaysPNI(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := types.ClusterSizing{ClusterID: "x", PeakBurstECKU: 1, PeakBurstPctOfPLCap: 10}
	ct := types.ClusterTypeDecision{ClusterID: "x", Verdict: types.ClusterTypeDedicated}
	d := DecideNetworking(sizing, ct, cfg, defaultInputs())
	assert.Equal(t, types.NetworkingPNI, d.Verdict)
	assert.Contains(t, d.Reason, "Dedicated")
}

func TestDecideNetworking_PrivateLinkBelowThreshold(t *testing.T) {
	// PrivateLink cap = 10, threshold 0.80 → cap-7 fits, cap-8 doesn't.
	cfg := defaultCfg(t)
	sizing := types.ClusterSizing{ClusterID: "x", PeakBurstECKU: 7, PeakBurstPctOfPLCap: 70}
	ct := types.ClusterTypeDecision{ClusterID: "x", Verdict: types.ClusterTypeEnterprise}
	d := DecideNetworking(sizing, ct, cfg, defaultInputs())
	assert.Equal(t, types.NetworkingPrivateLink, d.Verdict)
}

func TestDecideNetworking_PNIWhenOverThreshold(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := types.ClusterSizing{ClusterID: "x", PeakBurstECKU: 9, PeakBurstPctOfPLCap: 90}
	ct := types.ClusterTypeDecision{ClusterID: "x", Verdict: types.ClusterTypeEnterprise}
	d := DecideNetworking(sizing, ct, cfg, defaultInputs())
	assert.Equal(t, types.NetworkingPNI, d.Verdict)
	assert.Contains(t, d.Reason, "exceeds")
}

func TestDecideNetworking_BoundaryAt80Percent(t *testing.T) {
	// At exactly 80% of cap (8 eCKU of 10), the rule is "less than" — flips
	// to PNI. Lock the boundary.
	cfg := defaultCfg(t)
	sizing := types.ClusterSizing{ClusterID: "x", PeakBurstECKU: 8, PeakBurstPctOfPLCap: 80}
	ct := types.ClusterTypeDecision{ClusterID: "x", Verdict: types.ClusterTypeEnterprise}
	d := DecideNetworking(sizing, ct, cfg, defaultInputs())
	assert.Equal(t, types.NetworkingPNI, d.Verdict)
}
