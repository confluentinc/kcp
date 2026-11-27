package reconcile

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/goccy/go-yaml"
)

func withGroups(f ReversibleFacts, committed map[string][]string) ReversibleFacts {
	f.Groups = &ReversibleGroupFacts{Committed: committed, EnteredStates: map[string]string{}, EnteredTypes: map[string]string{}}
	return f
}

func routeOf(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var v struct {
		Route map[string]any `yaml:"route"`
	}
	if err := yaml.Unmarshal(doc, &v); err != nil || v.Route == nil {
		t.Fatalf("not a {route: …} document (%v):\n%s", err, doc)
	}
	return v.Route
}

func requirePlanned(t *testing.T, p *Plan) {
	t.Helper()
	if p.Report.Refused() {
		t.Fatalf("refused: %+v", p.Report)
	}
	if p.Mode != ModeReversibleStatic {
		t.Fatalf("Mode = %q", p.Mode)
	}
	if p.Artifacts == nil || p.Reversal == nil {
		t.Fatalf("want artifacts and a reversal, got %+v / %+v", p.Artifacts, p.Reversal)
	}
}

func TestReconcileReversibleStatic_NormalFlipPlan(t *testing.T) {
	p := ReconcileReversibleStatic(reversibleInput("cc"), withGroups(neverReversedFacts(), nil))
	requirePlanned(t, p)
	r := p.Reversal
	if r.ReverseOn != SideTarget || !reflect.DeepEqual(r.Reverse, []string{"a", "b"}) || r.AwaitStopped != nil || r.SwitchOnly != nil {
		t.Errorf("reversal = %+v", r)
	}
	if r.Sides.Bound != SideSource || r.Sides.Writable != SideTarget || r.Sides.BackOut {
		t.Errorf("sides = %+v", r.Sides)
	}

	sw := routeOf(t, p.Artifacts.SwitchoverRules)
	if !reflect.DeepEqual(sw["streamingDomain"], map[string]any{"name": "cc", "bootstrapServerId": "cc-sasl"}) {
		t.Errorf("switch route streamingDomain = %v", sw["streamingDomain"])
	}
	if _, has := sw["fence"]; has {
		t.Error("the switch route must carry no fence")
	}
	fence, _ := BuildFenceFragment()
	if !bytes.Equal(p.Artifacts.FenceRules, fence) {
		t.Errorf("FenceRules =\n%s", p.Artifacts.FenceRules)
	}
	rb := routeOf(t, p.Artifacts.RollbackFenceRules)
	if !reflect.DeepEqual(rb["streamingDomain"], map[string]any{"name": "cp", "bootstrapServerId": "cp-plain"}) {
		t.Errorf("rollback route streamingDomain = %v", rb["streamingDomain"])
	}
	if _, has := rb["fence"]; has {
		t.Error("the rollback route must carry no fence")
	}
	a := p.Artifacts
	if !reflect.DeepEqual(a.MigrateTopics, []string{"a", "b"}) || a.PromoteTopics != nil || a.FencedAtStart || !a.RollbackAllowed {
		t.Errorf("artifacts = %+v", a)
	}
}

func TestReconcileReversibleStatic_TowardSource(t *testing.T) {
	f := placedFacts(reversibleGateway("cc", false),
		map[string]MirrorRow{"a": active, "b": active},
		map[string]MirrorRow{"a": stopped, "b": stopped},
		"a", "b")
	p := ReconcileReversibleStatic(reversibleInput("cp"), withGroups(f, nil))
	requirePlanned(t, p)
	if p.Reversal.ReverseOn != SideSource || !reflect.DeepEqual(p.Reversal.Reverse, []string{"a", "b"}) {
		t.Errorf("reversal = %+v", p.Reversal)
	}
	if sd := routeOf(t, p.Artifacts.SwitchoverRules)["streamingDomain"]; !reflect.DeepEqual(sd, map[string]any{"name": "cp", "bootstrapServerId": "cp-plain"}) {
		t.Errorf("switch route streamingDomain = %v", sd)
	}
}

// backOutFacts: a flip toward cc stopped part way, fenced on cp, and the
// operator set writableOn back to cp. a was reversed by that run; b never was.
func backOutFacts() ReversibleFacts {
	return withGroups(placedFacts(reversibleGateway("cp", true),
		map[string]MirrorRow{"a": active},
		map[string]MirrorRow{"a": stopped, "b": active},
		"a", "b"), nil)
}

func TestReconcileReversibleStatic_BackOutPlan(t *testing.T) {
	p := ReconcileReversibleStatic(reversibleInput("cp"), backOutFacts())
	requirePlanned(t, p)
	r := p.Reversal
	if !r.Sides.BackOut || r.ReverseOn != SideSource || !reflect.DeepEqual(r.Reverse, []string{"a"}) || !reflect.DeepEqual(r.SwitchOnly, []string{"b"}) {
		t.Errorf("reversal = %+v", r)
	}
	if !bytes.Equal(p.Artifacts.SwitchoverRules, p.Artifacts.RollbackFenceRules) {
		t.Errorf("a back-out's switch only removes the fence, so it equals the rollback route:\n%s\nvs\n%s",
			p.Artifacts.SwitchoverRules, p.Artifacts.RollbackFenceRules)
	}
	if !p.Artifacts.FencedAtStart || !reflect.DeepEqual(p.Artifacts.MigrateTopics, []string{"a", "b"}) {
		t.Errorf("artifacts = %+v", p.Artifacts)
	}
}

func TestReconcileReversibleStatic_BackOutNothingReversedOnlyUnfences(t *testing.T) {
	f := withGroups(neverReversedFacts(), nil)
	f.Gateway = reversibleGateway("cp", true)
	p := ReconcileReversibleStatic(reversibleInput("cp"), f)
	requirePlanned(t, p)
	if p.NothingToDo || len(p.Report.SwitchOnly) != 2 || len(p.Report.Migratable) != 0 || p.Reversal.Reverse != nil {
		t.Errorf("want two switch-only topics and the fence to remove, got %+v / %+v", p.Report, p.Reversal)
	}
}

func TestReconcileReversibleStatic_NothingToDo(t *testing.T) {
	f := placedFacts(reversibleGateway("cc", false),
		map[string]MirrorRow{"a": active, "b": active},
		map[string]MirrorRow{"a": stopped, "b": stopped},
		"a", "b")
	in := reversibleInput("cc")
	p := ReconcileReversibleStatic(in, f)
	if p.Report.Refused() || !p.NothingToDo || p.Artifacts != nil || len(p.Report.Unchanged) != 2 {
		t.Fatalf("want nothing to do, got %+v", p)
	}
	sides, _ := ResolveSides(in, f.Gateway)
	if !reflect.DeepEqual(p.Reversal, &Reversal{Sides: sides}) {
		t.Errorf("reversal = %+v", p.Reversal)
	}
}

func TestReconcileReversibleStatic_LinkRefusalHasNoTopics(t *testing.T) {
	f := withGroups(neverReversedFacts(), nil)
	f.SourceLink = LinkCopy{}
	p := ReconcileReversibleStatic(reversibleInput("cc"), f)
	if !p.Report.Refused() || p.Mode != ModeReversibleStatic {
		t.Fatalf("want refused, got %+v", p)
	}
	r := p.Report
	if n := len(r.Migratable) + len(r.SwitchOnly) + len(r.AwaitStopped) + len(r.Unchanged) + len(r.FailFast); n != 0 {
		t.Errorf("a link refusal places no topics, got %d", n)
	}
	if p.Artifacts != nil || p.Reversal != nil {
		t.Errorf("refusal carries artifacts or a reversal: %+v", p)
	}
}

func TestReconcileReversibleStatic_RefusalHasNoArtifactsOrReversal(t *testing.T) {
	f := withGroups(neverReversedFacts(), map[string][]string{"g1": {"a"}})
	f.Groups.EnteredStates["g1"] = "Stable"
	p := ReconcileReversibleStatic(reversibleInput("cc"), f)
	if !p.Report.Refused() || !hasFailedCheck(p.Report.Preconditions, RSSplitBrainCheckName) {
		t.Fatalf("want a split-brain refusal, got %+v", p.Report.Preconditions)
	}
	if p.Artifacts != nil || p.Reversal != nil || p.NothingToDo {
		t.Errorf("refusal carries something to act on: %+v", p)
	}
	if len(p.Report.Migratable) != 2 {
		t.Errorf("a group refusal still shows the placed topics: %+v", p.Report)
	}
}

func TestReconcileReversibleStatic_UnresolvedWritableOnRefusesWithAReason(t *testing.T) {
	p := ReconcileReversibleStatic(reversibleInput("x"), withGroups(neverReversedFacts(), nil))
	if !p.Report.Refused() {
		t.Fatalf("an unresolved writable side must refuse with a failed check: %+v", p.Report)
	}
	if p.Artifacts != nil || p.Reversal != nil {
		t.Errorf("refusal carries something to act on: %+v", p)
	}
}

func TestReconcileReversibleStatic_PatternThatDoesNotCompileRefuses(t *testing.T) {
	in := reversibleInput("cc")
	in.TopicPatterns = []string{"("}
	p := ReconcileReversibleStatic(in, withGroups(neverReversedFacts(), nil))
	if !hasFailedCheck(p.Report.Preconditions, "selector patterns compile") {
		t.Errorf("preconditions = %+v", p.Report.Preconditions)
	}
}

func TestReconcileReversibleStatic_PodsCannotTellWhileFencedRefuses(t *testing.T) {
	f := withGroups(neverReversedFacts(), nil)
	f.Gateway = reversibleGateway("cp", true)
	f.Gateway.Sync = GatewaySync{Verdict: SyncCannotTell, Detail: "no configId on the pods"}
	p := ReconcileReversibleStatic(reversibleInput("cc"), f)
	if !p.Report.Refused() || !hasFailedCheck(p.Report.Preconditions, GatewayPodsCheckName) {
		t.Fatalf("a fenced resume relies on the fence being served, so cannot-tell refuses: %+v", p.Report.Preconditions)
	}
	if p.Artifacts != nil || p.Reversal != nil {
		t.Errorf("refusal carries something to act on: %+v", p)
	}
}

func TestReconcileReversibleStatic_InScopeGroupsCarried(t *testing.T) {
	f := withGroups(neverReversedFacts(), map[string][]string{"g2": {"b"}, "g1": {"a"}, "g3": {}})
	p := ReconcileReversibleStatic(reversibleInput("cc"), f)
	requirePlanned(t, p)
	if !reflect.DeepEqual(p.Reversal.InScopeGroups, []string{"g1", "g2"}) {
		t.Errorf("InScopeGroups = %q", p.Reversal.InScopeGroups)
	}
}
