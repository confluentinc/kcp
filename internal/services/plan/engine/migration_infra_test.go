package engine

import (
	"strings"
	"testing"
)

func TestMigrationInfraDecision(t *testing.T) {
	prof := func(public bool, auths ...string) Profile {
		p := Profile{SourceAuthTypes: auths}
		if public {
			p.SourcePublicAccess = "Yes"
		}
		return p
	}

	cases := []struct {
		name    string
		p       Profile
		tier    Tier
		want    int
		wantAlt int // expected AlternativeType (0 = none)
	}{
		// A public source is a direct link (Enterprise); a Dedicated target always
		// routes to a specialist, regardless of source.
		{"public scram, enterprise", prof(true, authSCRAM), TierEnterprise, 1, 0},
		{"public scram, dedicated", prof(true, authSCRAM), TierDedicated, 0, 0},

		// Private SCRAM on Enterprise: external outbound link with a jump-cluster
		// alternative. Dedicated -> specialist.
		{"private scram, enterprise", prof(false, authSCRAM), TierEnterprise, 2, 4},
		{"private scram, dedicated", prof(false, authSCRAM), TierDedicated, 0, 0},

		// Private unauthenticated: CC has no unauthenticated link path, so add a SASL/SCRAM
		// listener (type 2) — not a plaintext link, and no jump-cluster alternative, since
		// type 4 also signs in to the source with SCRAM. Dedicated -> specialist.
		{"private unauth, enterprise", prof(false, authUnauth), TierEnterprise, 2, 0},
		{"private unauth, dedicated", prof(false, authUnauth), TierDedicated, 0, 0},

		// Private IAM on PROVISIONED MSK, Enterprise: jump cluster (type 5), with a
		// SASL/SCRAM listener + direct link (type 2) as the alternative. Dedicated ->
		// specialist.
		{"private iam, enterprise", prof(false, authAWSIAM), TierEnterprise, 5, 2},
		{"private iam, dedicated", prof(false, authAWSIAM), TierDedicated, 0, 0},

		// MSK Serverless is IAM-only: you cannot add a SASL/SCRAM listener, so type 5
		// carries no SCRAM-listener alternative (Replicator is offered by the data
		// verdict instead).
		{"private iam, serverless", Profile{MSKClusterType: MSKServerless, SourceAuthTypes: []string{authAWSIAM}}, TierEnterprise, 5, 0},

		// SASL/SCRAM is preferred over IAM for the link.
		{"private iam+scram prefers scram", prof(false, authAWSIAM, authSCRAM), TierEnterprise, 2, 4},

		// mTLS and SASL/PLAIN both need a SASL/SCRAM external outbound link (type 2) with an
		// "add a SASL/SCRAM listener" prerequisite (kcp's link is SCRAM-only), with no
		// jump-cluster alternative: type 4 signs in with SCRAM, so it doesn't avoid the
		// listener. Only genuinely-undetected auth -> specialist.
		{"private mtls", prof(false, authMTLS), TierEnterprise, 2, 0},
		{"private sasl-plain", prof(false, authSASLPlain), TierEnterprise, 2, 0},
		// Kerberos likewise gets no jump-cluster alternative.
		{"private kerberos", prof(false, authKerberos), TierEnterprise, 2, 0},
		{"private none", prof(false), TierEnterprise, 0, 0},
	}
	for _, tc := range cases {
		got := MigrationInfraDecision(tc.p, tc.tier)
		if got.Type != tc.want {
			t.Errorf("%s: type=%d, want %d", tc.name, got.Type, tc.want)
		}
		if got.AlternativeType != tc.wantAlt {
			t.Errorf("%s: alt type=%d, want %d", tc.name, got.AlternativeType, tc.wantAlt)
		}
		// Types 4 and 5 (and only those) are jump clusters.
		wantJump := got.Type == 4 || got.Type == 5
		if got.JumpCluster != wantJump {
			t.Errorf("%s: jumpCluster=%v, want %v (type %d)", tc.name, got.JumpCluster, wantJump, got.Type)
		}
	}

	// A government target never auto-maps: Cluster Linking isn't available there.
	if got := MigrationInfraDecision(Profile{SourceAuthTypes: []string{authSCRAM}, TargetIsGovCloud: "Yes"}, TierEnterprise); got.Type != 0 {
		t.Errorf("gov target: type=%d, want 0", got.Type)
	}

	// An OSK/CP source known to run outside AWS can't use the AWS-VPC migration-infra,
	// so it routes to a specialist — but Cluster Linking still applies (clusterLinkable).
	for _, cloud := range []string{"Azure", "GCP", "On-prem or other"} {
		got := MigrationInfraDecision(Profile{SourceType: SourceApacheKafka, SourceCloud: cloud, SourceAuthTypes: []string{authSCRAM}}, TierEnterprise)
		if got.Type != 0 || !got.SpecialistClusterLinkable {
			t.Errorf("non-AWS (%s) OSK source: type=%d clusterLinkable=%v, want 0/true", cloud, got.Type, got.SpecialistClusterLinkable)
		}
	}
	// An AWS (or unknown-cloud) OSK source still auto-maps to a standard type.
	if got := MigrationInfraDecision(Profile{SourceType: SourceApacheKafka, SourceCloud: "AWS", SourceAuthTypes: []string{authSCRAM}}, TierEnterprise); got.Type != 2 {
		t.Errorf("AWS OSK source: type=%d, want 2", got.Type)
	}
}

// A Kerberos source on AWS gets the SASL/SCRAM listener link (like SASL/PLAIN-only)
// rather than the undetected-auth specialist fallback, and its source-credentials
// note renders.
func TestMigrationInfra_KerberosOnAWS(t *testing.T) {
	p := Profile{SourceType: SourceApacheKafka, SourceCloud: "AWS", SourceAuthTypes: []string{authKerberos}, RequiresPrivateField: "Yes", NeedsDataMigration: "Yes"}
	got := MigrationInfraDecision(p, TierEnterprise)
	if got.Type != 2 || got.AlternativeType != 0 || got.Alternative != "" {
		t.Errorf("kerberos on AWS: type=%d alt=%d %q, want 2 with no alternative", got.Type, got.AlternativeType, got.Alternative)
	}
	if !strings.Contains(got.Rationale, "SASL/SCRAM listener") {
		t.Errorf("rationale = %q, want the SCRAM-listener note", got.Rationale)
	}
}

// An on-premises OSK/CP source has no cloud network, so the rationale says so rather
// than claiming the standard link still applies with only its networking specialist-wired.
func TestMigrationInfra_OnPremText(t *testing.T) {
	got := MigrationInfraDecision(Profile{SourceType: SourceConfluentPlatform, SourceCloud: "On-prem or other", SourceAuthTypes: []string{authSCRAM}}, TierEnterprise)
	if got.Type != 0 || !got.SpecialistClusterLinkable {
		t.Fatalf("on-prem source: type=%d clusterLinkable=%v, want 0/true", got.Type, got.SpecialistClusterLinkable)
	}
	if !strings.Contains(got.Rationale, "runs on-premises or in another environment") ||
		!strings.Contains(got.Rationale, "from an AWS VPC") ||
		strings.Contains(got.Rationale, "standard cutover steps") {
		t.Errorf("on-prem rationale = %q", got.Rationale)
	}
	// Azure/GCP keep the "Cluster Linking still applies" wording.
	az := MigrationInfraDecision(Profile{SourceType: SourceApacheKafka, SourceCloud: "Azure", SourceAuthTypes: []string{authSCRAM}}, TierEnterprise)
	if !strings.Contains(az.Rationale, "runs in Azure") || !strings.Contains(az.Rationale, "standard cutover steps below are unchanged") {
		t.Errorf("Azure rationale = %q", az.Rationale)
	}
}

// An AWS IAM source with no SASL/SCRAM gets the jump cluster even alongside plaintext
// or mTLS (the production cluster stays untouched), and SASL/SCRAM still wins.
func TestMigrationInfra_IAMComboPrefersJumpCluster(t *testing.T) {
	for _, auths := range [][]string{{authAWSIAM, authUnauth}, {authAWSIAM, authMTLS}, {authUnauth, authAWSIAM}} {
		got := MigrationInfraDecision(Profile{SourceAuthTypes: auths, SourceAccessibility: "Private"}, TierEnterprise)
		if got.Type != miPrivateJumpIAM || !got.JumpCluster {
			t.Errorf("%v: type=%d jump=%v, want %d/true", auths, got.Type, got.JumpCluster, miPrivateJumpIAM)
		}
	}
	if got := MigrationInfraDecision(Profile{SourceAuthTypes: []string{authAWSIAM, authSCRAM}}, TierEnterprise); got.Type != miPrivateOutSCRAM {
		t.Errorf("IAM+SCRAM: type=%d, want %d", got.Type, miPrivateOutSCRAM)
	}
}

func TestMigrationInfra_IAMNonAWSTargetIsSpecialist(t *testing.T) {
	for _, cloud := range []string{"Azure", "GCP"} {
		for _, serverless := range []bool{false, true} {
			p := Profile{SourceAuthTypes: []string{authAWSIAM}, SourceAccessibility: "Private", TargetCloud: cloud}
			if serverless {
				p.MSKClusterType = MSKServerless
			}
			got := MigrationInfraDecision(p, TierEnterprise)
			if got.Type != 0 || got.JumpCluster || got.SpecialistClusterLinkable || !strings.Contains(got.Rationale, "built in AWS") {
				t.Errorf("%s serverless=%v: %+v", cloud, serverless, got)
			}
		}
	}
	// kcp's private link types are AWS-only, so a private SCRAM (or any non-IAM) source
	// going to Azure or GCP is also designed with a specialist, with Cluster Linking kept.
	for _, cloud := range []string{"Azure", "GCP"} {
		for _, auths := range [][]string{{authSCRAM}, {authAWSIAM, authSCRAM}, {authMTLS}, {authUnauth}} {
			got := MigrationInfraDecision(Profile{SourceAuthTypes: auths, SourceAccessibility: "Private", TargetCloud: cloud}, TierEnterprise)
			if got.Type != 0 || got.JumpCluster || !got.SpecialistClusterLinkable || got.Alternative != "" {
				t.Errorf("%v on %s: %+v", auths, cloud, got)
			}
		}
		// A public source links directly over the internet, whatever the target.
		if got := MigrationInfraDecision(Profile{SourceAuthTypes: []string{authSCRAM}, SourcePublicAccess: "Yes", TargetCloud: cloud}, TierEnterprise); got.Type != miPublicSCRAM {
			t.Errorf("public SCRAM on %s: type=%d, want %d", cloud, got.Type, miPublicSCRAM)
		}
	}
	// The jump-cluster alternative names the source's network, not the target's.
	if got := MigrationInfraDecision(Profile{SourceAuthTypes: []string{authSCRAM}, TargetCloud: "AWS"}, TierEnterprise); !strings.Contains(got.Alternative, "in your VPC (") {
		t.Errorf("alternative = %q, want the source's VPC noun", got.Alternative)
	}
}
