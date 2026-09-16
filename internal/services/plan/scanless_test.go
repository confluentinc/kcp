package plan

import (
	"strings"
	"testing"
	"time"
)

// A scanless run plans one synthetic cluster with nothing derived: every verdict
// that needs an answer is Pending, the scan-fact questions surface as required,
// and the copy never claims a scan happened.
func TestScanless_QuestionnairePlan(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	ep := BuildEnginePlan(ScanlessState(), DeclaredInputs{}, "", fixed)

	if len(ep.Clusters) != 1 {
		t.Fatalf("scanless plan: got %d clusters, want 1", len(ep.Clusters))
	}
	cp := ep.Clusters[0]
	if cp.ClusterID != ScanlessClusterName {
		t.Errorf("cluster name = %q, want %q", cp.ClusterID, ScanlessClusterName)
	}

	// With no scan, the connector / auth / topic facts have nothing to derive from,
	// so they must show up as open required questions for the customer to answer.
	req, _ := openCounts(cp.Questions)
	if req == 0 {
		t.Errorf("scanless plan should surface required questions, got 0")
	}
	for _, key := range []string{"source_auth", "msk_connect_present", "self_managed_connectors", "topics_have_custom_settings"} {
		if !hasOpenRequired(cp.Questions, key) {
			t.Errorf("expected %q to be an open required question in a scanless plan", key)
		}
	}

	// Verdicts that hinge on unanswered questions are held.
	if got := verdictValues(cp.Plan)[nodeConnectors]; got == "" {
		t.Errorf("connectors verdict missing")
	}
	if _, held := cp.Contingent[nodeConnectors]; !held {
		t.Errorf("Connectors should be contingent (held) with no scan and no answers")
	}
}

// The rendered markdown for a scanless plan must not claim a scan happened.
func TestScanless_MarkdownCopy(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	md := RenderEnginePlanMarkdown(BuildEnginePlan(ScanlessState(), DeclaredInputs{}, "", fixed))

	if !strings.Contains(md, "No scan, answers only") {
		t.Errorf("scanless markdown should carry the no-scan source line:\n%s", md)
	}
	for _, banned := range []string{"topics · ", "brokers · auth", "settled by your scan"} {
		if strings.Contains(md, banned) {
			t.Errorf("scanless markdown should not contain scan-only copy %q", banned)
		}
	}
	// The scanless top note frames the missing scan positively (an upgrade for sharper
	// sizing), not as a broken command.
	if !strings.Contains(md, "generated from your answers alone") {
		t.Errorf("scanless markdown should carry the positive no-scan top note:\n%s", md)
	}

	// A scanned plan, by contrast, keeps the MSK source line and drops the no-scan note.
	scanned := RenderEnginePlanMarkdown(BuildEnginePlan(twoClusterState(), DeclaredInputs{}, "s.json", fixed))
	if !strings.Contains(scanned, "**Source:** MSK") {
		t.Errorf("scanned markdown should keep the MSK source line")
	}
	if strings.Contains(scanned, "generated from your answers alone") {
		t.Errorf("scanned markdown should not carry the no-scan top note")
	}
}

// hasOpenRequired reports whether a resolved question with the given key is open
// and required.
func hasOpenRequired(qs []ResolvedQuestion, key string) bool {
	for _, q := range qs {
		if q.Key == key {
			return q.Status == "open_required"
		}
	}
	return false
}
