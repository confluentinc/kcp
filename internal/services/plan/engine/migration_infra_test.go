package engine

import "testing"

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

		// Private unauthenticated on Enterprise: external outbound. Dedicated -> specialist.
		{"private unauth, enterprise", prof(false, authUnauth), TierEnterprise, 3, 0},
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

		// mTLS / nothing detected -> specialist.
		{"private mtls", prof(false, authMTLS), TierEnterprise, 0, 0},
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
}
