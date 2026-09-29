package engine

import (
	"strings"
	"testing"
)

func authOf(p Profile) AuthResult { return authDecision(p, targetCloud(p)) }

// credNotes renders the source-credential handling notes as "method: note …",
// mirroring how the switchover reason folds them in.
func credNotes(p Profile) string {
	var parts []string
	for _, h := range sourceCredentialHandling(p, nil) {
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
	if n := credNotes(iamProv); !strings.Contains(n, "We recommend a jump cluster") ||
		!strings.Contains(n, "leaves your production MSK cluster and its clients untouched") ||
		!strings.Contains(n, "The alternative is to add a SASL/SCRAM listener") {
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
	const oauthSetup = "Setup: OAuth needs an identity provider and an identity pool configured in your Confluent Cloud organization."
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
	if r := authOf(both).Reason; !strings.Contains(r, "Setup: mTLS needs your certificate authority uploaded to your Confluent Cloud organization and a certificate identity pool that matches your client certificates; OAuth needs an identity provider and an identity pool configured in your Confluent Cloud organization.") ||
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
	if n := credNotes(kerberos); !strings.Contains(n, "Kerberos can't be used by a Cluster Link from Confluent Cloud") ||
		!strings.Contains(n, "Add a SASL/SCRAM listener on your source cluster that only the link uses") ||
		strings.Contains(n, "mTLS") ||
		!strings.Contains(n, "Your clients can keep Kerberos until cutover") {
		t.Errorf("Kerberos cluster-link note = %q", n)
	}

	// Replicator: runs on Kafka Connect in the customer's own environment, so it
	// reads the source with the existing Kerberos keytab — no change needed.
	replicatorNotes := func(p Profile) string {
		var parts []string
		for _, h := range sourceCredentialHandling(p, &SwitchoverResult{Replicator: true}) {
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
	if r := authOf(kerberos).Reason; !strings.Contains(r, "Confluent Cloud doesn't support Kerberos. Before cutover, move these clients to API keys.") {
		t.Errorf("Kerberos client-side reason = %q", r)
	}

	// A multi-select target names each picked method, "A, B or C" — no Oxford comma.
	multiKerberos := baseProfile(func(p *Profile) {
		p.SourceType = SourceApacheKafka
		p.SourceAuthTypes = []string{authKerberos}
		p.TargetIdentityModel = []string{"API keys (SASL/PLAIN)", "OAuth", "mTLS"}
	})
	if r := authOf(multiKerberos).Reason; !strings.Contains(r, "Confluent Cloud doesn't support Kerberos. Before cutover, move these clients to API keys, OAuth or mTLS.") {
		t.Errorf("Kerberos multi-select client-side reason = %q", r)
	}
}

// TestAuth_KerberosSentenceOrder pins the auth-reason sentence order for a
// Kerberos source with an mTLS + OAuth target: lead, setup, credential landing,
// the Kerberos client note, then the access-control notes (including the
// identity-pool grant note) — matching the reference decision engine.
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
	kerberosNote := idx("Confluent Cloud doesn't support Kerberos. Before cutover, move these clients to mTLS or OAuth.")
	acls := idx("Your Kafka ACLs don't carry over with your data.")
	identityPoolNote := idx("Your clients will sign in with mTLS and OAuth, so grant these permissions to the matching identity pools rather than to the generated service accounts.")

	if !(lead < setup && setup < landing && landing < kerberosNote && kerberosNote < acls && acls < identityPoolNote) {
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
	clientNote := "Confluent Cloud doesn't support Kerberos. Before cutover, move these clients to API keys."
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
