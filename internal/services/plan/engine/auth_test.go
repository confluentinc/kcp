package engine

import (
	"strings"
	"testing"
)

func authOf(p Profile) AuthResult { return authDecision(p, targetCloud(p), false) }

// credNotes renders the source-credential handling notes as "method: note …",
// mirroring how the switchover reason folds them in.
func credNotes(p Profile) string {
	var parts []string
	for _, h := range sourceCredentialHandling(p, nil, TierEnterprise) {
		parts = append(parts, h.Method+": "+h.Note)
	}
	return strings.Join(parts, " ")
}

func TestAuth_TargetCascade(t *testing.T) {
	// SASL/SCRAM, no target chosen -> API keys; the no-change note is present.
	scram := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{"SASL/SCRAM"} })
	if got := authOf(scram).Value; got != "API keys (SASL/PLAIN)" {
		t.Errorf("SCRAM default target = %q, want API keys (SASL/PLAIN)", got)
	}
	if n := credNotes(scram); !strings.Contains(n, "SCRAM") || !strings.Contains(n, "No change") {
		t.Errorf("SCRAM cred note = %q", n)
	}

	// mTLS source, no target -> preserved.
	if got := authOf(baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authMTLS} })).Value; got != "mTLS (preserved)" {
		t.Errorf("mTLS default = %q, want mTLS (preserved)", got)
	}

	// mTLS source but explicit OAuth pick -> explicit choice wins.
	oauth := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{authMTLS}
		p.TargetIdentityModel = []string{"OAuth"}
	})
	if got := authOf(oauth).Value; got != "OAuth (SASL/OAUTHBEARER)" {
		t.Errorf("explicit OAuth = %q, want OAuth (SASL/OAUTHBEARER)", got)
	}
}

func TestAuth_SourceCredentialNotes(t *testing.T) {
	// Pure IAM, provisioned: recommend the jump cluster (leaves the source untouched),
	// with the SASL/SCRAM listener as the alternative.
	iamProv := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authAWSIAM}; p.MSKClusterType = MSKProvisioned })
	if n := credNotes(iamProv); !strings.Contains(n, "which is why this plan runs a jump cluster.") ||
		strings.Contains(n, "leaves your production MSK cluster") ||
		!strings.Contains(n, "The alternative is to add a SASL/SCRAM listener on your MSK cluster that only the link uses.") {
		t.Errorf("IAM Provisioned note = %q", n)
	}
	// IAM alongside SASL/SCRAM: the link uses the existing SCRAM path, so there is no
	// IAM step and nothing changes on the source.
	iamScram := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{authAWSIAM, "SASL/SCRAM"}
		p.MSKClusterType = MSKProvisioned
	})
	if n := credNotes(iamScram); !strings.Contains(n, "Nothing to do for IAM here") || !strings.Contains(n, "your MSK cluster is unchanged") {
		t.Errorf("IAM+SCRAM note = %q", n)
	}
	// The IAM note ends with a short pointer at the auth verdict, not a restatement
	// of the IAM->RBAC story.
	if n := credNotes(iamScram); !strings.Contains(n, "Your plan's authentication recommendation covers how to recreate your AWS IAM permissions in Confluent Cloud.") {
		t.Errorf("IAM pointer text = %q", n)
	}
	iamSrvless := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authAWSIAM}; p.MSKClusterType = MSKServerless })
	if n := credNotes(iamSrvless); !strings.Contains(n, "jump cluster") {
		t.Errorf("IAM Serverless note = %q", n)
	}
	// Unauthenticated source over a cluster link: nothing to prepare on the source
	// CC has no unauthenticated link path, so the link uses a SASL/SCRAM listener the
	// source adds; the clients also need a supported method on Confluent Cloud after cutover.
	unauth := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{"None / plaintext"} })
	if n := credNotes(unauth); !strings.Contains(n, "add a SASL/SCRAM listener") ||
		!strings.Contains(n, "supported auth method on Confluent Cloud after cutover") {
		t.Errorf("unauth note = %q", n)
	}
}

// TestAuth_SetupAndIdentityLanding checks the mTLS/OAuth setup line (fires on
// every pick, preserved or not) and the per-method landing sentence: API keys
// map to service accounts, mTLS and OAuth map to identity pools.
func TestAuth_SetupAndIdentityLanding(t *testing.T) {
	const mtlsSetup = "Setup: mTLS needs your certificate authority uploaded to your Confluent Cloud organization and a certificate identity pool that matches your client certificates."
	const oauthSetup = "Setup: OAuth needs an identity provider and an identity pool set up in your Confluent Cloud organization."
	const apiKeysLanding = "Your applications' API keys belong to Confluent Cloud service accounts."

	// API keys only: no Setup line, no identity-pool landing.
	apiKeys := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{"SASL/SCRAM"} })
	if r := authOf(apiKeys).Reason; !strings.Contains(r, apiKeysLanding) || strings.Contains(r, "Setup:") || strings.Contains(r, "identity pools") {
		t.Errorf("API keys reason = %q", r)
	}

	// mTLS preserved: setup still fires (preserved or not), landing names
	// identity pools, not service accounts.
	preserved := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authMTLS} })
	if r := authOf(preserved).Reason; !strings.Contains(r, mtlsSetup) ||
		!strings.Contains(r, "mTLS clients map to Confluent Cloud identity pools.") ||
		strings.Contains(r, apiKeysLanding) {
		t.Errorf("mTLS preserved reason = %q", r)
	}

	// mTLS not preserved (source SCRAM, target mTLS chosen explicitly): same
	// setup line fires.
	notPreserved := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{"SASL/SCRAM"}
		p.TargetIdentityModel = []string{"mTLS"}
	})
	if r := authOf(notPreserved).Reason; !strings.Contains(r, mtlsSetup) {
		t.Errorf("mTLS not-preserved reason = %q, want setup line", r)
	}

	// OAuth: its own setup line and identity-pool landing.
	oauth := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{"SASL/SCRAM"}
		p.TargetIdentityModel = []string{"OAuth"}
	})
	if r := authOf(oauth).Reason; !strings.Contains(r, oauthSetup) ||
		!strings.Contains(r, "OAuth clients map to Confluent Cloud identity pools.") {
		t.Errorf("OAuth reason = %q", r)
	}

	// mTLS + OAuth: both setup lines joined with "; ", and the landing sentence
	// names both methods together.
	both := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{authMTLS}
		p.TargetIdentityModel = []string{"mTLS", "OAuth"}
	})
	if r := authOf(both).Reason; !strings.Contains(r, "Setup: mTLS needs your certificate authority uploaded to your Confluent Cloud organization and a certificate identity pool that matches your client certificates; OAuth needs an identity provider and an identity pool set up in your Confluent Cloud organization.") ||
		!strings.Contains(r, "mTLS and OAuth clients map to Confluent Cloud identity pools.") {
		t.Errorf("mTLS+OAuth reason = %q", r)
	}

	// API keys + mTLS: both landing sentences present, in that order.
	mixed := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{"SASL/SCRAM"}
		p.TargetIdentityModel = []string{"API keys (SASL/PLAIN)", "mTLS"}
	})
	r := authOf(mixed).Reason
	wantOrder := apiKeysLanding + " mTLS clients map to Confluent Cloud identity pools."
	if !strings.Contains(r, wantOrder) {
		t.Errorf("API keys + mTLS reason = %q, want %q in order", r, wantOrder)
	}
}

// TestAuth_Kerberos covers C6: a Confluent Cloud cluster link cannot use
// Kerberos to the source, Replicator can, and Confluent Cloud never accepts
// Kerberos from clients.
func TestAuth_Kerberos(t *testing.T) {
	kerberos := baseProfile(func(p *Profile) {
		p.SourceType = SourceApacheKafka
		p.SourceAuthTypes = []string{authKerberos}
	})

	// Cluster Linking (direct): Kerberos can't cross the link, so add a
	// SASL/SCRAM listener the link alone uses; clients keep Kerberos
	// until cutover.
	if n := credNotes(kerberos); !strings.Contains(n, "Kerberos can't be used by a cluster link from Confluent Cloud") ||
		!strings.Contains(n, "Add a SASL/SCRAM listener on your source cluster that only the link uses") ||
		!strings.Contains(n, "kcp's generated link signs in over SASL/SCRAM only, so choose SCRAM to use it; a SASL/PLAIN or mTLS link is created by hand.") ||
		!strings.Contains(n, "Your clients can keep Kerberos until cutover") {
		t.Errorf("Kerberos cluster-link note = %q", n)
	}

	// Replicator: runs on Kafka Connect in the customer's own environment, so it
	// reads the source with the existing Kerberos keytab — no change needed.
	replicatorNotes := func(p Profile) string {
		var parts []string
		for _, h := range sourceCredentialHandling(p, &SwitchoverResult{Replicator: true}, TierEnterprise) {
			parts = append(parts, h.Method+": "+h.Note)
		}
		return strings.Join(parts, " ")
	}
	if n := replicatorNotes(kerberos); !strings.Contains(n, "No change. Replicator runs on Kafka Connect in your own environment and reads your source with your existing Kerberos keytab.") {
		t.Errorf("Kerberos Replicator note = %q", n)
	}

	// Client-side: Confluent Cloud doesn't support Kerberos, so clients must move
	// off it before cutover — named to the resolved target picks (here, the
	// default cascade to API keys, since Kerberos isn't preservable).
	if r := authOf(kerberos).Reason; !strings.Contains(r, "Confluent Cloud doesn't support Kerberos. At cutover, move your Kerberos clients to API keys.") {
		t.Errorf("Kerberos client-side reason = %q", r)
	}

	// A multi-select target names each picked method, "A, B, or C" with an Oxford comma.
	multiKerberos := baseProfile(func(p *Profile) {
		p.SourceType = SourceApacheKafka
		p.SourceAuthTypes = []string{authKerberos}
		p.TargetIdentityModel = []string{"API keys (SASL/PLAIN)", "OAuth", "mTLS"}
	})
	if r := authOf(multiKerberos).Reason; !strings.Contains(r, "Confluent Cloud doesn't support Kerberos. At cutover, move your Kerberos clients to API keys, OAuth, or mTLS.") {
		t.Errorf("Kerberos multi-select client-side reason = %q", r)
	}
}

// TestAuth_KerberosSentenceOrder pins the auth-reason sentence order for a
// Kerberos source with an mTLS + OAuth target: lead, setup, credential landing,
// the Kerberos client note, then the access-control notes (including the
// identity-pool grant note).
func TestAuth_KerberosSentenceOrder(t *testing.T) {
	p := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{authKerberos}
		p.TargetIdentityModel = []string{"mTLS", "OAuth"}
	})
	r := authOf(p).Reason

	idx := func(s string) int {
		i := strings.Index(r, s)
		if i == -1 {
			t.Fatalf("expected %q in reason: %q", s, r)
		}
		return i
	}
	lead := idx("authenticate to Confluent Cloud with mTLS (SSL client certificates) or OAuth (SASL/OAUTHBEARER)")
	setup := idx("Setup: mTLS needs your certificate authority")
	landing := idx("mTLS and OAuth clients map to Confluent Cloud identity pools.")
	kerberosNote := idx("Confluent Cloud doesn't support Kerberos. At cutover, move your Kerberos clients to mTLS or OAuth.")
	acls := idx("Your Kafka ACLs don't carry over with your data.")
	identityPoolNote := idx("Your clients will sign in with mTLS and OAuth, so grant these permissions to the matching identity pools rather than to the generated service accounts.")

	if lead >= setup || setup >= landing || landing >= kerberosNote || kerberosNote >= acls || acls >= identityPoolNote {
		t.Errorf("sentence order wrong, want lead < setup < landing < kerberos note < access-control notes < identity-pool note, got reason: %q", r)
	}
}

// TestAuth_UnrecognisedMethodNeverNoChange checks the generic fallthrough: an
// unrecognised source-auth method must never assert the cluster link works
// as-is ("No change."), which would wrongly promise compatibility.
func TestAuth_UnrecognisedMethodNeverNoChange(t *testing.T) {
	unknown := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{"Something else entirely"} })
	n := credNotes(unknown)
	if strings.Contains(n, "No change.") {
		t.Errorf("unrecognised method must not say 'No change.': %q", n)
	}
	if !strings.Contains(n, "Check with your Confluent team whether Cluster Linking can use Something else entirely.") {
		t.Errorf("unrecognised method note = %q", n)
	}
}

// TestAuth_AccessControlNotes covers C2: a cluster link cannot sync Kafka ACLs,
// AWS IAM policies, or Confluent Platform RBAC role bindings from a
// non-Confluent-Cloud source, so authDecision's reason must say so — reaching
// every plan, including start-fresh.
func TestAuth_AccessControlNotes(t *testing.T) {
	t.Run("SCRAM on MSK -> Kafka ACLs note only", func(t *testing.T) {
		p := baseProfile(func(p *Profile) { p.SourcePlatform = "Amazon MSK"; p.SourceAuthTypes = []string{"SASL/SCRAM"} })
		r := authOf(p).Reason
		if !strings.Contains(r, "Kafka ACLs") {
			t.Errorf("Kafka ACLs note must be present: %q", r)
		}
		if strings.Contains(r, "AWS IAM policies") {
			t.Errorf("IAM policies note must not fire on a SCRAM-only source: %q", r)
		}
		if strings.Contains(r, "RBAC role bindings") {
			t.Errorf("CP RBAC note must not fire off Confluent Platform: %q", r)
		}
	})
	t.Run("AWS IAM on MSK (Provisioned) -> IAM policies note only, no Kafka ACLs note", func(t *testing.T) {
		p := baseProfile(func(p *Profile) {
			p.SourcePlatform = "Amazon MSK"
			p.SourceAuthTypes = []string{authAWSIAM}
			p.MSKClusterType = MSKProvisioned
		})
		r := authOf(p).Reason
		if !strings.Contains(r, "AWS IAM policies") {
			t.Errorf("IAM policies note must be present: %q", r)
		}
		if strings.Contains(r, "Kafka ACLs") {
			t.Errorf("Kafka ACLs note must not fire on an IAM-only source: %q", r)
		}
	})
	t.Run("AWS IAM + SCRAM on MSK -> both Kafka ACLs note and IAM policies note", func(t *testing.T) {
		p := baseProfile(func(p *Profile) {
			p.SourcePlatform = "Amazon MSK"
			p.SourceAuthTypes = []string{authAWSIAM, "SASL/SCRAM"}
			p.MSKClusterType = MSKProvisioned
		})
		r := authOf(p).Reason
		if !strings.Contains(r, "Kafka ACLs") || !strings.Contains(r, "AWS IAM policies") {
			t.Errorf("both Kafka ACLs note and IAM policies note must be present: %q", r)
		}
	})
	t.Run("MSK Serverless -> IAM policies note only, Kafka ACLs note still guarded even though it can never fire here", func(t *testing.T) {
		p := baseProfile(func(p *Profile) {
			p.SourcePlatform = "Amazon MSK"
			p.MSKClusterType = MSKServerless
			p.SourceAuthTypes = []string{authAWSIAM}
		})
		r := authOf(p).Reason
		if !strings.Contains(r, "AWS IAM policies") {
			t.Errorf("IAM policies note must be present: %q", r)
		}
		if strings.Contains(r, "Kafka ACLs") {
			t.Errorf("Kafka ACLs note must not fire on Serverless: %q", r)
		}
	})
	t.Run("Confluent Platform SCRAM -> Kafka ACLs note and CP RBAC note, no IAM policies note", func(t *testing.T) {
		p := baseProfile(func(p *Profile) { p.SourcePlatform = "Confluent Platform"; p.SourceAuthTypes = []string{"SASL/SCRAM"} })
		r := authOf(p).Reason
		if !strings.Contains(r, "Kafka ACLs") {
			t.Errorf("Kafka ACLs note must be present: %q", r)
		}
		if !strings.Contains(r, "RBAC role bindings") {
			t.Errorf("CP RBAC note must be present: %q", r)
		}
		if strings.Contains(r, "AWS IAM policies") {
			t.Errorf("IAM policies note must not fire off MSK: %q", r)
		}
	})
	t.Run("plaintext-only Apache Kafka -> neither Kafka ACLs note nor IAM policies note", func(t *testing.T) {
		p := baseProfile(func(p *Profile) { p.SourcePlatform = "Apache Kafka"; p.SourceAuthTypes = []string{authUnauth} })
		r := authOf(p).Reason
		if strings.Contains(r, "Kafka ACLs") {
			t.Errorf("Kafka ACLs note must not fire on a plaintext-only source: %q", r)
		}
		if strings.Contains(r, "AWS IAM policies") {
			t.Errorf("IAM policies note must not fire on a plaintext-only source: %q", r)
		}
		if strings.Contains(r, "RBAC role bindings") {
			t.Errorf("CP RBAC note must not fire off Confluent Platform: %q", r)
		}
	})
	t.Run("explicit mTLS target pick (non-preserved) -> identity-pool note names mTLS", func(t *testing.T) {
		p := baseProfile(func(p *Profile) {
			p.SourceAuthTypes = []string{"SASL/SCRAM"}
			p.TargetIdentityModel = []string{"mTLS"}
		})
		r := authOf(p).Reason
		if !strings.Contains(r, "Your clients will sign in with mTLS, so grant") {
			t.Errorf("identity-pool note must name mTLS: %q", r)
		}
	})
	t.Run("start-fresh plan still carries the access-control note", func(t *testing.T) {
		p := baseProfile(func(p *Profile) {
			p.SourcePlatform = "Amazon MSK"
			p.SourceAuthTypes = []string{"SASL/SCRAM"}
			p.NeedsDataMigration = "No"
		})
		plan := ComputePlan(p)
		if !plan.Switchover.StartFresh {
			t.Fatalf("precondition: this must be a start-fresh plan")
		}
		if !strings.Contains(plan.Auth.Reason, "Kafka ACLs") {
			t.Errorf("infra auth still carries Kafka ACLs note on a start-fresh plan: %q", plan.Auth.Reason)
		}
	})
}

// TestAuth_KerberosAccessControl covers C6/C2 together: a Kerberos source
// counts as an ACL-bearing method for Kafka ACLs note, and the Kerberos client-side note
// comes before the access-control notes.
func TestAuth_KerberosAccessControl(t *testing.T) {
	p := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authKerberos} })
	r := authOf(p).Reason
	if !strings.Contains(r, "Kafka ACLs") {
		t.Errorf("Kafka ACLs note must fire for a Kerberos-only source: %q", r)
	}
	clientNote := "Confluent Cloud doesn't support Kerberos. At cutover, move your Kerberos clients to API keys."
	ci, ki := strings.Index(r, clientNote), strings.Index(r, "Kafka ACLs")
	if ci == -1 {
		t.Fatalf("client note must be present: %q", r)
	}
	if ci >= ki {
		t.Errorf("client note must come before the access-control notes: %q", r)
	}
}

func TestAuth_MultiSelectTarget(t *testing.T) {
	multi := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{"SASL/SCRAM"}
		p.TargetIdentityModel = []string{"API keys (SASL/PLAIN)", "OAuth"}
	})
	v := authOf(multi).Value
	if !strings.Contains(v, "API keys (SASL/PLAIN)") || !strings.Contains(v, "OAuth") {
		t.Errorf("multi-select value = %q, want both methods", v)
	}
}

// The mTLS link note must be truthful: Cluster Linking supports mTLS, but the
// link kcp generates signs in over SASL/SCRAM.
func TestAuth_MTLSLinkNote(t *testing.T) {
	mtls := baseProfile(func(p *Profile) {
		p.SourceType = SourceApacheKafka
		p.SourceAuthTypes = []string{authMTLS}
	})
	n := credNotes(mtls)
	if !strings.Contains(n, "Cluster Linking itself can sign in with mTLS, but the migration link kcp generates signs in over SASL/SCRAM") {
		t.Errorf("mTLS cluster-link note = %q", n)
	}
}

// TestAuth_IdentityPoolNoteIsContextual checks the identity-pool grant note reads
// differently depending on whether API keys are also picked and whether an
// access-control note precedes it.
func TestAuth_IdentityPoolNoteIsContextual(t *testing.T) {
	reasonFor := func(src string, picks ...string) string {
		return authOf(baseProfile(func(p *Profile) {
			p.SourceAuthTypes = []string{src}
			p.TargetIdentityModel = picks
		})).Reason
	}
	// API keys also picked: split the landing by method.
	if r := reasonFor("SASL/SCRAM", "API keys (SASL/PLAIN)", "mTLS"); !strings.Contains(r, "Clients that sign in with mTLS get their permissions through the matching identity pools; clients that use API keys keep theirs on their service accounts.") ||
		strings.Contains(r, "rather than to the generated service accounts") {
		t.Errorf("API keys + mTLS note = %q", r)
	}
	// An access-control note precedes it: contrast with the service accounts.
	if r := reasonFor("SASL/SCRAM", "mTLS", "OAuth"); !strings.Contains(r, "Your clients will sign in with mTLS and OAuth, so grant these permissions to the matching identity pools rather than to the generated service accounts.") {
		t.Errorf("mTLS + OAuth with ACL note = %q", r)
	}
	// No access-control note: plain grant sentence.
	r := reasonFor("None / plaintext", "OAuth")
	if !strings.Contains(r, "Your clients will sign in with OAuth, so grant their permissions to the matching identity pools.") ||
		strings.Contains(r, "rather than to the generated service accounts") {
		t.Errorf("OAuth without ACL note = %q", r)
	}
}

// TestAuth_KerberosWithSCRAMReusesListener checks a Kerberos source that also runs
// SASL/SCRAM signs the link in over the existing SCRAM listener.
func TestAuth_KerberosWithSCRAMReusesListener(t *testing.T) {
	p := baseProfile(func(p *Profile) {
		p.SourceType = SourceApacheKafka
		p.SourceAuthTypes = []string{authKerberos, authSCRAM}
	})
	n := credNotes(p)
	if !strings.Contains(n, "Kerberos can't be used by a cluster link from Confluent Cloud, so the link signs in with your existing SASL/SCRAM listener instead. Your clients can keep Kerberos until cutover.") ||
		strings.Contains(n, "Add a SASL/SCRAM listener") {
		t.Errorf("Kerberos+SCRAM note = %q", n)
	}
	// The migration-infra reuses the SCRAM path rather than adding a listener.
	mi := MigrationInfraDecision(p, TierEnterprise)
	if strings.Contains(mi.Rationale, "add a SASL/SCRAM listener") {
		t.Errorf("Kerberos+SCRAM infra rationale = %q", mi.Rationale)
	}
}

func TestJoinOr_OxfordComma(t *testing.T) {
	for in, want := range map[string]string{
		"":        "",
		"A":       "A",
		"A|B":     "A or B",
		"A|B|C":   "A, B, or C",
		"A|B|C|D": "A, B, C, or D",
	} {
		var xs []string
		if in != "" {
			xs = strings.Split(in, "|")
		}
		if got := joinOr(xs); got != want {
			t.Errorf("joinOr(%v) = %q, want %q", xs, got, want)
		}
	}
}

// credNotesByMethod returns each method's cluster-link credential note.
func credNotesByMethod(p Profile) map[string]string {
	out := map[string]string{}
	for _, h := range sourceCredentialHandling(p, nil, TierEnterprise) {
		out[h.Method] = h.Note
	}
	return out
}

// TestAuth_CredNotesAgreeWithMigrationInfra pins that the per-method notes follow the
// migration-infra decision for every multi-method combo: a jump-cluster link never asks
// for a listener on the source, a SCRAM source never asks for one at all, and a listener
// is asked for at most once per plan.
func TestAuth_CredNotesAgreeWithMigrationInfra(t *testing.T) {
	const ask = "add a sasl/scram listener"
	cases := []struct {
		name     string
		auths    []string
		wantType int
		wantAsks int // total "add a SASL/SCRAM listener" asks across all notes
	}{
		{"IAM+plaintext", []string{authAWSIAM, authUnauth}, miPrivateJumpIAM, 1}, // the IAM note's alternative
		{"IAM+mTLS", []string{authAWSIAM, authMTLS}, miPrivateJumpIAM, 1},
		{"IAM+PLAIN", []string{authAWSIAM, authSASLPlain}, miPrivateJumpIAM, 1},
		{"IAM+Kerberos", []string{authAWSIAM, authKerberos}, miPrivateJumpIAM, 1},
		{"IAM+SCRAM", []string{authAWSIAM, authSCRAM}, miPrivateOutSCRAM, 0},
		{"plaintext+PLAIN", []string{authUnauth, authSASLPlain}, miPrivateOutSCRAM, 1},
		{"Kerberos+plaintext", []string{authKerberos, authUnauth}, miPrivateOutSCRAM, 1},
		{"mTLS+PLAIN", []string{authMTLS, authSASLPlain}, miPrivateOutSCRAM, 1},
		{"mTLS+PLAIN+plaintext+Kerberos", []string{authMTLS, authSASLPlain, authUnauth, authKerberos}, miPrivateOutSCRAM, 1},
		{"SCRAM+plaintext", []string{authSCRAM, authUnauth}, miPrivateOutSCRAM, 0},
		{"SCRAM+mTLS", []string{authSCRAM, authMTLS}, miPrivateOutSCRAM, 0},
		{"SCRAM+PLAIN", []string{authSCRAM, authSASLPlain}, miPrivateOutSCRAM, 0},
		{"SCRAM+Kerberos", []string{authSCRAM, authKerberos}, miPrivateOutSCRAM, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := baseProfile(func(p *Profile) { p.SourceAuthTypes = tc.auths })
			if got := MigrationInfraDecision(p, TierEnterprise).Type; got != tc.wantType {
				t.Fatalf("migration-infra type = %d, want %d", got, tc.wantType)
			}
			jump := IsJumpClusterType(tc.wantType)
			asks := 0
			for m, note := range credNotesByMethod(p) {
				asks += strings.Count(strings.ToLower(note), ask)
				if jump && m != authAWSIAM && strings.Contains(strings.ToLower(note), ask) {
					t.Errorf("%s note asks for a listener on a jump-cluster plan: %q", m, note)
				}
				if jump && m != authAWSIAM && !strings.Contains(note, "jump cluster") {
					t.Errorf("%s note should point at the jump cluster: %q", m, note)
				}
				if authHas(p, authSCRAM) && m != authSCRAM && !strings.Contains(note, "SASL/SCRAM") {
					t.Errorf("%s note should use the existing SCRAM path: %q", m, note)
				}
			}
			if asks != tc.wantAsks {
				t.Errorf("%q asked %d times across notes, want %d: %v", ask, asks, tc.wantAsks, credNotesByMethod(p))
			}
		})
	}
}

func TestAuth_CredNotesSASLPlainFraming(t *testing.T) {
	p := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authSASLPlain} })
	n := credNotesByMethod(p)[authSASLPlain]
	for _, want := range []string{
		"Cluster Linking can sign in with SASL/PLAIN, but the link kcp generates signs in over SASL/SCRAM only. Create a SASL/PLAIN link by hand, or add SCRAM to use kcp's generated link. Your application clients keep their SASL/PLAIN credentials unchanged.",
		"add a SASL/SCRAM listener and create a SCRAM user for the link",
		"you can remove the listener after cutover",
	} {
		if !strings.Contains(n, want) {
			t.Errorf("SASL/PLAIN note missing %q: %q", want, n)
		}
	}

	// A source that also has SCRAM links over that listener; the clients sentence leads.
	both := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authSASLPlain, authSCRAM} })
	if got := credNotesByMethod(both)[authSASLPlain]; got != "Your application clients keep their SASL/PLAIN credentials unchanged. The link signs in over your source's SASL/SCRAM listener." {
		t.Errorf("SASL/PLAIN+SCRAM note = %q", got)
	}
}

func TestAuth_PreservedMTLSOtherMethodsMoveToAPIKeys(t *testing.T) {
	mixed := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{authMTLS, authSCRAM, authSASLPlain}
	})
	if r := authOf(mixed).Reason; !strings.Contains(r, "Clients that use SASL/SCRAM and SASL/PLAIN move to API keys (SASL/PLAIN) at cutover.") {
		t.Errorf("mixed preserved-mTLS reason = %q", r)
	}
	three := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{authMTLS, authSCRAM, authSASLPlain, authUnauth}
	})
	if r := authOf(three).Reason; !strings.Contains(r, "Clients that use SASL/SCRAM, SASL/PLAIN, and plaintext move to API keys (SASL/PLAIN) at cutover.") {
		t.Errorf("three-method preserved-mTLS reason = %q", r)
	}
	// mTLS alone, or an explicit target pick, adds no such sentence.
	only := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authMTLS} })
	picked := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{authMTLS, authSCRAM}
		p.TargetIdentityModel = []string{"mTLS"}
	})
	for _, p := range []Profile{only, picked} {
		if r := authOf(p).Reason; strings.Contains(r, "move to API keys") {
			t.Errorf("unexpected API-keys move sentence: %q", r)
		}
	}
}

func TestSwitchover_KCPCaveatOnlyWhenLinkCannotUseSource(t *testing.T) {
	cases := []struct {
		name       string
		auths      []string
		wantCaveat bool
	}{
		{"mTLS only", []string{authMTLS}, true},
		{"SASL/PLAIN only", []string{authSASLPlain}, true},
		{"Kerberos only", []string{authKerberos}, true},
		{"mTLS+SCRAM", []string{authMTLS, authSCRAM}, false},
		{"SCRAM only", []string{authSCRAM}, false},
		{"IAM only", []string{authAWSIAM}, false},
		{"plaintext only", []string{authUnauth}, false},
		{"unknown", nil, false},
		{"IAM+mTLS (jump cluster)", []string{authAWSIAM, authMTLS}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := baseProfile(func(p *Profile) { p.SourceAuthTypes = tc.auths })
			r := kcpResource(p, TierEnterprise)
			if r == nil {
				t.Fatal("kcpResource = nil")
			}
			if got := r.Caveat != ""; got != tc.wantCaveat {
				t.Errorf("caveat = %q, want caveat=%v", r.Caveat, tc.wantCaveat)
			}
		})
	}
}

func TestSwitchover_GatewayEscalationAttributesActualDriver(t *testing.T) {
	hard := swOf(func(p *Profile) {
		p.DowntimeTolerance = "Minutes per service"
		p.ClientCoordinationBurden = "Hard (many clients and teams)"
	})
	if !strings.Contains(hard.Reason, "Based on your answer (client coordination)") || strings.Contains(hard.Reason, "minutes per service") {
		t.Errorf("hard-coordination escalation reason = %q", hard.Reason)
	}
	blast := swOf(func(p *Profile) {
		p.DowntimeTolerance = "Minutes per service"
		p.PartitionBand = "30,000–96,000"
	})
	if !strings.Contains(blast.Reason, "partition count") || strings.Contains(blast.Reason, "minutes per service") {
		t.Errorf("blast-radius escalation reason = %q", blast.Reason)
	}
}

func TestSizing_ServerlessAttributedAsAssumed(t *testing.T) {
	p := Profile{MSKClusterType: MSKServerless}
	r := buildSizingVerdict(p, sizingBand(p), TierEnterprise).Reason
	if !strings.Contains(r, "Based on our assumption (MSK Serverless partition limit)") || strings.Contains(r, "your answer") {
		t.Errorf("serverless sizing reason = %q", r)
	}
}

func TestCopy_StyleAndWording(t *testing.T) {
	if r := historicalDataDecision(Profile{StorageMode: strptr("Yes"), ConsumerHistoryRequirement: "Required"}).Reason; !strings.Contains(r, "keeps flowing until cutover") {
		t.Errorf("historical reason = %q", r)
	}
	for _, auths := range [][]string{{authKerberos}, {authKerberos, authSCRAM}} {
		p := baseProfile(func(p *Profile) { p.SourceAuthTypes = auths })
		if n := credNotes(p); strings.Contains(n, "Cluster Link ") {
			t.Errorf("capitalised noun in %q", n)
		}
	}
	const custom = "Workloads at this scale benefit from custom sizing, so we'd plan the right cluster with you rather than size it automatically."
	found := false
	for _, f := range ruleEncouragements(t) {
		found = found || f == custom
		if strings.Contains(f, "scale well past") {
			t.Errorf("encouragement not aligned: %q", f)
		}
	}
	if !found {
		t.Errorf("no rule carries %q", custom)
	}
}

// ruleEncouragements collects the Dedicated-withheld trigger copy from the cluster-type rules.
func ruleEncouragements(t *testing.T) []string {
	t.Helper()
	p := baseProfile(nil)
	rules, _ := clusterTypeRules(p, sizingBand(p), targetCloud(p))
	var out []string
	for _, r := range rules {
		out = append(out, r.Encouragement)
	}
	return out
}

// A public IAM-only source can't use kcp's jump cluster (private sources only), so the
// type and the IAM note both say: direct SASL/SCRAM link, with a listener added.
func TestAuth_IAMPublicVsPrivateNotesAgreeWithType(t *testing.T) {
	pub := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{authAWSIAM}
		p.SourcePublicAccess = "Yes"
	})
	ch := MigrationInfraDecision(pub, TierEnterprise)
	if ch.Type != miPublicSCRAM || ch.JumpCluster || !strings.Contains(ch.Rationale, "add a SASL/SCRAM listener") {
		t.Errorf("public IAM choice = %+v", ch)
	}
	n := credNotesByMethod(pub)[authAWSIAM]
	if !strings.Contains(n, "your MSK brokers are public") || !strings.Contains(n, "Add a SASL/SCRAM listener") || strings.Contains(n, "We recommend a jump cluster") {
		t.Errorf("public IAM note = %q", n)
	}
	// With mTLS alongside, the listener is asked for once.
	pubMTLS := baseProfile(func(p *Profile) {
		p.SourceAuthTypes = []string{authMTLS, authAWSIAM}
		p.SourcePublicAccess = "Yes"
	})
	asks := 0
	for _, note := range credNotesByMethod(pubMTLS) {
		asks += strings.Count(strings.ToLower(note), "add a sasl/scram listener")
	}
	if asks != 1 {
		t.Errorf("public IAM+mTLS asks = %d, want 1: %v", asks, credNotesByMethod(pubMTLS))
	}

	priv := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authAWSIAM} })
	if ch := MigrationInfraDecision(priv, TierEnterprise); ch.Type != miPrivateJumpIAM {
		t.Errorf("private IAM type = %d, want %d", ch.Type, miPrivateJumpIAM)
	}
	if n := credNotesByMethod(priv)[authAWSIAM]; !strings.Contains(n, "which is why this plan runs a jump cluster.") {
		t.Errorf("private IAM note = %q", n)
	}
}

// Specialist-led (type 0) outcomes don't prescribe a listener or a link sign-in method.
func TestAuth_SpecialistOutcomeNotesDeferToSpecialist(t *testing.T) {
	for _, auths := range [][]string{{authMTLS}, {authSASLPlain}, {authUnauth}, {authKerberos}, {authAWSIAM}, {authMTLS, authSCRAM}} {
		p := baseProfile(func(p *Profile) { p.SourceAuthTypes = auths; p.TargetIsGovCloud = "Yes" })
		if MigrationInfraDecision(p, TierEnterprise).Type != 0 {
			t.Fatalf("%v: expected type 0", auths)
		}
		for m, note := range credNotesByMethod(p) {
			if m == authSCRAM {
				continue
			}
			l := strings.ToLower(note)
			if strings.Contains(l, "add a sasl/scram listener") || strings.Contains(l, "jump cluster this plan runs") {
				t.Errorf("%v: %s note prescribes a listener: %q", auths, m, note)
			}
			if !strings.Contains(note, "designed with you as part of the specialist-led setup") {
				t.Errorf("%v: %s note missing specialist handoff: %q", auths, m, note)
			}
		}
	}
}

// When the specialist owns only the link's networking (private source to an Azure or
// Google Cloud target), a plaintext or Kerberos-only source still needs a SASL/SCRAM
// listener, because a cluster link can't use either method.
func TestAuth_NetworkOnlySpecialistStillAsksForListener(t *testing.T) {
	for _, cloud := range []string{"Azure", "GCP"} {
		for _, a := range []string{authUnauth, authKerberos} {
			p := baseProfile(func(p *Profile) { p.TargetCloud = cloud; p.SourceAuthTypes = []string{a} })
			if ch := MigrationInfraDecision(p, TierEnterprise); ch.Type != 0 || !ch.SpecialistClusterLinkable {
				t.Fatalf("%s %s: want network-only specialist, got %+v", cloud, a, ch)
			}
			n := credNotes(p)
			if strings.Contains(n, "specialist-led setup") || !strings.Contains(n, "SASL/SCRAM listener") || !strings.Contains(n, linkACLSentence) {
				t.Errorf("%s %s note = %q", cloud, a, n)
			}
		}
	}
	// mTLS and SASL/PLAIN keep the specialist-designed sign-in.
	p := baseProfile(func(p *Profile) { p.TargetCloud = "Azure"; p.SourceAuthTypes = []string{authMTLS} })
	if n := credNotes(p); !strings.Contains(n, "specialist-led setup") {
		t.Errorf("mTLS note = %q", n)
	}
}

// A non-AWS target can't use the AWS-built jump cluster, so the IAM note hands off to a
// specialist instead of prescribing it.
func TestAuth_IAMNonAWSTargetNoJumpCluster(t *testing.T) {
	note := func(cloud string) string {
		return credNotes(baseProfile(func(p *Profile) {
			p.TargetCloud = cloud
			p.SourceAuthTypes = []string{authAWSIAM}
			p.MSKClusterType = MSKProvisioned
		}))
	}
	if n := note("AWS"); !strings.Contains(n, "PrivateLink VPC endpoint in your AWS account") || !strings.Contains(n, "stays on PNI") {
		t.Errorf("AWS note = %q", n)
	}
	for _, cloud := range []string{"Azure", "GCP"} {
		n := note(cloud)
		if strings.Contains(n, "jump cluster") || !strings.Contains(n, "designed with you as part of the specialist-led setup") {
			t.Errorf("%s note = %q", cloud, n)
		}
	}
}

// Through the Gateway, clients keep their source credentials (the Gateway swaps them), so
// the reason must not tell them to move to Confluent Cloud credentials at or after cutover.
func TestAuth_GatewayKeepsSourceCredentials(t *testing.T) {
	scram := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{"SASL/SCRAM"} })
	got := authDecision(scram, targetCloud(scram), true).Reason
	for _, want := range []string{"keep their current credentials, which the Gateway swaps for Confluent Cloud credentials", "bypassing the Gateway"} {
		if !strings.Contains(got, want) {
			t.Errorf("gateway auth reason missing %q: %q", want, got)
		}
	}
	for _, bad := range []string{"afterwards", "during cutover, then"} {
		if strings.Contains(got, bad) {
			t.Errorf("gateway auth reason should not say %q: %q", bad, got)
		}
	}
}

// A plaintext source has no access control to carry over, and new Confluent Cloud
// identities start with no access, so the reason says to grant it before cutover.
func TestAuth_PlaintextSourceAccessNote(t *testing.T) {
	plain := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{"None / plaintext"} })
	if got := authOf(plain).Reason; !strings.Contains(got, "A plaintext source has no access control to carry over, and new Confluent Cloud service accounts or identity pools have no access by default. Grant them ACLs or role bindings before cutover.") {
		t.Errorf("plaintext auth reason = %q", got)
	}
	scram := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{"SASL/SCRAM"} })
	if got := authOf(scram).Reason; strings.Contains(got, "plaintext source") {
		t.Errorf("non-plaintext source should not get the plaintext note: %q", got)
	}
}

// The SCRAM note names the access the link's user needs on the source.
func TestSourceAuthHandling_SCRAMLinkACLs(t *testing.T) {
	got := sourceAuthHandling("SASL/SCRAM", Profile{}, viaLink, &linkPath{}, false).Note
	want := "Cluster Linking uses your SCRAM credentials as-is. The link's user needs READ and DESCRIBE_CONFIGS on the topics you mirror, and DESCRIBE on the consumer groups."
	if !strings.Contains(got, want) {
		t.Errorf("note = %q, want it to contain %q", got, want)
	}
}

// Kerberos asks for a SCRAM user for the link, like the mTLS, SASL/PLAIN and plaintext notes, and
// a SASL/PLAIN source is told the link kcp generates signs in over SASL/SCRAM.
func TestSourceAuthHandling_ListenerNotesAskForSCRAMUser(t *testing.T) {
	p := Profile{SourcePlatform: "Apache Kafka"}
	kerb := sourceAuthHandling(authKerberos, p, viaLink, &linkPath{}, false).Note
	if !strings.Contains(kerb, "Add a SASL/SCRAM listener on your source cluster that only the link uses, and create a SCRAM user for the link") {
		t.Errorf("Kerberos note = %q", kerb)
	}
	plain := sourceAuthHandling(authSASLPlain, p, viaLink, &linkPath{}, false).Note
	if !strings.Contains(plain, "the link kcp generates signs in over SASL/SCRAM only") || !strings.Contains(plain, "create a SCRAM user for the link") {
		t.Errorf("SASL/PLAIN note = %q", plain)
	}
}

// A specialist that owns only the link's networking is the one who sets up the link, so the
// notes neither point at a migration-link step nor claim kcp generates the link.
func TestAuth_NetworkOnlySpecialistWording(t *testing.T) {
	for _, a := range []string{authUnauth, authKerberos} {
		p := baseProfile(func(p *Profile) { p.TargetCloud = "Azure"; p.SourceAuthTypes = []string{a} })
		n := credNotes(p)
		if !strings.Contains(n, "before the specialist sets up the cluster link") || strings.Contains(n, "migration-link step") || strings.Contains(n, "kcp's generated link") {
			t.Errorf("%s note = %q", a, n)
		}
	}
	for _, other := range []struct{ auth, label string }{{authMTLS, "mTLS listener"}, {"API keys (SASL/PLAIN)", "SASL/PLAIN listener"}} {
		p := baseProfile(func(p *Profile) { p.TargetCloud = "GCP"; p.SourceAuthTypes = []string{authKerberos, other.auth} })
		for m, n := range credNotesByMethod(p) {
			if strings.Contains(n, "specialist-led") || strings.Contains(n, "add a SASL/SCRAM listener") || !strings.Contains(n, "signs in with your existing "+other.label) {
				t.Errorf("%s %s note = %q", other.auth, m, n)
			}
		}
	}
}
