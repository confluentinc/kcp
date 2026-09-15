package engine

import (
	"strings"
	"testing"
)

// swOf calls switchoverDecision with no tier — the tier-less mechanism
// resolution (unknown destination tier).
func swOf(mut func(*Profile)) SwitchoverResult {
	p := baseProfile(mut)
	return switchoverDecision(p, sizingBand(p), "")
}

func TestSwitchover_BelowFloorReplicator(t *testing.T) {
	sw := swOf(func(p *Profile) { p.KafkaVersion = "Older than 2.4" })
	if sw.Value != "Confluent Replicator" || sw.MM2 {
		t.Errorf("below floor: value=%q mm2=%v, want Confluent Replicator / false", sw.Value, sw.MM2)
	}
	if sw.Action == nil || *sw.Action != "Set up Confluent Replicator" {
		t.Errorf("replicator action = %v", sw.Action)
	}
	if !strings.Contains(sw.Reason, "needs a license") || !sw.TechAssist {
		t.Errorf("replicator reason=%q techAssist=%v", sw.Reason, sw.TechAssist)
	}
	// Data migration not explicitly committed (unset) -> "Start fresh" stays a real
	// alternative.
	if sw.Alternative == nil || sw.Alternative.Value != "Start fresh" {
		t.Errorf("replicator alternative = %v, want Start fresh", sw.Alternative)
	}
}

// When the customer said they must move existing data, "Start fresh" (drop history)
// contradicts that stated requirement, so the Replicator plan suppresses it.
func TestSwitchover_ReplicatorSuppressesStartFreshWhenDataRequired(t *testing.T) {
	sw := swOf(func(p *Profile) {
		p.KafkaVersion = "Older than 2.4"
		p.NeedsDataMigration = "Yes"
	})
	if sw.Value != "Confluent Replicator" {
		t.Fatalf("value=%q, want Confluent Replicator", sw.Value)
	}
	if sw.Alternative != nil {
		t.Errorf("Start fresh alternative should be suppressed when data must move, got %v", sw.Alternative)
	}
}

func TestSwitchover_IBPBelowBlocksClusterLinking(t *testing.T) {
	sw := swOf(func(p *Profile) {
		p.KafkaVersion = "2.4-2.9"
		p.InterBrokerProtocol = "No"
		p.DowntimeTolerance = "Minutes per service"
	})
	if sw.Value != "Confluent Replicator" {
		t.Errorf("IBP below: value=%q, want Confluent Replicator", sw.Value)
	}
}

func TestSwitchover_MM2NeverRecommended(t *testing.T) {
	for _, v := range []string{"Older than 2.4", "2.4-2.9", "3.0 or newer"} {
		p := baseProfile(func(p *Profile) {
			p.KafkaVersion = v
			p.InterBrokerProtocol = "No"
			p.DowntimeTolerance = "Minutes per service"
		})
		sw := switchoverDecision(p, sizingBand(p), TierEnterprise)
		if strings.Contains(strings.ToLower(sw.Value), "mirrormaker") {
			t.Errorf("%s recommended MM2: %q", v, sw.Value)
		}
	}
}

func TestSwitchover_DowntimeHeldWhenUnanswered(t *testing.T) {
	sw := swOf(nil) // no downtime_tolerance
	if !sw.Held || sw.Value != "Pending" {
		t.Errorf("unanswered downtime: held=%v value=%q, want true / Pending", sw.Held, sw.Value)
	}
}

func TestSwitchover_CutoverStyles(t *testing.T) {
	cases := map[string]string{
		"A scheduled window, one service at a time": "Cluster Linking, one service at a time",
		"A scheduled window, all at once":           "Cluster Linking, all at once",
	}
	for tol, want := range cases {
		if got := swOf(func(p *Profile) { p.DowntimeTolerance = tol }).Value; got != want {
			t.Errorf("%q -> %q, want %q", tol, got, want)
		}
	}
}

func TestSwitchover_GatewayStylesStateLicense(t *testing.T) {
	zero := swOf(func(p *Profile) { p.DowntimeTolerance = "Zero downtime" })
	if zero.Value != "Gateway cutover (no downtime): Gateway license required" || !zero.TechAssist {
		t.Errorf("zero downtime: value=%q techAssist=%v", zero.Value, zero.TechAssist)
	}
	if zero.Action == nil || *zero.Action != "Set up the Confluent Gateway" {
		t.Errorf("zero downtime action=%v", zero.Action)
	}
	if !strings.Contains(zero.Reason, "Gateway license and Confluent for Kubernetes") {
		t.Errorf("zero downtime reason should name what to acquire: %q", zero.Reason)
	}
	if strings.Contains(strings.ToLower(zero.Reason), "blue") {
		t.Errorf("no Blue/Green offer expected in: %q", zero.Reason)
	}

	secs := swOf(func(p *Profile) { p.DowntimeTolerance = "Seconds per service" })
	if secs.Value != "Cluster Linking, near-zero downtime: Gateway license required" {
		t.Errorf("seconds per service value=%q", secs.Value)
	}
}

func TestSwitchover_EscalationToGatewayMediated(t *testing.T) {
	// High blast radius (Band 3) escalates even when coordination is Easy.
	blast := swOf(func(p *Profile) {
		p.PartitionBand = "30,000–96,000"
		p.DowntimeTolerance = "Minutes per service"
		p.ClientCoordinationBurden = "Easy (few clients, one team)"
	})
	if !blast.GatewayMediated {
		t.Errorf("high blast radius: gatewayMediated=%v, want true", blast.GatewayMediated)
	}
	if !strings.Contains(blast.Value, "Gateway-mediated") || !strings.Contains(blast.Value, "Gateway license required") {
		t.Errorf("escalated value=%q", blast.Value)
	}
	// Hard client-coordination burden escalates on its own.
	hard := swOf(func(p *Profile) {
		p.DowntimeTolerance = "A scheduled window, one service at a time"
		p.ClientCoordinationBurden = "Hard (many clients and teams)"
	})
	if !strings.Contains(hard.Value, "Gateway-mediated") {
		t.Errorf("hard coordination value=%q, want Gateway-mediated", hard.Value)
	}
}

func TestSwitchover_EOSCaveat(t *testing.T) {
	sw := swOf(func(p *Profile) {
		p.EosStreams = []string{"Exactly-once or transactions"}
		p.DowntimeTolerance = "Minutes per service"
	})
	if !strings.Contains(sw.Reason, "Cluster Linking does not carry") {
		t.Errorf("EOS caveat missing from reason: %q", sw.Reason)
	}
}

func TestSwitchover_StartFresh(t *testing.T) {
	sw := swOf(func(p *Profile) { p.NeedsDataMigration = "No"; p.DowntimeTolerance = "Minutes per service" })
	if sw.Value != "Start fresh" || sw.Action != nil {
		t.Errorf("start fresh: value=%q action=%v", sw.Value, sw.Action)
	}
}

func TestSwitchover_ServerlessJumpCluster(t *testing.T) {
	// Serverless + Enterprise destination + data move -> jump cluster; Replicator the alternative.
	p := Profile{
		SourcePlatform: "Amazon MSK", MSKClusterType: MSKServerless, TargetCloud: "AWS",
		SourceAuthTypes: []string{authAWSIAM}, PartitionBand: "2,500–30,000",
		NeedsDataMigration: "Yes", DowntimeTolerance: "Zero downtime",
	}
	sw := switchoverDecision(p, sizingBand(p), TierEnterprise)
	if !strings.Contains(strings.ToLower(sw.Value), "jump cluster") {
		t.Errorf("serverless: value=%q, want jump cluster", sw.Value)
	}
	if sw.Alternative == nil || sw.Alternative.Value != "Confluent Replicator" {
		t.Errorf("serverless alternative=%+v, want Confluent Replicator", sw.Alternative)
	}
	// The jump cluster and the alternative both hedge the CP Enterprise license.
	joined := strings.Join(sw.Cons, " ")
	if !strings.Contains(joined, "Confluent Platform Enterprise license may be required") {
		t.Errorf("jump-cluster cons should hedge the license: %v", sw.Cons)
	}
	if sw.KCPResource == nil {
		t.Errorf("serverless jump cluster should carry a KCP resource link")
	}
}

// TestSwitchover_GovForcesReplicator checks the gov-cloud guard: a government
// target lacks Cluster Linking, so an Enterprise + data-migration profile that
// would otherwise get Cluster Linking is routed to Replicator instead — matching
// schemaDecision and MigrationInfraDecision. (Latent today: gov has no intake
// path, so this changes no current output.)
func TestSwitchover_GovForcesReplicator(t *testing.T) {
	p := baseProfile(func(p *Profile) {
		p.TargetIsGovCloud = "Yes"
		p.NeedsDataMigration = "Yes"
		p.DowntimeTolerance = "Minutes per service"
	})
	sw := switchoverDecision(p, sizingBand(p), TierEnterprise)
	if !sw.Replicator || sw.Value != "Confluent Replicator" {
		t.Errorf("gov target: value=%q replicator=%v, want Confluent Replicator / true", sw.Value, sw.Replicator)
	}
	if strings.Contains(sw.Value, "Cluster Linking") {
		t.Errorf("gov target must not recommend Cluster Linking, got %q", sw.Value)
	}
	// A non-gov profile with the same shape still gets Cluster Linking.
	nonGov := switchoverDecision(baseProfile(func(p *Profile) {
		p.NeedsDataMigration = "Yes"
		p.DowntimeTolerance = "Minutes per service"
	}), sizingBand(baseProfile(nil)), TierEnterprise)
	if nonGov.Replicator || !strings.Contains(nonGov.Value, "Cluster Linking") {
		t.Errorf("non-gov control: value=%q replicator=%v, want a Cluster Linking style", nonGov.Value, nonGov.Replicator)
	}
}

// TestSwitchover_ClusterLinkingCreditsDowntimeAnswer checks the cluster-linking
// cutover Reason leads with the downtime-tolerance answer, lowercased (A6).
func TestSwitchover_ClusterLinkingCreditsDowntimeAnswer(t *testing.T) {
	tol := "A scheduled window, all at once"
	sw := swOf(func(p *Profile) { p.DowntimeTolerance = tol })
	want := "Based on your answer (" + strings.ToLower(tol) + ")"
	if !strings.Contains(sw.Reason, want) {
		t.Errorf("cluster-linking reason should carry %q: %q", want, sw.Reason)
	}
}
