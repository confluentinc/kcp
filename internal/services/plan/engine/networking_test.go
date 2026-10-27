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

func gcpPublicOK(p *Profile) {
	p.TargetCloud = "GCP"
	p.PublicEndpointsOK = "Yes"
	p.SourceAccessibility = "Public"
	p.SourcePublicAccess = "Yes"
	p.AnyAppNeedsDataMigration = "No"
}

func TestNetworking_GCPPublicEgressForcesDedicated(t *testing.T) {
	// "Public endpoints OK" covers client ingress only; an outbound private need
	// (cc_egress_required) is a separate requirement and must not be dropped on GCP.
	plan := ComputePlan(baseProfile(func(p *Profile) { gcpPublicOK(p); p.CCEgressRequired = "Yes" }))
	n := plan.Networking
	if n.Value != "Private Service Connect (moves to Dedicated)" || !n.ForcesDedicated {
		t.Errorf("GCP public + egress: value=%q forcesDedicated=%v, want PSC (moves to Dedicated) / true", n.Value, n.ForcesDedicated)
	}
	if !plan.Withheld {
		t.Errorf("GCP public + egress: want withheld (specialist handoff)")
	}
}

func TestNetworking_GCPPublicEgressLinkTriggerNeedsExplicitDataYes(t *testing.T) {
	// The connector-egress need makes the plan private, so the infra-wide data-migration
	// question is asked and an unanswered default assumes the data moves.
	for _, c := range []struct {
		data     string
		wantLink bool
	}{{"", true}, {"No", false}, {"Yes", true}} {
		plan := ComputePlan(baseProfile(func(p *Profile) {
			gcpPublicOK(p)
			p.SourcePublicAccess = "No"
			p.CCEgressRequired = "Yes"
			p.AnyAppNeedsDataMigration = c.data
		}))
		if got := hasTriggerID(plan, "gcp_private_source_cluster_link"); got != c.wantLink {
			t.Errorf("data=%q: link trigger=%v, want %v: %+v", c.data, got, c.wantLink, plan.HumanAssist.Triggers)
		}
		if !hasTriggerID(plan, "dedicated_0") {
			t.Errorf("data=%q: connector-egress Dedicated handoff missing: %+v", c.data, plan.HumanAssist.Triggers)
		}
	}
}

func TestNetworking_GCPPublicNoEgressStaysPublic(t *testing.T) {
	n := netOf(func(p *Profile) { gcpPublicOK(p); p.CCEgressRequired = "No" })
	if n.Value != "Public endpoint" || n.ForcesDedicated {
		t.Errorf("GCP public, no egress: value=%q forcesDedicated=%v, want Public endpoint / false", n.Value, n.ForcesDedicated)
	}
}

// smallPublicOK is a Standard-sized, public-OK workload on tc, so a cross to
// private can only come from the egress answer.
func smallPublicOK(tc, egress string) Profile {
	return baseProfile(func(p *Profile) {
		gcpPublicOK(p)
		p.TargetCloud = tc
		p.CCEgressRequired = egress
		p.PartitionBand = "Under 2,500"
		p.IngressBand = ""
		p.EgressBand = ""
	})
}

func TestNetworking_AWSAzurePublicEgressGoesEnterpriseWithEgressEndpoint(t *testing.T) {
	// "Public endpoints OK" covers client ingress only; an outbound private
	// connection needs Enterprise with an Egress PrivateLink Endpoint.
	const want = "public endpoints are fine for your clients, but Confluent Cloud also needs an outbound private connection, which needs an Enterprise cluster with an Egress PrivateLink Endpoint."
	for tc, value := range map[string]string{
		"AWS":   "PNI + Egress PrivateLink Endpoint",
		"Azure": "PrivateLink + Egress PrivateLink Endpoint",
	} {
		plan := ComputePlan(smallPublicOK(tc, "Yes"))
		n := plan.Networking
		if n.Value != value || n.ForcesDedicated || !n.HasEgressEndpoint {
			t.Errorf("%s public + egress: value=%q forcesDedicated=%v hasEgress=%v, want %q / false / true", tc, n.Value, n.ForcesDedicated, n.HasEgressEndpoint, value)
		}
		if !strings.Contains(n.Reason, want) {
			t.Errorf("%s networking reason=%q, want to contain %q", tc, n.Reason, want)
		}
		if strings.Contains(n.Reason, "for your private connection") {
			t.Errorf("%s networking reason=%q, must not say \"for your private connection\"", tc, n.Reason)
		}
		method := "On AWS, we use PNI (Private Network Interface) for the cluster's private networking."
		if tc == "Azure" {
			method = "On Azure, we use PrivateLink for the cluster's private networking, which fits an Enterprise cluster."
		}
		if !strings.Contains(n.Reason, want+" "+method) {
			t.Errorf("%s networking reason=%q, want outbound need followed by %q", tc, n.Reason, method)
		}
		if n.PublicFallback != nil {
			t.Errorf("%s: no public fallback should be offered, got %v", tc, *n.PublicFallback)
		}
		ct := plan.ClusterType
		if ct.Tier != TierEnterprise || !ct.CrossedToPrivate || plan.Withheld {
			t.Errorf("%s cluster type: tier=%q crossed=%v withheld=%v, want Enterprise / true / false", tc, ct.Tier, ct.CrossedToPrivate, plan.Withheld)
		}
		if !strings.Contains(ct.Reason, want) {
			t.Errorf("%s cluster reason=%q, want to contain %q", tc, ct.Reason, want)
		}
		if !willBePrivate(smallPublicOK(tc, "Yes")) {
			t.Errorf("%s: willBePrivate should be true", tc)
		}
	}
}

func TestNetworking_AWSAzurePublicNoEgressStaysPublic(t *testing.T) {
	for _, tc := range []string{"AWS", "Azure"} {
		n := ComputePlan(smallPublicOK(tc, "No")).Networking
		if n.Value != "Public endpoint" {
			t.Errorf("%s public, no egress: value=%q, want Public endpoint", tc, n.Value)
		}
	}
}

func TestNetworking_GCPPrivateLinkSourceIsHandoffWithoutEgressEndpoint(t *testing.T) {
	const linkNote = "Cluster Linking from a private source into Google Cloud isn't supported over Private Service Connect, so we'll design the migration path with you."
	plan := ComputePlan(baseProfile(func(p *Profile) {
		p.TargetCloud = "GCP"
		p.SourcePublicAccess = "No"
		p.AnyAppNeedsDataMigration = "Yes"
	}))
	n := plan.Networking
	if !plan.Withheld || n.ForcesDedicated || !hasTriggerID(plan, "gcp_private_source_cluster_link") {
		t.Fatalf("GCP private link source: withheld=%v forcesDedicated=%v, want true / false (link trigger alone)", plan.Withheld, n.ForcesDedicated)
	}
	if n.Value != "Private Service Connect" || strings.Contains(n.Value+n.Reason+n.Why, "Dedicated") {
		t.Errorf("link-only networking must not mention Dedicated: value=%q reason=%q why=%q", n.Value, n.Reason, n.Why)
	}
	if plan.ClusterType.Tier == TierDedicated {
		t.Errorf("link-only must not move to Dedicated: tier=%v", plan.ClusterType.Tier)
	}
	if !strings.Contains(n.Reason, linkNote) {
		t.Errorf("reason=%q, want link note", n.Reason)
	}
	if strings.Contains(n.Reason, "outbound private connection") || n.HasEgressEndpoint || strings.Contains(n.Value, "Egress") {
		t.Errorf("link must not present an egress endpoint: value=%q reason=%q", n.Value, n.Reason)
	}
	// With connectors also needing egress, the connectors endpoint stays and the note is added.
	both := ComputePlan(baseProfile(func(p *Profile) {
		p.TargetCloud = "GCP"
		p.SourcePublicAccess = "No"
		p.AnyAppNeedsDataMigration = "Yes"
		p.CCEgressRequired = "Yes"
	})).Networking
	if !strings.Contains(both.Reason, linkNote) || !strings.Contains(both.Reason, "outbound private connection") {
		t.Errorf("connectors + link: reason=%q", both.Reason)
	}
	if !both.ForcesDedicated {
		t.Errorf("connectors + link: connector egress must still force Dedicated")
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
	if n.Value != "Private Service Connect" {
		t.Errorf("GCP no outbound: value=%q, want Private Service Connect", n.Value)
	}
	if n.Action == nil || *n.Action != "Set up Private Service Connect" {
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

// gcpLinkTrigger returns the GCP private-source cluster-link handoff trigger, if any.
func gcpLinkTrigger(plan PlanResult) (Trigger, bool) {
	for _, tr := range plan.HumanAssist.Triggers {
		if tr.ID == "gcp_private_source_cluster_link" {
			return tr, true
		}
	}
	return Trigger{}, false
}

func TestNetworking_GCPHandDeclaredDivergenceIsHandoff(t *testing.T) {
	const linkWhy = "Cluster Linking from a private source into Google Cloud isn't supported over Private Service Connect, so we'll design the migration path with you."
	// Infra-wide data migration is No, but this cluster's own answer is Yes: infra
	// stays on Enterprise/PSC, and the plan is withheld by an app-scope handoff.
	plan := ComputePlan(baseProfile(func(p *Profile) {
		p.TargetCloud = "GCP"
		p.SourcePublicAccess = "No"
		p.AnyAppNeedsDataMigration = "No"
		p.NeedsDataMigration = "Yes"
	}))
	if !plan.Withheld || plan.Networking.ForcesDedicated || plan.ClusterType.Value == "Dedicated" {
		t.Fatalf("withheld=%v forcesDedicated=%v type=%q, want true / false / not Dedicated", plan.Withheld, plan.Networking.ForcesDedicated, plan.ClusterType.Value)
	}
	tr, ok := gcpLinkTrigger(plan)
	if !ok || tr.Why != linkWhy {
		t.Errorf("trigger=%+v ok=%v, want the GCP link handoff with the link why", tr, ok)
	}
	// Infra-wide Yes keeps the existing handoff, with the same why and no Dedicated copy.
	infra := ComputePlan(baseProfile(func(p *Profile) {
		p.TargetCloud = "GCP"
		p.SourcePublicAccess = "No"
		p.AnyAppNeedsDataMigration = "Yes"
	}))
	tr, ok = gcpLinkTrigger(infra)
	if !infra.Withheld || !ok || tr.Why != linkWhy {
		t.Errorf("infra Yes: withheld=%v trigger=%+v ok=%v", infra.Withheld, tr, ok)
	}
	for _, x := range infra.HumanAssist.Triggers {
		if strings.Contains(x.Why, "Dedicated cluster") || strings.Contains(x.Why, "outbound private connection") {
			t.Errorf("infra Yes: trigger %q must not use Dedicated/outbound copy: %q", x.ID, x.Why)
		}
	}
	// Neither answer moves data: no hand-off.
	none := ComputePlan(baseProfile(func(p *Profile) {
		p.TargetCloud = "GCP"
		p.SourcePublicAccess = "No"
		p.AnyAppNeedsDataMigration = "No"
		p.NeedsDataMigration = "No"
	}))
	if strings.Contains(none.Networking.Reason, "Cluster Linking from a private source") {
		t.Errorf("no data migration should not hit the link note: %q", none.Networking.Reason)
	}
}

func TestNetworking_JumpClusterAddsNoEgressEndpoint(t *testing.T) {
	// Private MSK Provisioned, IAM-only: the jump cluster connects out through the
	// customer's own PrivateLink VPC endpoint, so no Egress PrivateLink Endpoint.
	jump := ComputePlan(cpBase(func(p *Profile) {
		p.SourceAuthTypes = []string{"AWS IAM"}
		p.SourcePublicAccess = "No"
		p.RequiresPrivateField = "Yes"
		p.AnyAppNeedsDataMigration = "Yes"
	}))
	if jump.Networking.HasEgressEndpoint || strings.Contains(jump.Networking.Value, "Egress") {
		t.Errorf("jump cluster: value=%q hasEgress=%v, want no egress endpoint", jump.Networking.Value, jump.Networking.HasEgressEndpoint)
	}
	// The CC-pulled SCRAM link keeps its egress endpoint.
	scram := ComputePlan(cpBase(func(p *Profile) {
		p.SourcePublicAccess = "No"
		p.RequiresPrivateField = "Yes"
		p.AnyAppNeedsDataMigration = "Yes"
	}))
	if !scram.Networking.HasEgressEndpoint {
		t.Errorf("SCRAM link: value=%q, want an egress endpoint", scram.Networking.Value)
	}
}

func TestClusterLinkingFloor_SourceAware(t *testing.T) {
	cases := map[SourceType]string{
		SourceMSK:               "Kafka 2.4 and inter-broker protocol (IBP) 2.8",
		SourceApacheKafka:       "Kafka 2.4 and inter-broker protocol (IBP) 2.8",
		SourceConfluentPlatform: "Confluent Platform 5.4 and inter-broker protocol (IBP) 2.8",
		"":                      "Kafka 2.4, Confluent Platform 5.4, inter-broker protocol (IBP) 2.8",
	}
	for st, want := range cases {
		if got := ClusterLinkingFloor(st); got != want {
			t.Errorf("ClusterLinkingFloor(%q) = %q, want %q", st, got, want)
		}
	}
}

func TestHumanAssist_OnPremTransitGatewayOnlyOnAWS(t *testing.T) {
	aws := ComputePlan(onPremBase(SourceApacheKafka, nil))
	if !strings.Contains(aws.HumanAssist.Reason, "over network peering or Transit Gateway.") {
		t.Errorf("AWS reason = %q, want Transit Gateway", aws.HumanAssist.Reason)
	}
	for _, tc := range []string{"Azure", "GCP"} {
		r := ComputePlan(onPremBase(SourceApacheKafka, func(p *Profile) { p.TargetCloud = tc })).HumanAssist.Reason
		if strings.Contains(r, "Transit Gateway") || !strings.Contains(r, "over network peering.") {
			t.Errorf("%s reason = %q, want peering only", tc, r)
		}
	}
}

func hasTriggerID(plan PlanResult, id string) bool {
	for _, tr := range plan.HumanAssist.Triggers {
		if tr.ID == id {
			return true
		}
	}
	return false
}

func TestNetworking_GCPLinkIsHandoffNotDedicatedCause(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(p *Profile)
		wantDedic bool
	}{
		{"data migration unset (assumed)", func(p *Profile) {
			p.AnyAppNeedsDataMigration = ""
		}, false},
		{"cc_egress yes plus data yes", func(p *Profile) {
			p.CCEgressRequired = "Yes"
			p.AnyAppNeedsDataMigration = "Yes"
		}, true},
		{"mTLS plus data yes", func(p *Profile) {
			p.SourceAuthTypes = []string{authMTLS}
			p.AnyAppNeedsDataMigration = "Yes"
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := ComputePlan(baseProfile(func(p *Profile) {
				p.TargetCloud = "GCP"
				p.SourcePublicAccess = "No"
				c.mutate(p)
			}))
			if !hasTriggerID(plan, "gcp_private_source_cluster_link") {
				t.Errorf("missing link trigger: %+v", plan.HumanAssist.Triggers)
			}
			n := 0
			for _, tr := range plan.HumanAssist.Triggers {
				if tr.ID == "gcp_private_source_cluster_link" {
					n++
				}
				if strings.HasPrefix(tr.ID, "dedicated_") && strings.Contains(tr.Why, "private source into Google Cloud") {
					t.Errorf("link emitted as a Dedicated cause: %+v", tr)
				}
			}
			if n != 1 {
				t.Errorf("link trigger fired %d times, want 1", n)
			}
			if got := hasTriggerID(plan, "dedicated_0"); got != c.wantDedic {
				t.Errorf("dedicated_0=%v, want %v: %+v", got, c.wantDedic, plan.HumanAssist.Triggers)
			}
			if hasTriggerID(plan, "dedicated_1") {
				t.Errorf("unexpected dedicated_1: %+v", plan.HumanAssist.Triggers)
			}
		})
	}
}

func TestNetworking_JumpClusterPrivateLinkNoteAWSOnly(t *testing.T) {
	const note = "Your AWS IAM source needs a jump cluster, a temporary Kafka cluster in your AWS account, and it needs a PrivateLink connection from your AWS account to your Confluent Cloud cluster."
	jump := func(cloud string) *Profile {
		p := baseProfile(func(p *Profile) {
			p.TargetCloud = cloud
			p.SourceType = SourceMSK
			p.SourcePublicAccess = "No"
			p.SourceAuthTypes = []string{authAWSIAM}
			p.AnyAppNeedsDataMigration = "Yes"
		})
		return &p
	}
	aws := ComputePlan(*jump("AWS"))
	if !strings.Contains(aws.Networking.Reason, note) {
		t.Errorf("AWS jump cluster: reason=%q, want PrivateLink note", aws.Networking.Reason)
	}
	for _, cloud := range []string{"Azure", "GCP"} {
		if n := ComputePlan(*jump(cloud)).Networking; strings.Contains(n.Reason, note) {
			t.Errorf("%s target: reason=%q must not carry the AWS jump-cluster note", cloud, n.Reason)
		}
	}
}

// A jump-cluster plan on AWS names the PrivateLink endpoint the jump cluster uses in
// the networking value; other clouds, which have no jump cluster, are unchanged.
func TestNetworking_JumpClusterValueNamesPrivateLink(t *testing.T) {
	jump := func(cloud string) NetworkingResult {
		return ComputePlan(baseProfile(func(p *Profile) {
			p.TargetCloud = cloud
			p.SourceType = SourceMSK
			p.SourcePublicAccess = "No"
			p.SourceAuthTypes = []string{authAWSIAM}
			p.AnyAppNeedsDataMigration = "Yes"
		})).Networking
	}
	if got := jump("AWS").Value; got != "PNI + PrivateLink for the jump cluster" {
		t.Errorf("AWS jump cluster value = %q", got)
	}
	if got := jump("Azure").Value; strings.Contains(got, "jump cluster") {
		t.Errorf("Azure value must not name a jump cluster: %q", got)
	}
}

// A connector Egress PrivateLink Endpoint already on the plan doesn't hide the
// jump cluster's PrivateLink note.
func TestNetworking_JumpClusterNoteSurvivesConnectorEgress(t *testing.T) {
	const note = "Your AWS IAM source needs a jump cluster, a temporary Kafka cluster in your AWS account, and it needs a PrivateLink connection from your AWS account to your Confluent Cloud cluster."
	plan := ComputePlan(baseProfile(func(p *Profile) {
		p.TargetCloud = "AWS"
		p.SourceType = SourceMSK
		p.SourcePublicAccess = "No"
		p.SourceAuthTypes = []string{authAWSIAM}
		p.AnyAppNeedsDataMigration = "Yes"
		p.CCEgressRequired = "Yes"
	}))
	if !strings.Contains(plan.Networking.Value, "Egress PrivateLink Endpoint") || !strings.Contains(plan.Networking.Reason, note) {
		t.Errorf("value=%q reason=%q, want the connector egress endpoint and the jump cluster note", plan.Networking.Value, plan.Networking.Reason)
	}
}

// On the GCP public-OK path the cluster's own explicit "Yes" raises the link
// problem, so the networking verdict and the triggers are the same whether or not
// the infra-wide answer is also set.
func TestNetworking_GCPPublicLinkReadsClusterOwnDataAnswer(t *testing.T) {
	var base *PlanResult
	for _, anyApp := range []string{"", "Yes"} {
		plan := ComputePlan(baseProfile(func(p *Profile) {
			gcpPublicOK(p)
			p.SourcePublicAccess = "No"
			p.CCEgressRequired = "No"
			p.PartitionBand = "Over 96,000" // Dedicated
			p.NeedsDataMigration = "Yes"
			p.AnyAppNeedsDataMigration = anyApp
		}))
		if base == nil {
			base = &plan
			continue
		}
		if plan.Networking.Value != base.Networking.Value || plan.Networking.ForcesDedicated != base.Networking.ForcesDedicated ||
			hasTriggerID(plan, "gcp_private_source_cluster_link") != hasTriggerID(*base, "gcp_private_source_cluster_link") {
			t.Errorf("anyApp=%q differs from unset: networking %q/%v vs %q/%v; triggers %+v vs %+v", anyApp,
				plan.Networking.Value, plan.Networking.ForcesDedicated, base.Networking.Value, base.Networking.ForcesDedicated,
				plan.HumanAssist.Triggers, base.HumanAssist.Triggers)
		}
	}
}

// On the public path the infra-wide data-migration question isn't asked, so only
// the cluster's own explicit Yes can raise the GCP private-source link handoff.
func TestGCPPrivateLinkHandoff_PublicPathUsesOwnAnswer(t *testing.T) {
	p := baseProfile(func(p *Profile) {
		p.TargetCloud = "GCP"
		p.PublicEndpointsOK = "Yes"
		p.SourcePublicAccess = "No"
		p.AnyAppNeedsDataMigration = "Yes"
		p.NeedsDataMigration = "No"
		p.PartitionBand = "Under 2,500"
		p.IngressBand = ""
		p.EgressBand = ""
	})
	if WillBePrivate(p) {
		t.Fatalf("fixture must be on the public path")
	}
	if gcpPrivateLinkHandoff(p, TierDedicated) {
		t.Errorf("public path: infra-wide Yes with the cluster's own No must not raise the GCP link handoff")
	}
}

// A public-acceptable Azure or GCP workload crossed to private never credits an
// answer of "private networking required" it did not give, and carries the same
// public-acceptable framing AWS does.
func TestNetworking_CrossedToPrivateAzureGCPFraming(t *testing.T) {
	for _, tc := range []string{"Azure", "GCP"} {
		p := baseProfile(func(p *Profile) {
			p.TargetCloud = tc
			p.PublicEndpointsOK = "Yes"
			p.ExceedsStandardLimits = "Yes"
			p.AnyAppNeedsDataMigration, p.NeedsDataMigration = "No", "No"
		})
		n := networkingDecision(p, netCtx{TC: tc, Band: sizingBand(p).Band, CrossedToPrivate: true})
		if strings.Contains(n.Reason, "private networking required") {
			t.Errorf("%s: crossed reason must not credit the private-required answer: %q", tc, n.Reason)
		}
		if !strings.Contains(n.Reason, "Based on your answer (workload exceeds Standard limits)") {
			t.Errorf("%s: reason=%q, want the honest cross cause", tc, n.Reason)
		}
		if !strings.Contains(n.Reason, "You told us public networking is acceptable") {
			t.Errorf("%s: reason=%q, want the public-acceptable framing", tc, n.Reason)
		}
	}
}

// The on-prem hybrid-connectivity note names the target cloud's own private link.
func TestNetworking_OnPremHybridNotePerCloud(t *testing.T) {
	for target, want := range map[string]string{"AWS": "Direct Connect", "Azure": "ExpressRoute", "GCP": "Cloud Interconnect", "": "Direct Connect"} {
		p := baseProfile(func(p *Profile) { p.SourceCloud = "On-prem or other"; p.TargetCloud = target })
		net := &NetworkingResult{Value: "PNI", Reason: "x.", Why: "y."}
		addOnPremHybridNote(p, net)
		if !strings.Contains(net.Reason, want+" or a VPN") || !strings.Contains(net.Why, want+" or VPN") {
			t.Errorf("target %q: reason=%q why=%q, want %s", target, net.Reason, net.Why, want)
		}
		for _, other := range []string{"Direct Connect", "ExpressRoute", "Cloud Interconnect"} {
			if other != want && strings.Contains(net.Reason+net.Why, other) {
				t.Errorf("target %q: names %s: %q", target, other, net.Reason)
			}
		}
	}
}

// A GCP target whose connector-egress need makes the plan private asks the infra-wide
// data-migration question. Left unanswered it assumes the data moves, so a private source
// raises the Google Cloud link handoff just as an explicit "Yes" does.
func TestNetworking_GCPWillBePrivateUnansweredDataRaisesLinkHandoff(t *testing.T) {
	plan := ComputePlan(baseProfile(func(p *Profile) {
		gcpPublicOK(p)
		p.SourcePublicAccess = "No"
		p.CCEgressRequired = "Yes"
		p.AnyAppNeedsDataMigration = ""
	}))
	if !hasTriggerID(plan, "gcp_private_source_cluster_link") {
		t.Errorf("link trigger missing: %+v", plan.HumanAssist.Triggers)
	}
}

// When connector egress already added the Egress PrivateLink Endpoint, the migration link
// rides the same endpoint: both reasons stay, and an Azure source adds the per-broker note.
func TestNetworking_EgressEndpointKeepsBothReasons(t *testing.T) {
	const azureNote = "To reach your Azure brokers, each broker needs its own load balancer and Private Link Service, with a Confluent access point and a DNS record for each."
	for _, c := range []struct {
		tc, cloud, want string
		azureNote       bool
	}{
		{"AWS", "AWS", "PNI doesn't carry Cluster Linking traffic", false},
		{"Azure", "Azure", "PrivateLink is inbound only, so the link reaches your source cluster", true},
	} {
		p := baseProfile(func(p *Profile) {
			p.TargetCloud = c.tc
			p.SourceCloud = c.cloud
			p.CCEgressRequired = "Yes"
			p.NeedsDataMigration = "Yes"
			p.AnyAppNeedsDataMigration = "Yes"
		})
		n := networkingDecision(p, netCtx{TC: c.tc, Band: sizingBand(p).Band, Tier: TierEnterprise, ApplyMigrationEgress: true})
		if !strings.Contains(n.Reason, "connectors and consumers also need to connect into your network") ||
			!strings.Contains(n.Reason, "The same endpoint carries your migration: Confluent Cloud pulls your data over the cluster link,") ||
			!strings.Contains(n.Reason, c.want) {
			t.Errorf("%s target, %s source: reason lacks both egress reasons: %q", c.tc, c.cloud, n.Reason)
		}
		if got := strings.Contains(n.Reason, azureNote); got != c.azureNote {
			t.Errorf("%s target, %s source: Azure broker note = %v, want %v: %q", c.tc, c.cloud, got, c.azureNote, n.Reason)
		}
	}
}

// A cross-cloud link goes to a specialist, so the plan does not say the connector egress
// endpoint also carries the migration.
func TestNetworking_EgressEndpointCarriesLinkOnlySameCloud(t *testing.T) {
	for _, c := range []struct{ tc, cloud string }{{"Azure", "AWS"}, {"AWS", "Azure"}, {"AWS", "GCP"}} {
		p := baseProfile(func(p *Profile) {
			p.TargetCloud = c.tc
			p.SourceCloud = c.cloud
			p.CCEgressRequired = "Yes"
			p.NeedsDataMigration = "Yes"
			p.AnyAppNeedsDataMigration = "Yes"
		})
		n := networkingDecision(p, netCtx{TC: c.tc, Band: sizingBand(p).Band, Tier: TierEnterprise, ApplyMigrationEgress: true})
		if strings.Contains(n.Reason, "The same endpoint carries your migration") || strings.Contains(n.Why, "The same endpoint lets the migration cluster link") {
			t.Errorf("%s target, %s source: claims the endpoint carries the link: %q / %q", c.tc, c.cloud, n.Reason, n.Why)
		}
	}
}

// A cross-cloud plan swaps the "reach your source cluster" copy for the specialist
// sentence, whichever way the egress endpoint was added; same-cloud and unknown-source
// plans keep the original copy.
func TestNetworking_CrossCloudLinkCopy(t *testing.T) {
	sentence := func(src, tgt string) string {
		return "Your source runs on " + src + " and the new cluster runs on " + tgt + ", so a private cluster link would cross clouds, which an Egress PrivateLink Endpoint can't carry. A specialist sets up that link."
	}
	for _, c := range []struct {
		name, tc, cloud string
		connectorEgress bool
		want            string // "" means the original copy stays
	}{
		{"aws to azure, link egress", "Azure", "AWS", false, sentence("AWS", "Azure")},
		{"azure to aws, link egress", "AWS", "Azure", false, sentence("Azure", "AWS")},
		{"azure to aws, connector egress", "AWS", "Azure", true, sentence("Azure", "AWS")},
		{"aws to aws", "AWS", "AWS", false, ""},
		{"azure to azure", "Azure", "Azure", false, ""},
		{"unknown source", "Azure", "", false, ""},
	} {
		p := baseProfile(func(p *Profile) {
			p.TargetCloud = c.tc
			p.SourceCloud = c.cloud
			if c.cloud == "" {
				p.SourceType = SourceApacheKafka
			}
			p.NeedsDataMigration = "Yes"
			p.AnyAppNeedsDataMigration = "Yes"
			if c.connectorEgress {
				p.CCEgressRequired = "Yes"
			}
		})
		n := networkingDecision(p, netCtx{TC: c.tc, Band: sizingBand(p).Band, Tier: TierEnterprise, ApplyMigrationEgress: true})
		all := n.Reason + " " + n.Why
		if c.want == "" {
			if strings.Contains(all, "would cross clouds") {
				t.Errorf("%s: unexpected cross-cloud sentence: %q", c.name, all)
			}
			continue
		}
		if !strings.Contains(n.Reason, c.want) {
			t.Errorf("%s: reason lacks cross-cloud sentence: %q", c.name, n.Reason)
		}
		if strings.Contains(all, "reach your source cluster") || strings.Contains(all, "PrivateLink is inbound only") {
			t.Errorf("%s: still claims the endpoint reaches the source: %q", c.name, all)
		}
	}
}

// A cross-cloud private link is handed to a specialist, so no Egress PrivateLink
// Endpoint is added for it unless the runtime egress need asks for one.
func TestNetworking_CrossCloudLinkAddsNoEgressEndpoint(t *testing.T) {
	for _, c := range []struct {
		name, tc, cloud, egress string
		wantEndpoint            bool
	}{
		{"aws to azure, no egress need", "Azure", "AWS", "No", false},
		{"azure to aws, no egress need", "AWS", "Azure", "No", false},
		{"azure to aws, egress needed", "AWS", "Azure", "Yes", true},
		{"aws to aws, no egress need", "AWS", "AWS", "No", true},
		{"azure to azure, no egress need", "Azure", "Azure", "No", true},
	} {
		p := baseProfile(func(p *Profile) {
			p.TargetCloud = c.tc
			p.SourceCloud = c.cloud
			p.CCEgressRequired = c.egress
			p.NeedsDataMigration = "Yes"
			p.AnyAppNeedsDataMigration = "Yes"
		})
		n := networkingDecision(p, netCtx{TC: c.tc, Band: sizingBand(p).Band, Tier: TierEnterprise, ApplyMigrationEgress: true})
		if got := strings.Contains(n.Value, "Egress PrivateLink Endpoint"); got != c.wantEndpoint || n.HasEgressEndpoint != c.wantEndpoint {
			t.Errorf("%s: value %q, HasEgressEndpoint %v; want endpoint %v", c.name, n.Value, n.HasEgressEndpoint, c.wantEndpoint)
		}
		if c.cloud != c.tc && !strings.Contains(n.Reason, "A specialist sets up that link.") {
			t.Errorf("%s: cross-cloud specialist sentence missing: %q", c.name, n.Reason)
		}
	}
}

// A size-only cross to private on Azure/GCP states no basis for private networking.
func TestNetworking_SizeOnlyCrossHasNoBasisOnAzureGCP(t *testing.T) {
	for _, tc := range []string{"Azure", "GCP"} {
		p := baseProfile(func(p *Profile) { p.TargetCloud = tc; p.SourceAuthTypes = []string{"SASL/SCRAM"} })
		n := networkingDecision(p, netCtx{TC: tc, Band: sizingBand(p).Band, CrossedToPrivate: true})
		if strings.Contains(n.Reason, "workload needs private networking") {
			t.Errorf("%s: unexplained basis in %q", tc, n.Reason)
		}
	}
}
