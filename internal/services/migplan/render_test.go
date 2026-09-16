package migplan

import (
	"bytes"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/fatih/color"
)

// TestRenderReportTopicCentric exercises the common path: all route checks pass,
// a mix of blocked/ready/unchanged topics (a blocked topic ⇒ refused). It asserts
// the topic-centric structure, blocked-first ordering, the reason sub-line, the
// per-topic shadow warning, and the Terraform counts footer.
func TestRenderReportTopicCentric(t *testing.T) {
	color.NoColor = true // deterministic, plain output
	r := reconcile.Report{
		Preconditions: []reconcile.PreconditionResult{
			{Name: "route is dynamic", OK: true},
			{Name: "consumer offset sync disabled on link", OK: true},
		},
		FailFast:   []reconcile.TopicVerdict{{Topic: "team-b.audit", Reason: "not on the cluster link"}},
		Migratable: []reconcile.TopicVerdict{{Topic: "team-a.orders"}, {Topic: "billing-v2"}},
		Unchanged:  []reconcile.TopicVerdict{{Topic: "done"}},
		Warnings:   []string{`existing condition for "team-a.orders" (-> msk) is now shadowed`},
	}
	var buf bytes.Buffer
	RenderReport(&buf, r, RenderView{Route: "migration-route", TargetDomain: "cc"})
	out := buf.String()

	for _, want := range []string{
		`Migration plan · route "migration-route" → cc`,
		"Route checks",
		"✓ route is dynamic",
		"Topics · 4 requested",
		"✗ team-b.audit",
		"blocked",
		"↳ not on the cluster link",
		"+ team-a.orders",
		"ready",
		"= done",
		"unchanged",
		`⚠ existing condition for "team-a.orders"`, // shadow warning attached to its topic
		"Plan: 2 to migrate, 1 blocked, 1 unchanged  (refused — no artifacts)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q; got:\n%s", want, out)
		}
	}

	// blocked-first: team-b.audit (blocked) appears before team-a.orders (ready).
	if strings.Index(out, "team-b.audit") > strings.Index(out, "team-a.orders") {
		t.Errorf("blocked topic must render before ready topics; got:\n%s", out)
	}
}

// TestRenderReportGateFailure: a failed route check stops the run before topics.
func TestRenderReportGateFailure(t *testing.T) {
	color.NoColor = true
	r := reconcile.Report{
		Preconditions: []reconcile.PreconditionResult{
			{Name: "route is dynamic", OK: true},
			{Name: "target domain is bound", OK: false, Detail: `"gcp" is not bound`},
		},
	}
	var buf bytes.Buffer
	RenderReport(&buf, r, RenderView{Route: "migration-route", TargetDomain: "gcp"})
	out := buf.String()
	if !strings.Contains(out, "✗ target domain is bound — \"gcp\" is not bound") {
		t.Errorf("missing failed gate line; got:\n%s", out)
	}
	if !strings.Contains(out, "Refused at route checks — 1 failed") {
		t.Errorf("missing gate-refusal footer; got:\n%s", out)
	}
	if strings.Contains(out, "Topics ·") {
		t.Errorf("no topics section should render when a gate fails; got:\n%s", out)
	}
}

// TestRenderReportSuccessAndVerbose: all ready ⇒ success footer with the artifact
// note; --verbose adds the per-topic facts sub-line.
func TestRenderReportSuccessAndVerbose(t *testing.T) {
	color.NoColor = true
	r := reconcile.Report{
		Preconditions: []reconcile.PreconditionResult{{Name: "route is dynamic", OK: true}},
		Migratable: []reconcile.TopicVerdict{
			{Topic: "billing-v2", S: "present", M: "mirroring", T: "present", R: "->source"},
		},
	}
	var buf bytes.Buffer
	RenderReport(&buf, r, RenderView{Route: "r", TargetDomain: "cc", Verbose: true, ArtifactNote: "artifacts → ./out"})
	out := buf.String()
	if !strings.Contains(out, "Plan: 1 to migrate, 0 unchanged  (artifacts → ./out)") {
		t.Errorf("missing success footer; got:\n%s", out)
	}
	if !strings.Contains(out, "↳ source present · mirror mirroring · target present · routes ->source") {
		t.Errorf("--verbose must show the per-topic facts line; got:\n%s", out)
	}
}
