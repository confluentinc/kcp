package engine

import (
	"strings"
	"testing"
)

// netOf mirrors the wiring computePlan uses for a first-pass networking verdict:
// size -> tentative cluster type -> networking (tc, band, dedicated). It does not
// pass crossedToPrivate, matching the tentative call.
func netOf(mut func(*Profile)) NetworkingResult {
	p := baseProfile(mut)
	s := sizingBand(p)
	tc := targetCloud(p)
	ct := clusterType(p, s, tc, nil)
	return networkingDecision(p, netCtx{TC: tc, Band: s.Band, Dedicated: ct.Dedicated})
}

func TestNetworking_PublicSource(t *testing.T) {
	n := netOf(func(p *Profile) { p.SourceAccessibility = "Public" })
	if n.Value != "Public endpoint" {
		t.Errorf("public source: value=%q, want Public endpoint", n.Value)
	}
	if n.Action != nil {
		t.Errorf("public endpoint action=%v, want nil", *n.Action)
	}
}

func TestNetworking_AWSEnterprisePNIDefault(t *testing.T) {
	// Band 2 base, and Band 2 with no preference, both land on PNI with no escalation.
	for _, mut := range []func(*Profile){nil, func(p *Profile) { p.PartitionBand = "2,500–30,000" }} {
		n := netOf(mut)
		if n.Value != "PNI" || n.ForcesDedicated {
			t.Errorf("AWS Enterprise default: value=%q forcesDedicated=%v, want PNI / false", n.Value, n.ForcesDedicated)
		}
	}
}

func TestNetworking_LegacyRoutedFieldsInert(t *testing.T) {
	// Topology hints no longer produce routed methods or force Dedicated — PNI stands.
	for _, topo := range []string{"Hub-and-spoke (Transit Gateway)", "Multiple VPCs (peered)", "Single VPC"} {
		n := netOf(func(p *Profile) { p.SourceTopology = topo })
		if n.Value != "PNI" || n.ForcesDedicated {
			t.Errorf("topology %q: value=%q forcesDedicated=%v, want PNI / false", topo, n.Value, n.ForcesDedicated)
		}
	}
}

func TestNetworking_DedicatedUsesPrivateLink(t *testing.T) {
	// Band 3 forces Dedicated; PNI is not available on Dedicated, so PrivateLink.
	if got := netOf(func(p *Profile) { p.PartitionBand = "Over 96,000" }).Value; got != "PrivateLink" {
		t.Errorf("AWS Dedicated: value=%q, want PrivateLink", got)
	}
	if got := netOf(func(p *Profile) { p.PartitionBand = "Over 96,000"; p.TargetCloud = "Azure" }).Value; got != "PrivateLink" {
		t.Errorf("non-AWS Dedicated: value=%q, want PrivateLink", got)
	}
}

func TestNetworking_AzureEnterprisePrivateLink(t *testing.T) {
	n := netOf(func(p *Profile) { p.TargetCloud = "Azure"; p.PartitionBand = "30,000–96,000" })
	if n.Value != "PrivateLink" || n.ForcesDedicated {
		t.Errorf("Azure Band 2: value=%q forcesDedicated=%v, want PrivateLink / false", n.Value, n.ForcesDedicated)
	}
}

func TestNetworking_GCPOutboundForcesDedicated(t *testing.T) {
	n := netOf(func(p *Profile) { p.TargetCloud = "GCP"; p.CCEgressRequired = "Yes" })
	if !n.ForcesDedicated {
		t.Errorf("GCP + egress: forcesDedicated=%v, want true", n.ForcesDedicated)
	}
}

func TestNetworking_GCPPublicEgressStaysPublic(t *testing.T) {
	// cc_egress_required is a private-path-only refinement: on a PUBLIC GCP plan it
	// must not push the workload onto Dedicated (matching AWS/Azure), so the plan
	// stays a public endpoint.
	n := netOf(func(p *Profile) {
		p.TargetCloud = "GCP"
		p.CCEgressRequired = "Yes"
		p.PublicEndpointsOK = "Yes"
		p.SourceAccessibility = "Public"
		p.SourcePublicAccess = "Yes"
		p.AnyAppNeedsDataMigration = "No"
	})
	if n.Value != "Public endpoint" || n.ForcesDedicated {
		t.Errorf("GCP public + egress: value=%q forcesDedicated=%v, want Public endpoint / false", n.Value, n.ForcesDedicated)
	}
}

func TestNetworking_CrossedToPrivateProvenance(t *testing.T) {
	// The lead-in provenance for a public-OK workload crossed to private must be
	// honest about the driver: an answer (exceeds Standard limits, or mTLS) reads as
	// an answer; only a scanned size band reads as a scan.
	crossed := func(mut func(*Profile)) NetworkingResult {
		p := baseProfile(mut)
		return networkingDecision(p, netCtx{TC: targetCloud(p), Band: sizingBand(p).Band, CrossedToPrivate: true})
	}
	exceeds := crossed(func(p *Profile) { p.PublicEndpointsOK = "Yes"; p.ExceedsStandardLimits = "Yes" })
	if !strings.Contains(exceeds.Reason, "Based on your answer (workload exceeds Standard limits)") {
		t.Errorf("exceeds cross: reason=%q, want answer provenance", exceeds.Reason)
	}
	mtls := crossed(func(p *Profile) { p.PublicEndpointsOK = "Yes"; p.SourceAuthTypes = []string{authMTLS} })
	if !strings.Contains(mtls.Reason, "Based on your answer (mTLS authentication)") {
		t.Errorf("mTLS cross: reason=%q, want answer provenance", mtls.Reason)
	}
	size := crossed(func(p *Profile) { p.PublicEndpointsOK = "Yes" })
	if !strings.Contains(size.Reason, "Based on your scan (workload needs private networking)") {
		t.Errorf("size cross: reason=%q, want scan provenance", size.Reason)
	}
}

func TestNetworking_GCPNoOutboundIsPSC(t *testing.T) {
	n := netOf(func(p *Profile) { p.TargetCloud = "GCP"; p.AnyAppNeedsDataMigration = "No" })
	if n.Value != "Private Service Connect (PSC)" {
		t.Errorf("GCP no outbound: value=%q, want Private Service Connect (PSC)", n.Value)
	}
	if n.Action == nil || *n.Action != "Set up Private Service Connect (PSC)" {
		t.Errorf("GCP PSC action=%v", n.Action)
	}
	if n.ForcesDedicated || !strings.Contains(n.Reason, "Private Service Connect") {
		t.Errorf("GCP PSC: forcesDedicated=%v reason=%q", n.ForcesDedicated, n.Reason)
	}
}

func TestNetworking_ConnectorEgressByCloud(t *testing.T) {
	// AWS: PNI + Egress endpoint.
	if got := netOf(func(p *Profile) { p.CCEgressRequired = "Yes" }).Value; got != "PNI + Egress PrivateLink Endpoint" {
		t.Errorf("AWS egress: value=%q, want PNI + Egress PrivateLink Endpoint", got)
	}
	// Azure: PrivateLink + Egress endpoint (not ignored, not Dedicated).
	az := netOf(func(p *Profile) { p.TargetCloud = "Azure"; p.CCEgressRequired = "Yes" })
	if az.Value != "PrivateLink + Egress PrivateLink Endpoint" || az.ForcesDedicated {
		t.Errorf("Azure egress: value=%q forcesDedicated=%v", az.Value, az.ForcesDedicated)
	}
	if !strings.Contains(az.Reason, "Egress PrivateLink Endpoint alongside PrivateLink") {
		t.Errorf("Azure egress reason should name the endpoint, got: %q", az.Reason)
	}
	// Azure without egress: plain PrivateLink.
	if got := netOf(func(p *Profile) { p.TargetCloud = "Azure"; p.CCEgressRequired = "No" }).Value; got != "PrivateLink" {
		t.Errorf("Azure no egress: value=%q, want PrivateLink", got)
	}
}

func TestNetworking_PrivateLinkAlternativeWhenAlreadyOnIt(t *testing.T) {
	// Already on PrivateLink: PNI leads, PrivateLink stays as the alternative.
	n := netOf(func(p *Profile) { p.ConnectsToday = connectsPrivateLink })
	if n.Value != "PNI" {
		t.Errorf("connects_today PrivateLink: value=%q, want PNI", n.Value)
	}
	if n.Alternative == nil || !strings.Contains(n.Alternative.Value, "PrivateLink") {
		t.Errorf("expected a PrivateLink alternative, got %+v", n.Alternative)
	}
}

func TestNetworking_TradeoffsAttached(t *testing.T) {
	n := netOf(nil) // AWS Enterprise PNI
	if len(n.Pros) == 0 || len(n.Cons) == 0 || n.Fit == "" {
		t.Errorf("PNI tradeoffs not attached: pros=%v cons=%v fit=%q", n.Pros, n.Cons, n.Fit)
	}
	// AWS PrivateLink alternative should carry the AWS-keyed tradeoffs.
	alt := netOf(func(p *Profile) { p.ConnectsToday = connectsPrivateLink }).Alternative
	if alt == nil || len(alt.Pros) == 0 {
		t.Errorf("PrivateLink alternative tradeoffs not attached: %+v", alt)
	}
}

// TestNetworking_UnansweredPrivatePNILead covers the privLead=="" path: when the
// public/private fork is unanswered, PNI leads with a capitalized "On AWS …" and
// credits no answer (A6).
func TestNetworking_UnansweredPrivatePNILead(t *testing.T) {
	n := netOf(func(p *Profile) { p.SourceAccessibility = "" }) // fork unanswered
	if n.Value != "PNI" {
		t.Fatalf("unanswered private: value=%q, want PNI", n.Value)
	}
	if !strings.HasPrefix(n.Reason, "On AWS, we recommend PNI") {
		t.Errorf("unanswered-private PNI reason should open 'On AWS, we recommend PNI': %q", n.Reason)
	}
	if strings.Contains(n.Reason, "Based on your answer") {
		t.Errorf("unanswered-private PNI reason must not credit an answer: %q", n.Reason)
	}
}
