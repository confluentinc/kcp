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

// TestRenderReportSkippedPrecondition proves a Skipped precondition (a live
// check that could not be run, e.g. a permission denial) renders as a
// yellow ⚠ — never the green ✓ a real pass gets, since that would claim a
// verification that never happened — and, being advisory (OK: true), does
// not stop the run at the route-checks gate the way a real failure does.
func TestRenderReportSkippedPrecondition(t *testing.T) {
	color.NoColor = true
	r := reconcile.Report{
		Preconditions: []reconcile.PreconditionResult{
			{Name: "route is static", OK: true},
			{Name: "staged auth secrets exist", OK: true, Skipped: true, Detail: "no permission to read secrets"},
		},
		Migratable: []reconcile.TopicVerdict{{Topic: "team-a.orders"}},
	}
	var buf bytes.Buffer
	RenderReport(&buf, r, RenderView{Route: "migration-route", TargetDomain: "cc"})
	out := buf.String()

	if !strings.Contains(out, "⚠ staged auth secrets exist — no permission to read secrets") {
		t.Errorf("skipped precondition must render as ⚠ with its detail; got:\n%s", out)
	}
	if strings.Contains(out, "✓ staged auth secrets exist") {
		t.Errorf("a skipped precondition must never render as a plain ✓ pass; got:\n%s", out)
	}
	if strings.Contains(out, "Refused at route checks") {
		t.Errorf("a skip is advisory and must not refuse at the route-checks gate; got:\n%s", out)
	}
	if !strings.Contains(out, "Topics ·") {
		t.Errorf("topics must still be evaluated past a skip (only a real failure stops the run); got:\n%s", out)
	}
}

// TestRenderReport_ResumeBuckets proves the resume verdicts (SwitchOnly,
// AwaitStopped) render with their own glyph/status word and are counted in
// the footer.
func TestRenderReport_ResumeBuckets(t *testing.T) {
	color.NoColor = true
	r := reconcile.Report{
		SwitchOnly:   []reconcile.TopicVerdict{{Topic: "t1", Verdict: reconcile.SwitchOnly}},
		AwaitStopped: []reconcile.TopicVerdict{{Topic: "t2", Verdict: reconcile.AwaitStopped}},
	}
	var b strings.Builder
	RenderReport(&b, r, RenderView{})
	out := b.String()
	for _, want := range []string{"t1", "t2", "switch", "awaiting", "1 to switch", "1 awaiting promotion"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// TestRenderReport_ResumeOnlyIsActionable proves a plan with ONLY resume work
// (SwitchOnly/AwaitStopped topics, zero Migratable) is actionable, not "nothing
// to do" — there IS a switch/await to perform, so it must reach the same
// artifact-note footer branch a Migratable-only plan does.
func TestRenderReport_ResumeOnlyIsActionable(t *testing.T) {
	color.NoColor = true
	r := reconcile.Report{
		SwitchOnly:   []reconcile.TopicVerdict{{Topic: "t1", Verdict: reconcile.SwitchOnly}},
		AwaitStopped: []reconcile.TopicVerdict{{Topic: "t2", Verdict: reconcile.AwaitStopped}},
	}
	var buf bytes.Buffer
	RenderReport(&buf, r, RenderView{ArtifactNote: "artifacts → ./out"})
	out := buf.String()

	if strings.Contains(out, "nothing to do") {
		t.Errorf("a resume-only plan (SwitchOnly/AwaitStopped) has actionable work and must not render \"nothing to do\"; got:\n%s", out)
	}
	if !strings.Contains(out, "(artifacts → ./out)") {
		t.Errorf("a resume-only plan must reach the actionable artifact-note footer branch; got:\n%s", out)
	}
}

// TestRenderReport_SwitchOnlyWarningNotDoublePrinted proves a shadow warning
// attached to a SwitchOnly topic (mirroring how reconcile.go's
// shadowWarnings(toMigrate, ...) attaches warnings to SwitchOnly/AwaitStopped
// topics too, since toMigrate includes them) renders exactly once — on the
// topic's own line — and is not also re-printed as a trailing "unattached"
// warning.
func TestRenderReport_SwitchOnlyWarningNotDoublePrinted(t *testing.T) {
	color.NoColor = true
	warning := `existing condition for "t1" (-> msk) is now shadowed`
	r := reconcile.Report{
		SwitchOnly: []reconcile.TopicVerdict{{Topic: "t1", Verdict: reconcile.SwitchOnly}},
		Warnings:   []string{warning},
	}
	var buf bytes.Buffer
	RenderReport(&buf, r, RenderView{})
	out := buf.String()

	if !strings.Contains(out, warning) {
		t.Fatalf("output missing warning %q:\n%s", warning, out)
	}
	if n := strings.Count(out, warning); n != 1 {
		t.Errorf("warning %q must appear exactly once (attached to its SwitchOnly topic line), got %d occurrences:\n%s", warning, n, out)
	}
}

// TestRenderReport_AwaitStoppedWarningNotDoublePrinted mirrors
// TestRenderReport_SwitchOnlyWarningNotDoublePrinted for the AwaitStopped
// bucket: a shadow warning naming an AwaitStopped topic must render exactly
// once, not once on the topic line and again as a trailing "unattached" note.
func TestRenderReport_AwaitStoppedWarningNotDoublePrinted(t *testing.T) {
	color.NoColor = true
	warning := `existing condition for "t2" (-> msk) is now shadowed`
	r := reconcile.Report{
		AwaitStopped: []reconcile.TopicVerdict{{Topic: "t2", Verdict: reconcile.AwaitStopped}},
		Warnings:     []string{warning},
	}
	var buf bytes.Buffer
	RenderReport(&buf, r, RenderView{})
	out := buf.String()

	if !strings.Contains(out, warning) {
		t.Fatalf("output missing warning %q:\n%s", warning, out)
	}
	if n := strings.Count(out, warning); n != 1 {
		t.Errorf("warning %q must appear exactly once (attached to its AwaitStopped topic line), got %d occurrences:\n%s", warning, n, out)
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
