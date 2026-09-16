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
		func(p *Profile) { p.PartitionBand = "Over 96,000" },                                 // band cap
		func(p *Profile) { p.TargetCloud = "Azure"; p.SourceAuthTypes = []string{authMTLS} }, // mTLS off AWS
		func(p *Profile) { p.TargetCloud = "GCP"; p.CCEgressRequired = "Yes" },               // GCP outbound
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

func TestComputePlan_MtlsTargetForcesDedicatedNonAWS(t *testing.T) {
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
