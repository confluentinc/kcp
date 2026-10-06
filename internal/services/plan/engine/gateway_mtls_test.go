package engine

import (
	"strings"
	"testing"
)

// Through the Gateway, mTLS clients swap to an API key: the plan must not call mTLS
// preserved, nor ask for a CA upload or identity-pool ACLs.
func TestAuthDecision_GatewayMTLSSwapsToAPIKeys(t *testing.T) {
	p := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authMTLS} })
	r := authDecision(p, "AWS", true)
	if r.Value != "API keys (SASL/PLAIN)" {
		t.Errorf("Value = %q, want API keys (SASL/PLAIN)", r.Value)
	}
	const g1 = "Through the Gateway, Confluent Cloud sees the API key the Gateway maps each client to, not the client's certificate, so map each client to its own API key and grant ACLs to that key's service account. Upload your CA and set up identity pools only if clients will later connect to Confluent Cloud directly."
	if !strings.Contains(r.Reason, g1) {
		t.Errorf("Reason lacks the Gateway mTLS sentence: %q", r.Reason)
	}
	for _, bad := range []string{"preserved", "we keep it after cutover", "matching identity pools", "certificate authority uploaded"} {
		if strings.Contains(r.Reason+r.Why+r.Value, bad) {
			t.Errorf("Gateway mTLS Auth must not say %q: %+v", bad, r)
		}
	}
	// Without the Gateway the same source still preserves mTLS.
	if got := authDecision(p, "AWS", false).Value; got != "mTLS (preserved)" {
		t.Errorf("non-Gateway Value = %q, want mTLS (preserved)", got)
	}
}
