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
	iamSrvless := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authAWSIAM}; p.MSKClusterType = MSKServerless })
	if n := credNotes(iamSrvless); !strings.Contains(n, "jump cluster") {
		t.Errorf("IAM Serverless note = %q", n)
	}
	// Unauthenticated source over a cluster link: nothing to prepare on the source
	// (the link reads it over plaintext with no credentials); the clients need a
	// supported method on Confluent Cloud after cutover.
	unauth := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{"None / plaintext"} })
	if n := credNotes(unauth); !strings.Contains(n, "Nothing to prepare on your source") ||
		!strings.Contains(n, "unauthenticated clients will need a supported auth method on Confluent Cloud after cutover") {
		t.Errorf("unauth note = %q", n)
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
