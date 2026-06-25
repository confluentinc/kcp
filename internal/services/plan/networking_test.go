package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecideNetworking_DedicatedDefaultsToPNI(t *testing.T) {
	// Dedicated + no `existing_vpc_connectivity` override → PNI.
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", PeakBurstECKU: 1, PeakBurstPctOfPLCap: 10}
	ct := ClusterTypeDecision{ClusterID: "x", Verdict: ClusterTypeDedicated}
	d := decideNetworking(sizing, ct, cfg, defaultInputs())
	assert.Equal(t, NetworkingPNI, d.Verdict)
	assert.Contains(t, d.Reason, "Dedicated")
}

func TestDecideNetworking_DedicatedTransitGateway(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", PeakBurstECKU: 1, PeakBurstPctOfPLCap: 10}
	ct := ClusterTypeDecision{ClusterID: "x", Verdict: ClusterTypeDedicated}
	in := defaultInputs()
	in.ExistingVPCConnectivity = "transit_gateway"
	d := decideNetworking(sizing, ct, cfg, in)
	assert.Equal(t, NetworkingTransitGateway, d.Verdict)
	assert.Contains(t, d.Reason, "transit_gateway")
}

func TestDecideNetworking_DedicatedVPCPeering(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", PeakBurstECKU: 1, PeakBurstPctOfPLCap: 10}
	ct := ClusterTypeDecision{ClusterID: "x", Verdict: ClusterTypeDedicated}
	in := defaultInputs()
	in.ExistingVPCConnectivity = "vpc_peering"
	d := decideNetworking(sizing, ct, cfg, in)
	assert.Equal(t, NetworkingVPCPeering, d.Verdict)
	assert.Contains(t, d.Reason, "vpc_peering")
}

func TestDecideNetworking_EnterpriseDefaultsToPNI(t *testing.T) {
	// AWS Enterprise default verdict is PNI when no trigger fires.
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", PeakBurstECKU: 1, PeakBurstPctOfPLCap: 10}
	ct := ClusterTypeDecision{ClusterID: "x", Verdict: ClusterTypeEnterprise}
	d := decideNetworking(sizing, ct, cfg, defaultInputs())
	assert.Equal(t, NetworkingPNI, d.Verdict)
	assert.Contains(t, d.Reason, "default for AWS Enterprise")
}

func TestDecideNetworking_EnterpriseIgnoresVPCConnectivity(t *testing.T) {
	// TGW / VPC Peering are Dedicated-only products. An Enterprise cluster
	// with `existing_vpc_connectivity: transit_gateway` falls back to the
	// PNI-default flow, not TGW.
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", PeakBurstECKU: 1, PeakBurstPctOfPLCap: 10}
	ct := ClusterTypeDecision{ClusterID: "x", Verdict: ClusterTypeEnterprise}
	in := defaultInputs()
	in.ExistingVPCConnectivity = "transit_gateway"
	d := decideNetworking(sizing, ct, cfg, in)
	assert.Equal(t, NetworkingPNI, d.Verdict)
}

func TestDecideNetworking_EnterpriseCrossCloudFlipsToPrivateLink(t *testing.T) {
	// PNI is AWS-to-AWS only — non-AWS target lands on PrivateLink.
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", PeakBurstECKU: 1, PeakBurstPctOfPLCap: 10}
	ct := ClusterTypeDecision{ClusterID: "x", Verdict: ClusterTypeEnterprise}
	in := defaultInputs()
	in.TargetCloud = "azure"
	d := decideNetworking(sizing, ct, cfg, in)
	assert.Equal(t, NetworkingPrivateLink, d.Verdict)
	assert.Contains(t, d.Reason, "AWS-to-AWS only")
}

func TestDecideNetworking_EnterpriseCCEgressRequiredFlipsToPrivateLink(t *testing.T) {
	// CCEgressRequired=true → PrivateLink (PNI lacks native CC→customer egress).
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", PeakBurstECKU: 1, PeakBurstPctOfPLCap: 10}
	ct := ClusterTypeDecision{ClusterID: "x", Verdict: ClusterTypeEnterprise}
	in := defaultInputs()
	in.CCEgressRequired = true
	d := decideNetworking(sizing, ct, cfg, in)
	assert.Equal(t, NetworkingPrivateLink, d.Verdict)
	assert.Contains(t, d.Reason, "cc_egress_required")
}

func TestDecideNetworking_EnterpriseTwoPNIGatewaysFlipsToPrivateLink(t *testing.T) {
	// ≥2 PNI gateways → PrivateLink (per-gateway PNI cost crosses breakeven).
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", PeakBurstECKU: 1, PeakBurstPctOfPLCap: 10}
	ct := ClusterTypeDecision{ClusterID: "x", Verdict: ClusterTypeEnterprise}
	in := defaultInputs()
	in.ProjectedPNIGatewayCount = 2
	d := decideNetworking(sizing, ct, cfg, in)
	assert.Equal(t, NetworkingPrivateLink, d.Verdict)
	assert.Contains(t, d.Reason, "projected_pni_gateway_count=2")
}

func TestDecideNetworking_EnterpriseOneGatewayStaysPNI(t *testing.T) {
	// Single gateway is below the breakeven — PNI default.
	cfg := defaultCfg(t)
	sizing := ClusterSizing{ClusterID: "x", PeakBurstECKU: 1, PeakBurstPctOfPLCap: 10}
	ct := ClusterTypeDecision{ClusterID: "x", Verdict: ClusterTypeEnterprise}
	in := defaultInputs()
	in.ProjectedPNIGatewayCount = 1
	d := decideNetworking(sizing, ct, cfg, in)
	assert.Equal(t, NetworkingPNI, d.Verdict)
}
