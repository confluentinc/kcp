package engine

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// cpBase is a clean, self-serve MSK-on-AWS profile; overrides mutate it. Public
// acceptable, Band 2, SCRAM, data must move, downtime answered.
func cpBase(mut func(*Profile)) Profile {
	p := Profile{
		SourcePlatform: "Amazon MSK", MSKClusterType: MSKProvisioned, TargetCloud: "AWS",
		UseCaseBreadth: "One team, one application", PartitionBand: "2,500–30,000",
		NeedsDataMigration: "Yes", RequiresPrivateField: "No", SourceAuthTypes: []string{"SASL/SCRAM"},
		KafkaVersion: "3.0 or newer", DowntimeTolerance: "Minutes per service",
	}
	if mut != nil {
		mut(&p)
	}
	return p
}

func haHas(r HumanAssistResult, id string) bool {
	for _, tr := range r.Triggers {
		if tr.ID == id {
			return true
		}
	}
	return false
}

func TestComputePlan_Band2EnterpriseBreachWithheld(t *testing.T) {
	over := ComputePlan(cpBase(func(p *Profile) { p.ExceedsEnterpriseLimits = "Yes" }))
	if !over.Withheld || over.ClusterType.Value != TierDedicated {
		t.Errorf("breach: withheld=%v value=%s, want true / Dedicated", over.Withheld, over.ClusterType.Value)
	}
	if !haHas(over.HumanAssist, "exceeds_enterprise_limits") {
		t.Errorf("expected exceeds_enterprise_limits trigger: %+v", over.HumanAssist.Triggers)
	}
	// No breach is indistinguishable from unanswered.
	no := ComputePlan(cpBase(func(p *Profile) { p.ExceedsEnterpriseLimits = "No" }))
	if no.Withheld {
		t.Errorf("No breach must not withhold")
	}
}

func TestComputePlan_Band1StandardBreachCrossesSelfServe(t *testing.T) {
	base := func(mut func(*Profile)) Profile {
		return cpBase(func(p *Profile) {
			p.PartitionBand = "Under 2,500"
			if mut != nil {
				mut(p)
			}
		})
	}
	if v := ComputePlan(base(nil)).ClusterType.Value; v != TierStandard {
		t.Errorf("no breach: value=%s, want Standard", v)
	}
	over := ComputePlan(base(func(p *Profile) { p.ExceedsStandardLimits = "Yes" }))
	if over.ClusterType.Value != TierEnterprise || !over.ClusterType.CrossedToPrivate || over.Withheld {
		t.Errorf("standard breach: value=%s crossed=%v withheld=%v", over.ClusterType.Value, over.ClusterType.CrossedToPrivate, over.Withheld)
	}
	// A stale Band-2 field must not withhold a Band 1 plan.
	if ComputePlan(base(func(p *Profile) { p.ExceedsEnterpriseLimits = "Yes" })).Withheld {
		t.Errorf("exceeds_enterprise on Band 1 must not withhold")
	}
}

func TestComputePlan_HandoffTriggers(t *testing.T) {
	priv := func(mut func(*Profile)) Profile {
		return cpBase(func(p *Profile) {
			p.RequiresPrivateField = "Yes"
			p.ConnectsToday = connectsSameVPC
			if mut != nil {
				mut(p)
			}
		})
	}
	// Shared fabric.
	fab := ComputePlan(priv(func(p *Profile) { p.UseCaseBreadth = breadthFabric }))
	if fab.ClusterType.Value != TierEnterprise || !haHas(fab.HumanAssist, "use_case_breadth") {
		t.Errorf("breadth: value=%s triggers=%+v", fab.ClusterType.Value, fab.HumanAssist.Triggers)
	}
	// Connects Other on the private path.
	oth := ComputePlan(priv(func(p *Profile) { p.ConnectsToday = "Other" }))
	if !haHas(oth.HumanAssist, "connection_other") {
		t.Errorf("connection_other not fired: %+v", oth.HumanAssist.Triggers)
	}
	// Every route to Dedicated is withheld and not self-serve.
	routes := []func(*Profile){
		func(p *Profile) { p.PartitionBand = "Over 96,000" },                               // band cap
		func(p *Profile) { p.TargetCloud = "GCP"; p.SourceAuthTypes = []string{authMTLS} }, // mTLS on GCP
		func(p *Profile) { p.TargetCloud = "GCP"; p.CCEgressRequired = "Yes" },             // GCP outbound
	}
	for i, r := range routes {
		plan := ComputePlan(priv(r))
		if plan.ClusterType.Value != TierDedicated || plan.ClusterType.SelfServe || !plan.HumanAssist.Required {
			t.Errorf("dedicated route %d: value=%s selfServe=%v required=%v", i, plan.ClusterType.Value, plan.ClusterType.SelfServe, plan.HumanAssist.Required)
		}
		if !plan.Withheld {
			t.Errorf("dedicated route %d must be withheld", i)
		}
	}
}

func TestComputePlan_UnusualShapeWithheld(t *testing.T) {
	plan := ComputePlan(cpBase(func(p *Profile) {
		p.PartitionBand = "Under 2,500"
		p.RequestRateBand = "75,000–240,000/s"
		p.SizingReviewed = []string{"partition_band", "request_rate_band"}
	}))
	if !plan.Withheld || !haHas(plan.HumanAssist, "unusual_workload_shape") {
		t.Errorf("unusual shape: withheld=%v triggers=%+v", plan.Withheld, plan.HumanAssist.Triggers)
	}
}

func TestComputePlan_ServerlessJumpClusterEndToEnd(t *testing.T) {
	plan := ComputePlan(Profile{
		SourcePlatform: "Amazon MSK", MSKClusterType: MSKServerless, TargetCloud: "AWS",
		SourceAuthTypes: []string{authAWSIAM}, RequiresPrivateField: "Yes", ConnectsToday: connectsSameVPC,
		PartitionBand: "2,500–30,000", NeedsDataMigration: "Yes", DowntimeTolerance: "Zero downtime",
		UseCaseBreadth: "One team, one application",
	})
	if plan.ClusterType.Value != TierEnterprise {
		t.Errorf("serverless Band 2: value=%s, want Enterprise", plan.ClusterType.Value)
	}
	if !strings.Contains(strings.ToLower(plan.Switchover.Value), "jump cluster") {
		t.Errorf("switchover value=%q, want jump cluster", plan.Switchover.Value)
	}
	if plan.Switchover.Alternative == nil || plan.Switchover.Alternative.Value != "Confluent Replicator" {
		t.Errorf("alternative=%+v, want Confluent Replicator", plan.Switchover.Alternative)
	}
}

func TestComputePlan_MtlsTargetForcesDedicatedOnGCP(t *testing.T) {
	plan := ComputePlan(cpBase(func(p *Profile) {
		p.TargetCloud = "GCP"
		p.TargetIdentityModel = []string{"mTLS"}
	}))
	if !plan.ClusterType.Dedicated {
		t.Errorf("mTLS target on GCP should force Dedicated, got %s", plan.ClusterType.Value)
	}
	if !strings.Contains(plan.Auth.Value, "mTLS") {
		t.Errorf("auth value should include mTLS: %q", plan.Auth.Value)
	}
}

func TestComputePlan_CitationsSuppressedWhenWithheld(t *testing.T) {
	// A withheld plan cites no cluster-type page; a clean one does.
	withheld := ComputePlan(cpBase(func(p *Profile) { p.PartitionBand = "Over 96,000" }))
	if withheld.ClusterType.Source != "" {
		t.Errorf("withheld plan should suppress the cluster-type citation, got %q", withheld.ClusterType.Source)
	}
	clean := ComputePlan(cpBase(func(p *Profile) { p.PartitionBand = "Under 2,500" }))
	if clean.ClusterType.Source == "" {
		t.Errorf("clean plan should cite the cluster-type page")
	}
	// Networking is cited even on a withheld plan (the method is still right).
	if withheld.Networking.Source == "" {
		t.Errorf("networking should be cited even when withheld")
	}
}

// Invariant sweep — the properties that must hold for every combination,
// whatever the outcome (the essence of the combination-matrix oracle).
func TestComputePlan_Invariants(t *testing.T) {
	bands := []string{"Under 2,500", "2,500–30,000", "Over 96,000"}
	privates := []string{"Yes", "No"}
	auths := [][]string{{"SASL/SCRAM"}, {authMTLS}}
	clouds := []string{"AWS", "Azure"}

	for _, band := range bands {
		for _, priv := range privates {
			for _, auth := range auths {
				for _, cloud := range clouds {
					p := cpBase(func(p *Profile) {
						p.PartitionBand = band
						p.RequiresPrivateField = priv
						p.SourceAuthTypes = auth
						p.TargetCloud = cloud
						p.ConnectsToday = connectsSameVPC
					})
					plan := ComputePlan(p)
					ct := plan.ClusterType.Value
					net := plan.Networking.Value

					// Basic is never an output.
					if ct == TierBasic {
						t.Errorf("Basic recommended for band=%s priv=%s auth=%v cloud=%s", band, priv, auth, cloud)
					}
					// Enterprise never serves a public endpoint.
					if ct == TierEnterprise && net == "Public endpoint" {
						t.Errorf("Enterprise on a public endpoint (band=%s priv=%s cloud=%s)", band, priv, cloud)
					}
					// A private requirement floors the tier at Enterprise.
					if requiresPrivate(p) && ct == TierStandard {
						t.Errorf("private requirement landed on Standard (band=%s cloud=%s)", band, cloud)
					}
					// The top band always hands off.
					if sizingBand(p).Band == bandXL && !plan.Withheld {
						t.Errorf("top band not withheld (band=%s priv=%s cloud=%s)", band, priv, cloud)
					}
				}
			}
		}
	}
}

// TestInvariant_DedicatedAlwaysWithheld is the guard for the whole "Dedicated ⇒
// withheld" contract: sweeping the Cartesian product of every input that can drive
// a plan to Dedicated, every result that lands on TierDedicated MUST be withheld
// and route to a human, and its serialized form MUST redact the downstream
// verdicts. It licenses removing the never-rendered Dedicated-downstream copy: if
// any Dedicated plan ever surfaced its cluster_type/networking, this fails first.
func TestInvariant_DedicatedAlwaysWithheld(t *testing.T) {
	clouds := []string{"", "AWS", "Azure", "GCP"}
	exceeds := []string{"", "No", "Yes"}
	bands := []string{"Under 2,500", "2,500–30,000", "Over 96,000"} // spans bandXL
	auths := [][]string{nil, {authSCRAM}, {authMTLS}, {authAWSIAM}}
	egress := []string{"", "No", "Yes"}

	var sawDedicated bool
	var dedicatedPlan PlanResult

	for _, cloud := range clouds {
		for _, exc := range exceeds {
			for _, band := range bands {
				for _, auth := range auths {
					for _, eg := range egress {
						plan := ComputePlan(cpBase(func(p *Profile) {
							p.TargetCloud = cloud
							p.ExceedsEnterpriseLimits = exc
							p.PartitionBand = band
							p.SourceAuthTypes = auth
							p.CCEgressRequired = eg
						}))
						if plan.ClusterType.Value != TierDedicated {
							continue
						}
						if !plan.Withheld || !plan.HumanAssist.Required {
							t.Errorf("Dedicated not withheld: cloud=%q exceeds=%q band=%q auth=%v egress=%q withheld=%v haRequired=%v",
								cloud, exc, band, auth, eg, plan.Withheld, plan.HumanAssist.Required)
						}
						sawDedicated = true
						dedicatedPlan = plan
					}
				}
			}
		}
	}

	if !sawDedicated {
		t.Fatalf("sweep produced no Dedicated plan — the invariant has nothing to guard")
	}

	// A withheld Dedicated plan must serialize only human_assist + withheld; the
	// cluster_type and networking verdicts are redacted from plan.json.
	raw, err := json.Marshal(dedicatedPlan)
	if err != nil {
		t.Fatalf("marshal Dedicated plan: %v", err)
	}
	for _, key := range []string{`"cluster_type"`, `"networking"`} {
		if bytes.Contains(raw, []byte(key)) {
			t.Errorf("withheld Dedicated plan.json leaked %s: %s", key, raw)
		}
	}
}

func TestComputePlan_RaisingDimensionNeverLowersBand(t *testing.T) {
	low := sizingBand(Profile{PartitionsExact: f(1000)}).Band
	for _, ing := range []float64{100, 300, 700} {
		got := sizingBand(Profile{PartitionsExact: f(1000), PeakIngressMbps: f(ing)}).Band
		if got < low {
			t.Errorf("raising ingress to %v lowered the band from %d to %d", ing, low, got)
		}
	}
}

// TestComputePlan_SizingSingleSignalCopy checks the sizing copy names one signal
// plainly when only the partition count is known, and speaks of the most demanding
// signal only when a throughput measurement joins it (A6).
func TestComputePlan_SizingSingleSignalCopy(t *testing.T) {
	only := ComputePlan(cpBase(nil)).Sizing.Reason
	if !strings.Contains(only, "the only signal we have") {
		t.Errorf("partition-only sizing should say 'the only signal we have': %q", only)
	}
	if strings.Contains(only, "most demanding signal") {
		t.Errorf("partition-only sizing must not claim a comparison: %q", only)
	}
	withTput := ComputePlan(cpBase(func(p *Profile) { p.PeakIngressMbps = f(200) })).Sizing.Reason
	if !strings.Contains(withTput, "the most demanding signal") {
		t.Errorf("measured-throughput sizing should say 'the most demanding signal': %q", withTput)
	}
}

// onPremBase is an on-prem/other Apache Kafka or Confluent Platform source,
// otherwise the same shape as cpBase.
func onPremBase(sourceType SourceType, mut func(*Profile)) Profile {
	return cpBase(func(p *Profile) {
		p.SourceType = sourceType
		p.SourcePlatform = "Apache Kafka"
		if sourceType == SourceConfluentPlatform {
			p.SourcePlatform = "Confluent Platform"
		}
		p.SourceCloud = "On-prem or other"
		p.RequiresPrivateField = "Yes"
		if mut != nil {
			mut(p)
		}
	})
}

// TestComputePlan_OnPremPrivateClusterLinkSpecialist covers C4: an on-prem (or
// "other") Apache Kafka / Confluent Platform source migrating data to a private
// Confluent Cloud target over a cluster link has no self-serve private path, so a
// specialist designs it — and the migration link never gets an Egress PrivateLink
// Endpoint it can't actually use from outside a cloud network.
func TestComputePlan_OnPremPrivateClusterLinkSpecialist(t *testing.T) {
	const trigger = "onprem_private_cluster_link"

	// Confluent Platform, on-prem, private, data migration over a cluster link:
	// fires, with the CP-specific copy, and no Egress PrivateLink Endpoint.
	cp := ComputePlan(onPremBase(SourceConfluentPlatform, nil))
	if !haHas(cp.HumanAssist, trigger) {
		t.Fatalf("CP on-prem private: trigger did not fire, triggers=%+v", cp.HumanAssist.Triggers)
	}
	if !strings.Contains(cp.HumanAssist.Reason, "source-initiated cluster link") {
		t.Errorf("CP on-prem private reason = %q, want the Confluent Platform copy", cp.HumanAssist.Reason)
	}
	if strings.Contains(cp.Networking.Value, "Egress PrivateLink Endpoint") {
		t.Errorf("on-prem source should not get an Egress PrivateLink Endpoint, got %q", cp.Networking.Value)
	}

	// Apache Kafka, on-prem, private: fires, with the Apache Kafka-specific copy.
	ak := ComputePlan(onPremBase(SourceApacheKafka, nil))
	if !haHas(ak.HumanAssist, trigger) {
		t.Fatalf("AK on-prem private: trigger did not fire, triggers=%+v", ak.HumanAssist.Triggers)
	}
	if !strings.Contains(ak.HumanAssist.Reason, "Replicator running in your network") {
		t.Errorf("AK on-prem private reason = %q, want the Apache Kafka copy", ak.HumanAssist.Reason)
	}

	// Public on-prem source: the link works over the internet, so it doesn't fire.
	// A small (Band 1) workload keeps the public-acceptable answer from crossing to
	// private on size alone.
	pub := ComputePlan(onPremBase(SourceConfluentPlatform, func(p *Profile) {
		p.RequiresPrivateField = "No"
		p.PartitionBand = "Under 2,500"
	}))
	if haHas(pub.HumanAssist, trigger) {
		t.Errorf("CP on-prem public should not fire: %+v", pub.HumanAssist.Triggers)
	}

	// Same source, but in AWS (not on-prem): the standard Egress PrivateLink
	// Endpoint path still applies, so it doesn't fire and the endpoint is kept.
	aws := ComputePlan(onPremBase(SourceConfluentPlatform, func(p *Profile) {
		p.SourceCloud = "AWS"
	}))
	if haHas(aws.HumanAssist, trigger) {
		t.Errorf("CP on AWS private should not fire: %+v", aws.HumanAssist.Triggers)
	}
	if !strings.Contains(aws.Networking.Value, "Egress PrivateLink Endpoint") {
		t.Errorf("CP on AWS private should keep the Egress PrivateLink Endpoint, got %q", aws.Networking.Value)
	}

	// Starting fresh on the same private on-prem source: the alternative promises a
	// designed private link, not a self-serve mechanism.
	for _, st := range []SourceType{SourceConfluentPlatform, SourceApacheKafka} {
		sf := ComputePlan(onPremBase(st, func(p *Profile) { p.NeedsDataMigration = "No" }))
		if sf.Switchover.Alternative == nil || !strings.Contains(sf.Switchover.Alternative.Reason, "we'd design the private link with you") {
			t.Errorf("%s on-prem private start fresh alternative = %+v, want the designed-private-link copy", st, sf.Switchover.Alternative)
		}
	}

	// Starting fresh: no data moves over a cluster link, so it doesn't fire.
	fresh := ComputePlan(onPremBase(SourceConfluentPlatform, func(p *Profile) {
		p.NeedsDataMigration = "No"
	}))
	if haHas(fresh.HumanAssist, trigger) {
		t.Errorf("on-prem private start-fresh should not fire: %+v", fresh.HumanAssist.Triggers)
	}

	// Below the Cluster Linking floor: data moves over Replicator instead of a
	// cluster link, so it doesn't fire.
	belowFloor := ComputePlan(onPremBase(SourceConfluentPlatform, func(p *Profile) {
		p.KafkaVersion = "Older than 2.4"
	}))
	if haHas(belowFloor.HumanAssist, trigger) {
		t.Errorf("on-prem private below the Cluster Linking floor should not fire: %+v", belowFloor.HumanAssist.Triggers)
	}
}

// TestComputePlan_OnPremPrivateClusterLinkEitherScope checks the handoff fires when
// either the infra-wide answer or this cluster's own answer resolves to a cluster
// link.
func TestComputePlan_OnPremPrivateClusterLinkEitherScope(t *testing.T) {
	const trigger = "onprem_private_cluster_link"
	for name, mut := range map[string]func(*Profile){
		"infra-wide Yes, own No": func(p *Profile) { p.AnyAppNeedsDataMigration = "Yes"; p.NeedsDataMigration = "No" },
		"infra-wide No, own Yes": func(p *Profile) { p.AnyAppNeedsDataMigration = "No"; p.NeedsDataMigration = "Yes" },
	} {
		if cp := ComputePlan(onPremBase(SourceConfluentPlatform, mut)); !haHas(cp.HumanAssist, trigger) {
			t.Errorf("%s: trigger did not fire, triggers=%+v", name, cp.HumanAssist.Triggers)
		}
	}
	both := ComputePlan(onPremBase(SourceConfluentPlatform, func(p *Profile) { p.AnyAppNeedsDataMigration = "No"; p.NeedsDataMigration = "No" }))
	if haHas(both.HumanAssist, trigger) {
		t.Errorf("both No should not fire: %+v", both.HumanAssist.Triggers)
	}
}

// The on-prem Confluent Platform handoff names only the target cloud's private
// link (the resolved target: AWS when none is set), and an open data-migration answer
// leads with the conditional promise before the facts.
func TestComputePlan_OnPremHandoffCopy(t *testing.T) {
	reason := func(target, needsData string) string {
		return ComputePlan(onPremBase(SourceConfluentPlatform, func(p *Profile) {
			p.TargetCloud, p.NeedsDataMigration, p.AnyAppNeedsDataMigration = target, needsData, needsData
		})).HumanAssist.Reason
	}
	links := []string{"Direct Connect", "ExpressRoute", "Cloud Interconnect"}
	for target, want := range map[string]string{"AWS": links[0], "Azure": links[1], "GCP": links[2]} {
		got := reason(target, "Yes")
		for _, l := range links {
			if has := strings.Contains(got, l); has != (l == want) {
				t.Errorf("target %s: contains %q = %v in %q", target, l, has, got)
			}
		}
	}
	// An on-prem source with no target cloud defaults to AWS, so it names Direct Connect.
	if got := reason("", "Yes"); !strings.Contains(got, "Direct Connect link") || strings.Contains(got, "ExpressRoute") {
		t.Errorf("unset target should resolve to AWS and name Direct Connect only: %q", got)
	}

	const lead = "If you move existing data, we'll design the private path with you."
	if got := reason("AWS", ""); !strings.HasPrefix(got, lead) || !strings.Contains(got, "source-initiated cluster link") {
		t.Errorf("open answer should lead with the promise then the facts: %q", got)
	}
	if got := reason("AWS", "Yes"); strings.Contains(got, lead) || !strings.HasSuffix(got, "Confluent Server brokers. We'll confirm your version and connectivity with you and design the link together.") {
		t.Errorf("a settled Yes should drop the lead and close with the confirm-and-design promise: %q", got)
	}
	// An open answer leads with the promise and does not also promise to design the link.
	if got := reason("AWS", ""); strings.Contains(got, "design the link together") || !strings.HasSuffix(got, "Confluent Server brokers.") {
		t.Errorf("an open answer should end at the facts, with no second design promise: %q", got)
	}
	ak := ComputePlan(onPremBase(SourceApacheKafka, func(p *Profile) { p.NeedsDataMigration, p.AnyAppNeedsDataMigration = "", "" })).HumanAssist.Reason
	if !strings.HasPrefix(ak, lead) || !strings.Contains(ak, "Replicator running in your network") {
		t.Errorf("Apache Kafka open answer: %q", ak)
	}
}

// Replicator citations point at the Replicator failover page.
func TestSrcReplicatorCitation(t *testing.T) {
	if want := "https://docs.confluent.io/platform/current/multi-dc-deployments/replicator/replicator-failover.html"; srcReplicator != want {
		t.Errorf("srcReplicator = %q, want %q", srcReplicator, want)
	}
}
