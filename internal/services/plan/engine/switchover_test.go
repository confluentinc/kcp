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
		p.KafkaVersion = "2.4–2.9"
		p.InterBrokerProtocol = "No"
		p.DowntimeTolerance = "Minutes per service"
	})
	if sw.Value != "Confluent Replicator" {
		t.Errorf("IBP below: value=%q, want Confluent Replicator", sw.Value)
	}
}

func TestSwitchover_MM2NeverRecommended(t *testing.T) {
	for _, v := range []string{"Older than 2.4", "2.4–2.9", "3.0 or newer"} {
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
		"A scheduled window, one service at a time": "Cluster Linking, one service at a time (Stop-Wait-Restart)",
		"A scheduled window, all at once":           "Cluster Linking, all at once (Restart-All-At-Once)",
	}
	for tol, want := range cases {
		if got := swOf(func(p *Profile) { p.DowntimeTolerance = tol }).Value; got != want {
			t.Errorf("%q -> %q, want %q", tol, got, want)
		}
	}
}

// TestSwitchover_AllAtOnceLeadNamesTheWindow checks the cutover-lead wording
// depends on style: the all-at-once style says "moving all your clients over
// together in one scheduled window", never "cut clients over when you are
// ready rather than all at once" — that phrase is backwards for a style whose
// whole point is one scheduled window. Other styles keep the ready-when-you-are
// wording.
func TestSwitchover_AllAtOnceLeadNamesTheWindow(t *testing.T) {
	allAtOnce := swOf(func(p *Profile) { p.DowntimeTolerance = "A scheduled window, all at once" })
	if !strings.Contains(allAtOnce.Reason, "moving all your clients over together in one scheduled window") {
		t.Errorf("all-at-once reason = %q, want the scheduled-window lead", allAtOnce.Reason)
	}
	if strings.Contains(allAtOnce.Reason, "cut clients over when you are ready rather than all at once") {
		t.Errorf("all-at-once reason must not say ready-when-you-are: %q", allAtOnce.Reason)
	}

	oneAtATime := swOf(func(p *Profile) { p.DowntimeTolerance = "A scheduled window, one service at a time" })
	if !strings.Contains(oneAtATime.Reason, "cut clients over when you are ready rather than all at once") {
		t.Errorf("one-service-at-a-time reason = %q, want the ready-when-you-are lead", oneAtATime.Reason)
	}
	if strings.Contains(oneAtATime.Reason, "moving all your clients over together in one scheduled window") {
		t.Errorf("one-service-at-a-time reason must not say scheduled-window: %q", oneAtATime.Reason)
	}
}

// TestSwitchover_AllAtOnceLeadOnEscalation checks the same style-dependent
// wording where the reason additionally escalates to Gateway-mediated (high
// blast radius) but the source can't use the Gateway (AWS IAM), which falls
// back to the plain Cluster Linking style with the IAM note appended.
func TestSwitchover_AllAtOnceLeadOnEscalation(t *testing.T) {
	allAtOnce := swOf(func(p *Profile) {
		p.PartitionBand = "30,000–96,000" // blast radius high
		p.DowntimeTolerance = "A scheduled window, all at once"
		p.SourceAuthTypes = []string{authAWSIAM} // gatewayUsable = false
	})
	if !strings.Contains(allAtOnce.Reason, "so we recommend a Cluster Linking cutover, moving all your clients over "+
		"together in one scheduled window.") {
		t.Errorf("all-at-once escalation-fallback reason = %q, want the scheduled-window lead", allAtOnce.Reason)
	}

	oneAtATime := swOf(func(p *Profile) {
		p.PartitionBand = "30,000–96,000"
		p.DowntimeTolerance = "A scheduled window, one service at a time"
		p.SourceAuthTypes = []string{authAWSIAM}
	})
	if !strings.Contains(oneAtATime.Reason, "cut clients over when you are ready rather than all at once") {
		t.Errorf("one-service-at-a-time escalation-fallback reason = %q, want the ready-when-you-are lead", oneAtATime.Reason)
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
	if secs.Value != "Cluster Linking, near-zero downtime (Stop-Restart-Repeat via Gateway): Gateway license required" {
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

// TestSwitchover_StartFreshAlternativeNamesMechanism checks that the "Start
// fresh" alternative names whichever mechanism resolveMechanism would actually
// pick with needs_data_migration forced to "Yes" — not a guess from isServerless
// alone — for every mechanism startFreshAlternative can resolve to. Each case
// also cross-checks against the plan actually computed with data migration Yes,
// so the alternative can never drift from what switchoverDecision would plan.
func TestSwitchover_StartFreshAlternativeNamesMechanism(t *testing.T) {
	cases := []struct {
		name       string
		tier       Tier
		mut        func(*Profile)
		wantReason string
		wantValue  string // Value of the real plan when needs_data_migration = Yes
	}{
		{
			name: "plain cluster linking",
			tier: TierEnterprise,
			mut:  func(p *Profile) {},
			wantReason: "If you need your existing messages on the new cluster, we would use Cluster Linking to mirror " +
				"topics and offsets continuously, then cut clients over once they have caught up. Change your answer " +
				"above and we will plan that instead.",
		},
		{
			name: "jump cluster",
			tier: TierEnterprise,
			mut:  func(p *Profile) { p.MSKClusterType = MSKServerless; p.SourceAuthTypes = []string{authAWSIAM} },
			wantReason: "If you need your existing messages on the new cluster, we would use Cluster Linking through a " +
				"temporary jump cluster to mirror topics and offsets continuously, then cut clients over once they have " +
				"caught up. The jump cluster is there because AWS IAM credentials cannot cross a cluster link directly. " +
				"Change your answer above and we will plan that instead.",
		},
		{
			name: "replicator, Standard target",
			tier: TierStandard,
			mut:  func(p *Profile) {},
			wantReason: "If you need your existing messages on the new cluster, we would use Confluent Replicator to " +
				"copy them across, then cut clients over. Cluster Linking needs an Enterprise or Dedicated destination, " +
				"so it is not available into a Standard cluster. Change your answer above and we will plan that instead.",
		},
		{
			name: "replicator, version floor",
			tier: TierEnterprise,
			mut:  func(p *Profile) { p.KafkaVersion = "Older than 2.4" },
			wantReason: "If you need your existing messages on the new cluster, we would use Confluent Replicator to " +
				"copy them across, then cut clients over. Your source is below the Cluster Linking floor: Kafka 2.4, " +
				"Confluent Platform 5.4, inter-broker protocol (IBP) 2.8. Change your answer above and we will plan that instead.",
		},
		{
			name: "replicator, IBP floor",
			tier: TierEnterprise,
			mut:  func(p *Profile) { p.KafkaVersion = "2.4–2.9"; p.InterBrokerProtocol = "No" },
			wantReason: "If you need your existing messages on the new cluster, we would use Confluent Replicator to " +
				"copy them across, then cut clients over. Your inter-broker protocol is below 2.8, so Cluster Linking " +
				"is not available even though your Kafka version qualifies. Change your answer above and we will plan that instead.",
		},
		{
			name: "replicator, MSK Serverless",
			tier: TierStandard,
			mut:  func(p *Profile) { p.MSKClusterType = MSKServerless; p.SourceAuthTypes = []string{authAWSIAM} },
			wantReason: "If you need your existing messages on the new cluster, we would use Confluent Replicator to " +
				"copy them across, then cut clients over. Cluster Linking is not an option from MSK Serverless, which " +
				"only supports AWS IAM authentication. Change your answer above and we will plan that instead.",
		},
		{
			// Latent today (gov has no intake path), but resolveMechanism already
			// routes it to Replicator, so the alternative must name it too.
			name: "replicator, gov cloud",
			tier: TierEnterprise,
			mut:  func(p *Profile) { p.TargetIsGovCloud = "Yes" },
			wantReason: "If you need your existing messages on the new cluster, we would use Confluent Replicator to " +
				"copy them across, then cut clients over. Confluent Cloud for Government doesn't offer fully managed Cluster Linking. " +
				"Change your answer above and we will plan that instead.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := baseProfile(func(p *Profile) {
				p.NeedsDataMigration = "No"
				tc.mut(p)
			})
			sw := switchoverDecision(p, sizingBand(p), tc.tier)
			if sw.Value != "Start fresh" || sw.Alternative == nil {
				t.Fatalf("value=%q alternative=%v, want Start fresh with an alternative", sw.Value, sw.Alternative)
			}
			if sw.Alternative.Reason != tc.wantReason {
				t.Errorf("alternative reason =\n%q\nwant\n%q", sw.Alternative.Reason, tc.wantReason)
			}

			// Cross-check: the mechanism the alternative names must match what
			// switchoverDecision actually plans when needs_data_migration is Yes.
			p2 := baseProfile(func(p *Profile) {
				p.NeedsDataMigration = "Yes"
				p.DowntimeTolerance = "Minutes per service"
				tc.mut(p)
			})
			real := switchoverDecision(p2, sizingBand(p2), tc.tier)
			wantMechanism := resolveMechanism(p2, tc.tier, "Yes")
			switch wantMechanism.Mechanism {
			case "jump-cluster":
				if !strings.Contains(strings.ToLower(real.Value), "jump cluster") {
					t.Errorf("real plan value=%q, want a jump cluster plan", real.Value)
				}
			case "replicator":
				if real.Value != "Confluent Replicator" {
					t.Errorf("real plan value=%q, want Confluent Replicator", real.Value)
				}
			case "cluster-linking":
				if !strings.Contains(real.Value, "Cluster Linking") {
					t.Errorf("real plan value=%q, want a Cluster Linking plan", real.Value)
				}
			}
		})
	}
}

func TestKCPResourceCaveat_SourceAuthOnly(t *testing.T) {
	msk := func(auth ...string) Profile {
		return Profile{SourcePlatform: "Amazon MSK", SourceAuthTypes: auth}
	}
	// SCRAM source: no caveat, even when mTLS is picked only as the target.
	p := msk(authSCRAM)
	p.TargetIdentityModel = []string{"mTLS"}
	if r := kcpResource(p, TierEnterprise); r == nil || r.Caveat != "" {
		t.Errorf("scram source + mTLS target: %+v", r)
	}
	// mTLS-only MSK source.
	r := kcpResource(msk(authMTLS), TierEnterprise)
	if r == nil || !strings.Contains(r.Caveat, "signs in over SASL/SCRAM, so if your source is mTLS-only, add a SASL/SCRAM listener") {
		t.Errorf("msk mTLS-only: %+v", r)
	}
	// Apache Kafka source without SCRAM.
	ak := Profile{SourceType: SourceApacheKafka, SourceAuthTypes: []string{authKerberos}}
	r = kcpResource(ak, TierEnterprise)
	if r == nil || !strings.Contains(r.Caveat, "if your source is mTLS-, SASL/PLAIN- or Kerberos-only") {
		t.Errorf("apache kafka: %+v", r)
	}
}
