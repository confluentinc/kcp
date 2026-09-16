package reconcile

import (
	"fmt"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// fencingSection isolates the `fencing` subtree of a serialized rules block so
// assertions about it can't accidentally match a batch topic name that
// legitimately appears elsewhere in the document (e.g. routing.conditions).
func fencingSection(t *testing.T, raw []byte) string {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal rules block: %v", err)
	}
	// The artifact is the whole `rules` subtree, wrapped under a top-level
	// rules: key — navigate into it before isolating fencing.
	rules, ok := doc["rules"].(map[string]any)
	if !ok {
		t.Fatalf("serialized artifact must have a top-level rules: key, got: %s", raw)
	}
	b, err := yaml.Marshal(rules["fencing"])
	if err != nil {
		t.Fatalf("marshal fencing section: %v", err)
	}
	return string(b)
}

func TestReconcileHappyPath(t *testing.T) {
	gw := dynGateway()
	gw.Route.Rules = map[string]any{
		// operator's pre-existing fencing entry; must survive both artifacts.
		"fencing": []any{map[string]any{"trafficType": "TRANSACTION"}},
		"routing": map[string]any{
			"coordination": map[string]any{"group": "msk"},
			"conditions":   []any{map[string]any{"topicPatterns": []any{"team-a.*"}, "streamingDomain": "msk"}},
			"default":      "msk",
		},
	}
	in := ReconcileInput{TopicPatterns: []string{"team-a.*"}, Route: "migration-route", TargetDomain: "cc"}
	source := []string{"team-a.orders", "team-a.payments"}
	target := []string{"team-a.orders", "team-a.payments"}
	mirrors := map[string]MirrorState{"team-a.orders": MirrorActive, "team-a.payments": MirrorActive}

	p := Reconcile(in, gw, source, target, mirrors, false, ClusterIDs{}, nil)
	if p.Report.Refused() {
		t.Fatalf("expected success, refused with %+v", p.Report)
	}
	if p.Artifacts == nil {
		t.Fatal("expected artifacts")
	}
	if len(p.Artifacts.Topics) != 2 {
		t.Fatalf("promote list = %v, want 2 topics", p.Artifacts.Topics)
	}
	// invariant: the topic still classifies Migratable (a pre-existing operator
	// fencing block must not change routing/verdict).
	migratableTopics := map[string]bool{}
	for _, tv := range p.Report.Migratable {
		migratableTopics[tv.Topic] = true
	}
	if !migratableTopics["team-a.orders"] || !migratableTopics["team-a.payments"] {
		t.Fatalf("expected both topics classified Migratable, got %+v", p.Report.Migratable)
	}

	fenceFencing := fencingSection(t, p.Artifacts.FenceRules)
	switchFencing := fencingSection(t, p.Artifacts.SwitchoverRules)

	// invariant: the fence artifact's fencing block carries the batch fence entry.
	if !strings.Contains(fenceFencing, "team-a.orders") {
		t.Fatalf("fence rules fencing block must contain the batch topic, got %q", fenceFencing)
	}
	// invariant: the operator's fencing entry SURVIVES in both artifacts —
	// prepending the batch fence must never drop the operator's own entries.
	if !strings.Contains(fenceFencing, "TRANSACTION") {
		t.Fatalf("fence rules fencing block must preserve the operator's fencing entry, got %q", fenceFencing)
	}
	if !strings.Contains(switchFencing, "TRANSACTION") {
		t.Fatalf("switchover rules must preserve the operator's fencing entry, got %q", switchFencing)
	}
	// invariant: the batch topic must NOT appear in the switchover's fencing
	// region as a fenced entry (it belongs in switchover's routing.conditions,
	// not the fencing block — the fencing block is untouched by switchover).
	if strings.Contains(switchFencing, "team-a.orders") {
		t.Fatalf("switchover rules must NOT carry the batch topic as a fenced entry, got %q", switchFencing)
	}
}

func TestReconcileRefusesOnFailFast(t *testing.T) {
	gw := dynGateway()
	in := ReconcileInput{Topics: []string{"lonely"}, Route: "migration-route", TargetDomain: "cc"}
	// present on source, but not a mirror -> blocked (not on the cluster link)
	p := Reconcile(in, gw, []string{"lonely"}, nil, map[string]MirrorState{}, false, ClusterIDs{}, nil)
	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("a fail-fast topic must refuse and emit no artifacts")
	}
	if len(p.Report.FailFast) != 1 {
		t.Fatalf("want 1 fail-fast, got %d", len(p.Report.FailFast))
	}
}

func TestReconcileRefusesOnPrecondition(t *testing.T) {
	gw := dynGateway()
	gw.Route.Mode = "static"
	in := ReconcileInput{Topics: []string{"x"}, Route: "migration-route", TargetDomain: "cc"}
	p := Reconcile(in, gw, []string{"x"}, nil, map[string]MirrorState{"x": MirrorActive}, false, ClusterIDs{}, nil)
	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("failed precondition must refuse and emit no artifacts")
	}
}

// TestReconcileRefusesOnOversizedRules exercises the size guardrail: a
// migratable batch large enough that the serialized rules block exceeds
// MaxRulesBytes (512*1024) must refuse with no artifacts, not silently emit
// an oversized rules file.
func TestReconcileRefusesOnOversizedRules(t *testing.T) {
	gw := dynGateway() // BoundDomains msk/cc, coordination.group=msk, default=msk (source)

	const n = 32000 // ~544KB serialized fencing/conditions block, comfortably > 512KiB
	source := make([]string, n)
	mirrors := make(map[string]MirrorState, n)
	for i := 0; i < n; i++ {
		topic := fmt.Sprintf("topic-%06d", i)
		source[i] = topic
		mirrors[topic] = MirrorActive
	}

	// default domain is msk (source), so with no matching condition every
	// topic routes to source -> !routesToTarget -> Migratable, for all n.
	in := ReconcileInput{TopicPatterns: []string{".*"}, Route: "migration-route", TargetDomain: "cc"}

	p := Reconcile(in, gw, source, nil, mirrors, false, ClusterIDs{}, nil)

	if !p.Report.Refused() {
		t.Fatal("oversized rules block must refuse")
	}
	if p.Artifacts != nil {
		t.Fatal("oversized rules block must emit no artifacts")
	}
	found := false
	for _, pc := range p.Report.Preconditions {
		if pc.Name == "rules block within size limit" && !pc.OK {
			found = true
			if !strings.Contains(pc.Detail, fmt.Sprintf("%d", MaxRulesBytes)) {
				t.Errorf("size-limit failure detail should mention the limit (%d), got %q", MaxRulesBytes, pc.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("expected a failed %q precondition, got %+v", "rules block within size limit", p.Report.Preconditions)
	}
}

// TestReconcileNoopWhenAllUnchanged exercises the "nothing migratable"
// no-op branch: every selected topic already classifies as Unchanged
// (mirror==Stopped, already routed to target, present on target), so the
// run must succeed (not refused) but emit no artifacts — there is nothing
// left to do.
func TestReconcileNoopWhenAllUnchanged(t *testing.T) {
	gw := dynGateway()
	gw.Route.Rules = map[string]any{"routing": map[string]any{
		"coordination": map[string]any{"group": "msk"},
		"conditions":   []any{map[string]any{"topics": []any{"done"}, "streamingDomain": "cc"}},
		"default":      "msk",
	}}
	in := ReconcileInput{Topics: []string{"done"}, Route: "migration-route", TargetDomain: "cc"}
	source := []string{"done"}
	target := []string{"done"}
	mirrors := map[string]MirrorState{"done": MirrorStopped}

	p := Reconcile(in, gw, source, target, mirrors, false, ClusterIDs{}, nil)

	if p.Report.Refused() {
		t.Fatalf("an all-Unchanged batch must not be refused, got %+v", p.Report)
	}
	if len(p.Report.Unchanged) != 1 || p.Report.Unchanged[0].Topic != "done" {
		t.Fatalf("expected %q classified Unchanged, got %+v", "done", p.Report.Unchanged)
	}
	if p.Artifacts != nil {
		t.Fatal("an all-Unchanged batch (nothing migratable) must emit no artifacts")
	}
}

// TestReconcileWiresExplodeError proves the selector-explosion error is
// surfaced through Reconcile (not just by Explode in isolation): a syntactically
// invalid --topic-pattern must land as a failed "selector patterns compile"
// precondition, refuse the run, and emit no artifacts.
func TestReconcileWiresExplodeError(t *testing.T) {
	gw := dynGateway()
	// "[" anchors to ^(?:[)$ — an unterminated character class, a compile error.
	in := ReconcileInput{TopicPatterns: []string{"["}, Route: "migration-route", TargetDomain: "cc"}

	p := Reconcile(in, gw, []string{"team-a.orders"}, nil, map[string]MirrorState{}, false, ClusterIDs{}, nil)

	if !p.Report.Refused() {
		t.Fatal("a bad selector pattern must refuse the run")
	}
	if p.Artifacts != nil {
		t.Fatal("a refused run must emit no artifacts")
	}
	found := false
	for _, pc := range p.Report.Preconditions {
		if pc.Name == "selector patterns compile" && !pc.OK {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a failed %q precondition, got %+v", "selector patterns compile", p.Report.Preconditions)
	}
}

// TestReconcileEmitsShadowWarning: when a migratable topic also appears in one of
// the operator's existing EXACT-name routing conditions, the prepend shadows it,
// and the run must WARN (never remove the operator's entry). The topic still
// migrates — the warning is advisory, not a refusal.
func TestReconcileEmitsShadowWarning(t *testing.T) {
	gw := dynGateway()
	// Operator already routes team-a.orders to the source domain by exact name.
	// Migrating it prepends an exact ->cc condition that shadows this entry.
	gw.Route.Rules = map[string]any{"routing": map[string]any{
		"coordination": map[string]any{"group": "msk"},
		"conditions":   []any{map[string]any{"topics": []any{"team-a.orders"}, "streamingDomain": "msk"}},
		"default":      "msk",
	}}
	in := ReconcileInput{Topics: []string{"team-a.orders"}, Route: "migration-route", TargetDomain: "cc"}
	source := []string{"team-a.orders"}
	target := []string{"team-a.orders"}
	mirrors := map[string]MirrorState{"team-a.orders": MirrorActive}

	p := Reconcile(in, gw, source, target, mirrors, false, ClusterIDs{}, nil)

	if p.Report.Refused() {
		t.Fatalf("a shadowing migration must warn, not refuse: %+v", p.Report)
	}
	if p.Artifacts == nil {
		t.Fatal("the topic still migrates — artifacts must be emitted")
	}
	if len(p.Report.Warnings) == 0 {
		t.Fatal("expected a shadow warning, got none")
	}
	if !strings.Contains(p.Report.Warnings[0], "team-a.orders") || !strings.Contains(p.Report.Warnings[0], "shadowed") {
		t.Fatalf("shadow warning must name the shadowed topic, got %q", p.Report.Warnings[0])
	}
}

func TestReconcileStaticHappyPath(t *testing.T) {
	gw := staticGateway()
	in := ReconcileInput{Topics: []string{"team-a.orders"}, Route: "migration-route", TargetDomain: "cc"}
	source := []string{"team-a.orders"}
	target := []string{}
	mirrors := map[string]MirrorState{"team-a.orders": MirrorActive}

	p := Reconcile(in, gw, source, target, mirrors, false, ClusterIDs{}, nil)
	if p.Mode != "static" {
		t.Fatalf("Mode = %q, want static", p.Mode)
	}
	if p.Report.Refused() {
		t.Fatalf("expected success, refused with %+v", p.Report)
	}
	if p.Artifacts == nil {
		t.Fatal("expected artifacts")
	}
	if len(p.Artifacts.Topics) != 1 || p.Artifacts.Topics[0] != "team-a.orders" {
		t.Fatalf("promote list = %v, want [team-a.orders]", p.Artifacts.Topics)
	}
	if !strings.Contains(string(p.Artifacts.FenceRules), "fence:") {
		t.Fatalf("FenceRules = %q, want a fence fragment", p.Artifacts.FenceRules)
	}
	if !strings.Contains(string(p.Artifacts.SwitchoverRules), "streamingDomain:") {
		t.Fatalf("SwitchoverRules = %q, want a streamingDomain fragment", p.Artifacts.SwitchoverRules)
	}
}

func TestReconcileStaticRefusesOnPrecondition(t *testing.T) {
	gw := staticGateway()
	in := ReconcileInput{Topics: []string{"team-a.orders"}, Route: "migration-route", TargetDomain: "gcp"} // undeclared domain
	p := Reconcile(in, gw, []string{"team-a.orders"}, nil, map[string]MirrorState{"team-a.orders": MirrorActive}, false, ClusterIDs{}, nil)
	if !p.Report.Refused() {
		t.Fatal("expected refusal on undeclared target domain")
	}
	if p.Artifacts != nil {
		t.Fatal("a refused plan must carry no artifacts")
	}
}

func TestReconcileStaticNarrowsTopicScopeToSelector(t *testing.T) {
	// A topic NOT resolved by this run's selector must never affect this
	// plan's outcome, even if it's a bad/inactive mirror elsewhere on the
	// link — confirms the deliberately narrowed scope (design doc decision
	// 8): only topics resolved by Explode/Classify are evaluated, not every
	// mirror on the whole cluster link.
	gw := staticGateway()
	in := ReconcileInput{Topics: []string{"team-a.orders"}, Route: "migration-route", TargetDomain: "cc"}
	source := []string{"team-a.orders", "unrelated.topic"}
	mirrors := map[string]MirrorState{
		"team-a.orders":   MirrorActive,
		"unrelated.topic": MirrorBad, // must not affect this run at all
	}
	p := Reconcile(in, gw, source, nil, mirrors, false, ClusterIDs{}, nil)
	if p.Report.Refused() {
		t.Fatalf("an unrelated bad-mirror topic outside the selector must not refuse this plan, got %+v", p.Report)
	}
}

func TestReconcileStaticTopicPatterns(t *testing.T) {
	// Static mode gains full topicPatterns support, unified with dynamic's
	// Explode — design doc decision 6.
	gw := staticGateway()
	in := ReconcileInput{TopicPatterns: []string{"team-a.*"}, Route: "migration-route", TargetDomain: "cc"}
	source := []string{"team-a.orders", "team-a.payments", "team-b.other"}
	mirrors := map[string]MirrorState{"team-a.orders": MirrorActive, "team-a.payments": MirrorActive}
	p := Reconcile(in, gw, source, nil, mirrors, false, ClusterIDs{}, nil)
	if p.Report.Refused() {
		t.Fatalf("expected success, refused with %+v", p.Report)
	}
	if len(p.Artifacts.Topics) != 2 {
		t.Fatalf("promote list = %v, want 2 topics (team-b.other must not match)", p.Artifacts.Topics)
	}
}

func TestReconcileStaticNoopWhenAlreadySwitched(t *testing.T) {
	// DEVIATION FROM BRIEF (documented in task-6-report.md): the brief's
	// original version of this test asserted `!p.Report.Refused()`, expecting
	// the run to reach per-topic classification and land Unchanged. But
	// CheckStaticPreconditions (Task 4, already committed, out of this
	// task's scope) has its own locked-in, separately-tested behavior
	// (TestStaticPreconditionsRoutesToTargetWhenAlreadyBound in
	// staticpreconditions_test.go) that fails the "route is not already
	// bound to the target domain" precondition whenever routesToTarget is
	// true — which this scenario's setup requires, since Classify's
	// Unchanged verdict itself needs routesToTarget==true. So a re-run
	// against an already-switched static route can never reach
	// classification: it is refused at the precondition gate instead, with
	// no artifacts either way. reconcileStatic deliberately adds no logic to
	// suppress that refusal (see reconcileStatic's doc comment / design doc
	// decision 8: "no new refusal logic is needed"), so this test asserts
	// the real, correct outcome — refused, no artifacts — rather than the
	// brief's original (unreachable) expectation.
	gw := staticGateway()
	route := gw.RawObj["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)
	route["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"} // already switched
	in := ReconcileInput{Topics: []string{"team-a.orders"}, Route: "migration-route", TargetDomain: "cc"}
	p := Reconcile(in, gw, []string{"team-a.orders"}, []string{"team-a.orders"},
		map[string]MirrorState{"team-a.orders": MirrorStopped}, false, ClusterIDs{}, nil)
	if !p.Report.Refused() {
		t.Fatalf("a re-run against an already-switched static route is refused at the precondition gate (see comment above), got: %+v", p.Report)
	}
	if p.Artifacts != nil {
		t.Fatal("a refused re-run must produce no artifacts")
	}
}

func TestReconcileDynamicStillReturnsMode(t *testing.T) {
	// Confirms the new Mode field is set correctly on the existing dynamic
	// path too, not just static.
	gw := dynGateway()
	gw.Route.Rules = map[string]any{"routing": map[string]any{"coordination": map[string]any{"group": "msk"}, "default": "msk"}}
	in := ReconcileInput{TopicPatterns: []string{"team-a.*"}, Route: "migration-route", TargetDomain: "cc"}
	p := Reconcile(in, gw, []string{"team-a.orders"}, []string{"team-a.orders"}, map[string]MirrorState{"team-a.orders": MirrorActive}, false, ClusterIDs{}, nil)
	if p.Mode != "dynamic" {
		t.Fatalf("Mode = %q, want dynamic", p.Mode)
	}
}
