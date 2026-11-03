package engine

import (
	"strings"
	"testing"
)

// A target-only mTLS pick isn't "keeping" mTLS; only an mTLS source is.
func TestDedicatedSrcReason_MTLSOnGCPWording(t *testing.T) {
	c := cause{ID: "mtls_on_gcp_target"}
	src, reason := dedicatedSrcReason(Profile{SourceAuthTypes: []string{authMTLS}}, c)
	if !strings.Contains(src, "your source uses mTLS") || !strings.HasPrefix(reason, "keeping mTLS") {
		t.Errorf("mTLS source: got %q / %q", src, reason)
	}
	src, reason = dedicatedSrcReason(Profile{SourceAuthTypes: []string{"SASL/SCRAM"}}, c)
	if !strings.Contains(src, "you chose mTLS for your clients") || strings.Contains(reason, "keeping") {
		t.Errorf("target-only mTLS pick: got %q / %q", src, reason)
	}
}
