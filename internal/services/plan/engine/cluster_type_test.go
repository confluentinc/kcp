package engine

import (
	"strings"
	"testing"
)

// baseProfile is a Band-2, private, SCRAM MSK-on-AWS source — the shared fixture
// the cluster-type cases start from. Overrides layer on top.
func baseProfile(mut func(*Profile)) Profile {
	p := Profile{
		SourcePlatform:      "Amazon MSK",
		MSKClusterType:      MSKProvisioned,
		PartitionBand:       "2,500–30,000",
		IngressBand:         "250–600 MB/s", // legacy label -> Band 2
		EgressBand:          "750–1,800 MB/s",
		SourceAccessibility: "Private",
		TargetCloud:         "AWS",
		SourceAuthTypes:     []string{"SASL/SCRAM"},
	}
	if mut != nil {
		mut(&p)
	}
	return p
}

func ctOf(p Profile) ClusterTypeResult {
	return clusterType(p, sizingBand(p), targetCloud(p), nil)
}

func TestClusterType_NoHardLimitEnterprise(t *testing.T) {
	c := ctOf(baseProfile(nil))
	if c.Value != TierEnterprise || c.Dedicated {
		t.Errorf("base: value=%s dedicated=%v, want Enterprise / false", c.Value, c.Dedicated)
	}
}

func TestClusterType_Band3ForcesDedicated(t *testing.T) {
	c := ctOf(baseProfile(func(p *Profile) { p.PartitionBand = "Over 96,000" }))
	if !c.Dedicated || c.Value != TierDedicated {
		t.Errorf("Band 3: value=%s dedicated=%v, want Dedicated / true", c.Value, c.Dedicated)
	}
}

func TestClusterType_MTLSGate(t *testing.T) {
	// mTLS source + non-AWS target -> Dedicated.
	nonAWS := ctOf(baseProfile(func(p *Profile) {
		p.TargetCloud = "Azure"
		p.SourceAuthTypes = []string{authMTLS}
	}))
	if !nonAWS.Dedicated {
		t.Errorf("mTLS + Azure: dedicated=%v, want true", nonAWS.Dedicated)
	}
	// mTLS source + AWS target does NOT force Dedicated.
	aws := ctOf(baseProfile(func(p *Profile) {
		p.TargetCloud = "AWS"
		p.SourceAuthTypes = []string{authMTLS}
	}))
	if aws.Dedicated {
		t.Errorf("mTLS + AWS: dedicated=%v, want false", aws.Dedicated)
	}
}

func TestClusterType_StaleFieldsInert(t *testing.T) {
	// A topology hint, or any deleted regulatory/checklist field carried in a
	// stale profile, must not move the tier — the engine ignores unknown fields
	// by construction (no such Profile field exists), so base stays Enterprise.
	c := ctOf(baseProfile(nil))
	if c.Dedicated || c.Value != TierEnterprise {
		t.Errorf("stale-field profile: value=%s dedicated=%v, want Enterprise / false", c.Value, c.Dedicated)
	}
}

func TestClusterType_AlwaysMultiZone(t *testing.T) {
	if got := ctOf(baseProfile(nil)).ZoneConfig; got != "Multi-zone" {
		t.Errorf("zoneConfig=%q, want Multi-zone", got)
	}
	// no input can produce Single-zone.
	if got := ctOf(baseProfile(func(p *Profile) { p.PartitionBand = "Over 96,000" })).ZoneConfig; got != "Multi-zone" {
		t.Errorf("Dedicated zoneConfig=%q, want Multi-zone", got)
	}
}

// The public path: Standard is now a first-class Band-1 outcome, and a declared
// ceiling breach (throughput or mTLS) crosses it to Enterprise on private
// networking, self-serve.
func TestClusterType_PublicPathStandardAndCrossings(t *testing.T) {
	publicBand1 := func(mut func(*Profile)) Profile {
		return baseProfile(func(p *Profile) {
			p.PublicEndpointsOK = "Yes"
			p.SourceAccessibility = ""
			p.PartitionBand = "Under 2,500"
			p.IngressBand = ""
			p.EgressBand = ""
			if mut != nil {
				mut(p)
			}
		})
	}

	// Band 1, public acceptable, fits Standard -> Standard.
	if c := ctOf(publicBand1(nil)); c.Value != TierStandard || c.CrossedToPrivate {
		t.Errorf("public Band 1: value=%s crossed=%v, want Standard / false", c.Value, c.CrossedToPrivate)
	}

	// Band 2 public -> crossed to Enterprise on private networking.
	band2 := ctOf(publicBand1(func(p *Profile) { p.PartitionBand = "2,500–30,000" }))
	if band2.Value != TierEnterprise || !band2.CrossedToPrivate {
		t.Errorf("public Band 2: value=%s crossed=%v, want Enterprise / true", band2.Value, band2.CrossedToPrivate)
	}

	// Band 1 public + declared Standard-ceiling breach -> crossed to Enterprise
	// (throughput), still self-serve; the reason names throughput, not size.
	thr := ctOf(publicBand1(func(p *Profile) { p.ExceedsStandardLimits = "Yes" }))
	if thr.Value != TierEnterprise || !thr.CrossedToPrivate {
		t.Errorf("Standard breach: value=%s crossed=%v, want Enterprise / true", thr.Value, thr.CrossedToPrivate)
	}
	if !strings.Contains(thr.Reason, "throughput is above what a Standard cluster holds") {
		t.Errorf("Standard-breach reason should name throughput, got: %q", thr.Reason)
	}

	// Band 1 public + mTLS -> crossed to Enterprise on the mTLS gate.
	mtls := ctOf(publicBand1(func(p *Profile) { p.SourceAuthTypes = []string{authMTLS} }))
	if mtls.Value != TierEnterprise || !mtls.CrossedToPrivate {
		t.Errorf("mTLS Band 1 public: value=%s crossed=%v, want Enterprise / true", mtls.Value, mtls.CrossedToPrivate)
	}

	// A stale Band-2 field (exceeds_enterprise_limits) must not affect a Band 1
	// plan, and exceeds_standard on Band 1 is self-serve, never Dedicated.
	if c := ctOf(publicBand1(func(p *Profile) { p.ExceedsEnterpriseLimits = "Yes" })); c.Dedicated {
		t.Errorf("exceeds_enterprise on Band 1 must not force Dedicated, got value=%s", c.Value)
	}
}

// A declared Enterprise-ceiling breach on Band 2 forces Dedicated (which the plan
// then withholds); No/unanswered is inert.
func TestClusterType_Band2EnterpriseBreachForcesDedicated(t *testing.T) {
	over := ctOf(baseProfile(func(p *Profile) {
		p.PublicEndpointsOK = "Yes"
		p.SourceAccessibility = ""
		p.ExceedsEnterpriseLimits = "Yes"
	}))
	if over.Value != TierDedicated || !over.Dedicated {
		t.Errorf("Band 2 Enterprise breach: value=%s dedicated=%v, want Dedicated / true", over.Value, over.Dedicated)
	}
	// A Dedicated verdict drives the plan-level withhold + human-assist handoff, so
	// it must be flagged Dedicated, marked not-self-serve, and carry the cause that
	// feeds the specialist trigger. (Its display strings are intentionally left
	// empty — never rendered for a withheld plan; see clusterType's dedicated arm.)
	if over.SelfServe {
		t.Errorf("Dedicated verdict must not be self-serve")
	}
	if len(over.Causes) == 0 || over.Causes[0].ID != "exceeds_enterprise_limits" {
		t.Errorf("Dedicated verdict should carry the exceeds_enterprise_limits cause, got: %+v", over.Causes)
	}
	no := ctOf(baseProfile(func(p *Profile) {
		p.PublicEndpointsOK = "Yes"
		p.SourceAccessibility = ""
		p.ExceedsEnterpriseLimits = "No"
	}))
	if no.Dedicated {
		t.Errorf("exceeds_enterprise_limits=No must be inert, got value=%s", no.Value)
	}
}

func TestClusterType_LimitsQuestionsGenerated(t *testing.T) {
	if got := enterpriseLimitsQuestion(); got != "Does your workload exceed any of the following: 1,920 megabytes/sec ingress, 5,760 megabytes/sec egress, or 240,000 requests/sec?" {
		t.Errorf("enterpriseLimitsQuestion = %q", got)
	}
	if got := standardLimitsQuestion(); got != "Does your workload exceed any of the following: 250 megabytes/sec ingress, 750 megabytes/sec egress, or 15,000 requests/sec?" {
		t.Errorf("standardLimitsQuestion = %q", got)
	}
}

func TestClusterType_Action(t *testing.T) {
	if got := ctOf(baseProfile(nil)).Action; got != "Create an Enterprise cluster" {
		t.Errorf("Enterprise action = %q, want %q", got, "Create an Enterprise cluster")
	}
	std := ctOf(baseProfile(func(p *Profile) {
		p.PublicEndpointsOK = "Yes"
		p.SourceAccessibility = ""
		p.PartitionBand = "Under 2,500"
		p.IngressBand = ""
		p.EgressBand = ""
	}))
	if std.Action != "Create a Standard cluster" {
		t.Errorf("Standard action = %q, want %q", std.Action, "Create a Standard cluster")
	}
}

// TestClusterType_AssumePrivateUnanswered covers the assume-private Enterprise
// branch (public/private fork unanswered): both bands lead with the private
// assumption, but only a Standard-fitting workload may drop to Standard on
// answering public — a size-forced one is Enterprise regardless (A5).
func TestClusterType_AssumePrivateUnanswered(t *testing.T) {
	base := func(band string) Profile {
		return Profile{
			SourcePlatform: "Amazon MSK", MSKClusterType: MSKProvisioned,
			PartitionBand: band, TargetCloud: "AWS", SourceAuthTypes: []string{"SASL/SCRAM"},
		}
	}
	// Size-forced (Band 2): Enterprise regardless of the fork, so no "may move to
	// Standard" nudge.
	forced := ctOf(base("2,500–30,000"))
	if forced.Value != TierEnterprise {
		t.Fatalf("size-forced: tier=%s, want Enterprise", forced.Value)
	}
	if !strings.Contains(forced.Reason, "we assume private networking") {
		t.Errorf("size-forced reason should assume private networking: %q", forced.Reason)
	}
	if strings.Contains(forced.Reason, "may move to Standard") {
		t.Errorf("size-forced reason must not offer a move to Standard: %q", forced.Reason)
	}
	// Standard-fitting (Band 1): answering public would drop it to Standard, so the
	// nudge is correct here.
	fits := ctOf(base("Under 2,500"))
	if fits.Value != TierEnterprise {
		t.Fatalf("band-1 assumed-private: tier=%s, want Enterprise", fits.Value)
	}
	if !strings.Contains(fits.Reason, "we assume private networking") {
		t.Errorf("band-1 reason should assume private networking: %q", fits.Reason)
	}
	if !strings.Contains(fits.Reason, "may move to Standard") {
		t.Errorf("band-1 reason should offer a move to Standard: %q", fits.Reason)
	}
}
