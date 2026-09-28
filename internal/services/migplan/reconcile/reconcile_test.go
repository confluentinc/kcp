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

// assertSetEqual compares two string slices for equality ignoring order.
func assertSetEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	gotSet := toSet(got)
	wantSet := toSet(want)
	if len(gotSet) != len(wantSet) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for w := range wantSet {
		if _, ok := gotSet[w]; !ok {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

// rulesFencingTopics/rulesSwitchTopics flatten every entry under
// rules.fencing[].topics / rules.routing.conditions[].topics respectively,
// across all entries in the serialized artifact (not just the prepended
// batch entry), since a batch may share the block with operator entries.
func rulesFencingTopics(t *testing.T, raw []byte) []string {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal rules block: %v", err)
	}
	rules, ok := doc["rules"].(map[string]any)
	if !ok {
		t.Fatalf("serialized artifact must have a top-level rules: key, got: %s", raw)
	}
	fencing, _ := rules["fencing"].([]any)
	var out []string
	for _, e := range fencing {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		topics, _ := entry["topics"].([]any)
		for _, top := range topics {
			if s, ok := top.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func rulesSwitchTopics(t *testing.T, raw []byte) []string {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal rules block: %v", err)
	}
	rules, ok := doc["rules"].(map[string]any)
	if !ok {
		t.Fatalf("serialized artifact must have a top-level rules: key, got: %s", raw)
	}
	routing, _ := rules["routing"].(map[string]any)
	conditions, _ := routing["conditions"].([]any)
	var out []string
	for _, e := range conditions {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		topics, _ := entry["topics"].([]any)
		for _, top := range topics {
			if s, ok := top.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// assertRulesFenceTopics asserts the serialized fence rules block's
// rules.fencing[].topics entries (across all entries) equal want, ignoring order.
func assertRulesFenceTopics(t *testing.T, raw []byte, want []string) {
	t.Helper()
	assertSetEqual(t, "fence topics", rulesFencingTopics(t, raw), want)
}

// assertRulesSwitchTopics asserts the serialized switchover rules block's
// rules.routing.conditions[].topics entries (across all entries) equal want,
// ignoring order.
func assertRulesSwitchTopics(t *testing.T, raw []byte, want []string) {
	t.Helper()
	assertSetEqual(t, "switch topics", rulesSwitchTopics(t, raw), want)
}

// TestReconcileDynamic_ResumeMixedBatch: a batch where t1 is already promoted
// (STOPPED, still routed ->source) and only needs switching, t2 is mid-promote
// (PENDING_STOPPED) and must await STOPPED then switch, and t3 is fresh
// (ACTIVE) and needs the full promote+switch. Must NOT refuse; must plan to
// promote only t2+t3 (t1 is already STOPPED), and switch/fence all three.
func TestReconcileDynamic_ResumeMixedBatch(t *testing.T) {
	gw := dynGateway() // BoundDomains msk/cc, coordination.group=msk, default=msk (source)
	in := ReconcileInput{Topics: []string{"t1", "t2", "t3"}, Route: "migration-route", TargetDomain: "cc"}
	sourceTopics := []string{"t1", "t2", "t3"}
	targetTopics := []string{"t1", "t2", "t3"}
	mirrors := map[string]MirrorState{"t1": MirrorStopped, "t2": MirrorPending, "t3": MirrorActive}

	plan := reconcileDynamic(in, gw, sourceTopics, targetTopics, mirrors, false, ClusterIDs{})

	if plan.Report.Refused() {
		t.Fatalf("resume plan refused, want a plan: %+v", plan.Report)
	}
	if plan.Artifacts == nil {
		t.Fatal("Artifacts nil, want a resume plan")
	}
	assertSetEqual(t, "promote", plan.Artifacts.PromoteTopics, []string{"t2", "t3"})
	// fence/switchover rules must name all three (t1 via SwitchOnly, t2 Await, t3 Migratable)
	assertRulesFenceTopics(t, plan.Artifacts.FenceRules, []string{"t1", "t2", "t3"})
	assertRulesSwitchTopics(t, plan.Artifacts.SwitchoverRules, []string{"t1", "t2", "t3"})

	if len(plan.Report.SwitchOnly) != 1 || plan.Report.SwitchOnly[0].Topic != "t1" {
		t.Fatalf("expected t1 classified SwitchOnly, got %+v", plan.Report.SwitchOnly)
	}
	if len(plan.Report.AwaitStopped) != 1 || plan.Report.AwaitStopped[0].Topic != "t2" {
		t.Fatalf("expected t2 classified AwaitStopped, got %+v", plan.Report.AwaitStopped)
	}
	if len(plan.Report.Migratable) != 1 || plan.Report.Migratable[0].Topic != "t3" {
		t.Fatalf("expected t3 classified Migratable, got %+v", plan.Report.Migratable)
	}
}

// TestReconcileDynamic_ResumeAlreadyFencedNoDouble: a resume where the live
// route is ALREADY fenced for the batch (a prior run fenced it, then the
// process died before switchover). Reconciling again must NOT double the
// rules.fencing entry — exactly one fence for the batch. End-to-end guard
// for PrependFence's idempotency.
func TestReconcileDynamic_ResumeAlreadyFencedNoDouble(t *testing.T) {
	gw := dynGateway() // BoundDomains msk/cc, coordination.group=msk, default=msk (source)
	// Pre-seed kcp's prior fence for exactly this batch, as a live route would
	// present it on resume (fenced but not yet switched).
	gw.Route.Rules["fencing"] = []any{map[string]any{"topics": []any{"t1", "t2", "t3"}, "blocked": true}}
	in := ReconcileInput{Topics: []string{"t1", "t2", "t3"}, Route: "migration-route", TargetDomain: "cc"}
	sourceTopics := []string{"t1", "t2", "t3"}
	targetTopics := []string{"t1", "t2", "t3"}
	mirrors := map[string]MirrorState{"t1": MirrorActive, "t2": MirrorActive, "t3": MirrorActive}

	plan := reconcileDynamic(in, gw, sourceTopics, targetTopics, mirrors, false, ClusterIDs{})

	if plan.Report.Refused() {
		t.Fatalf("resume-into-fenced plan refused, want a plan: %+v", plan.Report)
	}
	if plan.Artifacts == nil {
		t.Fatal("Artifacts nil, want a resume plan")
	}
	// The produced fence rules must carry exactly ONE fencing entry for the batch.
	var parsed map[string]any
	if err := yaml.Unmarshal(plan.Artifacts.FenceRules, &parsed); err != nil {
		t.Fatalf("parsing FenceRules: %v", err)
	}
	rules, _ := parsed["rules"].(map[string]any)
	fencing, _ := rules["fencing"].([]any)
	blockedForBatch := 0
	for _, e := range fencing {
		m, _ := e.(map[string]any)
		if b, _ := m["blocked"].(bool); b {
			blockedForBatch++
		}
	}
	if blockedForBatch != 1 {
		t.Fatalf("blocked fence entries = %d, want 1 (no doubling on resume): %s", blockedForBatch, plan.Artifacts.FenceRules)
	}
	// And it still names the whole batch.
	assertSetEqual(t, "fence topics", rulesFencingTopics(t, plan.Artifacts.FenceRules), []string{"t1", "t2", "t3"})
}

// TestReconcileDynamic_AllSwitchOnly is the headline resume case: every topic
// in the batch is already promoted (STOPPED) and awaiting only the switch —
// promote (Migratable ∪ AwaitStopped) is empty, but the batch is not a no-op:
// it still must fence + switch both topics.
func TestReconcileDynamic_AllSwitchOnly(t *testing.T) {
	gw := dynGateway() // BoundDomains msk/cc, coordination.group=msk, default=msk (source)
	in := ReconcileInput{Topics: []string{"t1", "t2"}, Route: "migration-route", TargetDomain: "cc"}
	sourceTopics := []string{"t1", "t2"}
	targetTopics := []string{"t1", "t2"}
	mirrors := map[string]MirrorState{"t1": MirrorStopped, "t2": MirrorStopped}

	plan := reconcileDynamic(in, gw, sourceTopics, targetTopics, mirrors, false, ClusterIDs{})

	if plan.Report.Refused() {
		t.Fatalf("all-SwitchOnly plan refused, want a plan: %+v", plan.Report)
	}
	if plan.Artifacts == nil {
		t.Fatal("Artifacts nil, want an actionable switch-only plan")
	}
	if len(plan.Artifacts.PromoteTopics) != 0 {
		t.Fatalf("promote list = %v, want empty (nothing left to promote)", plan.Artifacts.PromoteTopics)
	}
	assertSetEqual(t, "switch-only", topicsOf(plan.Report.SwitchOnly), []string{"t1", "t2"})
	assertRulesFenceTopics(t, plan.Artifacts.FenceRules, []string{"t1", "t2"})
	assertRulesSwitchTopics(t, plan.Artifacts.SwitchoverRules, []string{"t1", "t2"})
}

// TestReconcileStatic_ResumeMixedBatch is the static-route counterpart:
// Topics == {t2,t3} (promote input), and the static fence/switchover
// fragments are still produced (whole-route, unchanged shape) even though
// the batch is a resume mix.
func TestReconcileStatic_ResumeMixedBatch(t *testing.T) {
	gw := staticGateway() // route bound to msk (source), not yet switched -> RoutesToTarget=false
	in := ReconcileInput{Topics: []string{"t1", "t2", "t3"}, Route: "migration-route", TargetDomain: "cc"}
	sourceTopics := []string{"t1", "t2", "t3"}
	targetTopics := []string{"t1", "t2", "t3"}
	mirrors := map[string]MirrorState{"t1": MirrorStopped, "t2": MirrorPending, "t3": MirrorActive}

	plan := reconcileStatic(in, gw, sourceTopics, targetTopics, mirrors, false, ClusterIDs{}, nil, "")

	if plan.Report.Refused() {
		t.Fatalf("resume plan refused, want a plan: %+v", plan.Report)
	}
	if plan.Artifacts == nil {
		t.Fatal("Artifacts nil, want a resume plan")
	}
	assertSetEqual(t, "promote", plan.Artifacts.PromoteTopics, []string{"t2", "t3"})
	if !strings.Contains(string(plan.Artifacts.FenceRules), "fence:") {
		t.Fatalf("FenceRules = %q, want a fence fragment", plan.Artifacts.FenceRules)
	}
	if !strings.Contains(string(plan.Artifacts.SwitchoverRules), "streamingDomain:") {
		t.Fatalf("SwitchoverRules = %q, want a streamingDomain fragment", plan.Artifacts.SwitchoverRules)
	}
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

	p := Reconcile(in, gw, source, target, mirrors, false, ClusterIDs{}, nil, "")
	if p.Report.Refused() {
		t.Fatalf("expected success, refused with %+v", p.Report)
	}
	if p.Artifacts == nil {
		t.Fatal("expected artifacts")
	}
	if len(p.Artifacts.PromoteTopics) != 2 {
		t.Fatalf("promote list = %v, want 2 topics", p.Artifacts.PromoteTopics)
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
	p := Reconcile(in, gw, []string{"lonely"}, nil, map[string]MirrorState{}, false, ClusterIDs{}, nil, "")
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
	p := Reconcile(in, gw, []string{"x"}, nil, map[string]MirrorState{"x": MirrorActive}, false, ClusterIDs{}, nil, "")
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

	p := Reconcile(in, gw, source, nil, mirrors, false, ClusterIDs{}, nil, "")

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

	p := Reconcile(in, gw, source, target, mirrors, false, ClusterIDs{}, nil, "")

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

	p := Reconcile(in, gw, []string{"team-a.orders"}, nil, map[string]MirrorState{}, false, ClusterIDs{}, nil, "")

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

	p := Reconcile(in, gw, source, target, mirrors, false, ClusterIDs{}, nil, "")

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

	p := Reconcile(in, gw, source, target, mirrors, false, ClusterIDs{}, nil, "")
	if p.Mode != "static" {
		t.Fatalf("Mode = %q, want static", p.Mode)
	}
	if p.Report.Refused() {
		t.Fatalf("expected success, refused with %+v", p.Report)
	}
	if p.Artifacts == nil {
		t.Fatal("expected artifacts")
	}
	if len(p.Artifacts.PromoteTopics) != 1 || p.Artifacts.PromoteTopics[0] != "team-a.orders" {
		t.Fatalf("promote list = %v, want [team-a.orders]", p.Artifacts.PromoteTopics)
	}
	if !strings.Contains(string(p.Artifacts.FenceRules), "fence:") {
		t.Fatalf("FenceRules = %q, want a fence fragment", p.Artifacts.FenceRules)
	}
	if !strings.Contains(string(p.Artifacts.SwitchoverRules), "streamingDomain:") {
		t.Fatalf("SwitchoverRules = %q, want a streamingDomain fragment", p.Artifacts.SwitchoverRules)
	}
}

// TestReconcileStaticSecretCheckSkippedDoesNotRefuse is the end-to-end
// counterpart to TestStaticPreconditionsSecretCheckSkipped: a skipped secret
// check reaching Reconcile through its public entry point must not refuse
// the run, and the resulting plan's "staged auth secrets exist" precondition
// must be marked Skipped rather than a plain pass.
func TestReconcileStaticSecretCheckSkippedDoesNotRefuse(t *testing.T) {
	gw := staticGateway()
	in := ReconcileInput{Topics: []string{"team-a.orders"}, Route: "migration-route", TargetDomain: "cc"}
	source := []string{"team-a.orders"}
	target := []string{}
	mirrors := map[string]MirrorState{"team-a.orders": MirrorActive}
	const reason = "no permission to read secrets in namespace \"confluent\""

	p := Reconcile(in, gw, source, target, mirrors, false, ClusterIDs{}, nil, reason)

	if p.Report.Refused() {
		t.Fatalf("a skipped secret check must not refuse the run, got %+v", p.Report)
	}
	if p.Artifacts == nil {
		t.Fatal("expected artifacts (the run succeeded despite the skip)")
	}
	found := false
	for _, pc := range p.Report.Preconditions {
		if pc.Name == "staged auth secrets exist" {
			found = true
			if !pc.OK || !pc.Skipped {
				t.Errorf("staged auth secrets exist = %+v, want OK: true, Skipped: true", pc)
			}
		}
	}
	if !found {
		t.Fatalf("expected a %q precondition, got %+v", "staged auth secrets exist", p.Report.Preconditions)
	}
}

func TestReconcileStaticRefusesOnPrecondition(t *testing.T) {
	gw := staticGateway()
	in := ReconcileInput{Topics: []string{"team-a.orders"}, Route: "migration-route", TargetDomain: "gcp"} // undeclared domain
	p := Reconcile(in, gw, []string{"team-a.orders"}, nil, map[string]MirrorState{"team-a.orders": MirrorActive}, false, ClusterIDs{}, nil, "")
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
	p := Reconcile(in, gw, source, nil, mirrors, false, ClusterIDs{}, nil, "")
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
	p := Reconcile(in, gw, source, nil, mirrors, false, ClusterIDs{}, nil, "")
	if p.Report.Refused() {
		t.Fatalf("expected success, refused with %+v", p.Report)
	}
	if len(p.Artifacts.PromoteTopics) != 2 {
		t.Fatalf("promote list = %v, want 2 topics (team-b.other must not match)", p.Artifacts.PromoteTopics)
	}
}

func TestReconcileStaticNoopWhenAlreadySwitched(t *testing.T) {
	// Idempotency: a re-run against an already-switched static route (route bound
	// to the target, mirrors STOPPED) must be a clean NO-OP — matching dynamic's
	// Unchanged behavior — not a refusal. This reverses the original decision-8
	// "refuse when already bound" stance after the live static resume suite showed
	// it broke re-run idempotency (a completed migration refused instead of
	// no-op'ing) and the kill-after-switch resume. The already-bound route is a
	// valid done-state the classifier lands as Unchanged; there is nothing to do.
	gw := staticGateway()
	route := gw.RawObj["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)
	route["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"} // already switched
	in := ReconcileInput{Topics: []string{"team-a.orders"}, Route: "migration-route", TargetDomain: "cc"}
	p := Reconcile(in, gw, []string{"team-a.orders"}, []string{"team-a.orders"},
		map[string]MirrorState{"team-a.orders": MirrorStopped}, false, ClusterIDs{}, nil, "")
	if p.Report.Refused() {
		t.Fatalf("a re-run against an already-switched static route must be a clean no-op, not refused: %+v", p.Report)
	}
	if p.Artifacts != nil {
		t.Fatal("a completed (already-switched) migration must produce no artifacts — nothing to do")
	}
	if len(p.Report.Unchanged) != 1 || p.Report.Unchanged[0].Topic != "team-a.orders" {
		t.Fatalf("the already-switched topic must classify Unchanged, got %+v", p.Report.Unchanged)
	}
}

// TestReconcileRefusesOnCloneError is the end-to-end counterpart to
// TestCloneReturnsErrorOnUnmarshalableValue: an operator's rules tree that
// cannot be cloned must refuse the whole run — via the normal
// Preconditions/Refused() machinery, not a panic — rather than let a
// corrupt clone reach PrependFence.
func TestReconcileRefusesOnCloneError(t *testing.T) {
	gw := dynGateway()
	gw.Route.Rules = map[string]any{
		"routing": map[string]any{"coordination": map[string]any{"group": "msk"}, "default": "msk"},
		"bad":     make(chan int), // unmarshalable: yaml.Marshal fails inside Clone
	}
	in := ReconcileInput{Topics: []string{"team-a.orders"}, Route: "migration-route", TargetDomain: "cc"}
	source := []string{"team-a.orders"}
	target := []string{"team-a.orders"}
	mirrors := map[string]MirrorState{"team-a.orders": MirrorActive}

	p := Reconcile(in, gw, source, target, mirrors, false, ClusterIDs{}, nil, "")

	if !p.Report.Refused() {
		t.Fatalf("an unclonable rules tree must refuse the run, got %+v", p.Report)
	}
	if p.Artifacts != nil {
		t.Fatal("a refused run must emit no artifacts")
	}
	found := false
	for _, pc := range p.Report.Preconditions {
		if pc.Name == "fence rules clone" && !pc.OK {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a failed %q precondition, got %+v", "fence rules clone", p.Report.Preconditions)
	}
}

func TestReconcileDynamicStillReturnsMode(t *testing.T) {
	// Confirms the new Mode field is set correctly on the existing dynamic
	// path too, not just static.
	gw := dynGateway()
	gw.Route.Rules = map[string]any{"routing": map[string]any{"coordination": map[string]any{"group": "msk"}, "default": "msk"}}
	in := ReconcileInput{TopicPatterns: []string{"team-a.*"}, Route: "migration-route", TargetDomain: "cc"}
	p := Reconcile(in, gw, []string{"team-a.orders"}, []string{"team-a.orders"}, map[string]MirrorState{"team-a.orders": MirrorActive}, false, ClusterIDs{}, nil, "")
	if p.Mode != "dynamic" {
		t.Fatalf("Mode = %q, want dynamic", p.Mode)
	}
}

// On resume the live route already carries kcp's fence (from the interrupted
// run). The SWITCHOVER artifact must NOT carry that fence forward — the switched
// state is unfenced for our topics — so a resumed migration reaches the SAME
// clean end-state as an uninterrupted one. Operator-authored fences are a
// separate concern and must survive (covered elsewhere). Without DropFence, a
// switchover built from the fenced live base would keep kcp's fence on the
// switched route.
func TestReconcileDynamic_ResumeSwitchoverDropsKcpFence(t *testing.T) {
	gw := dynGateway()
	// kcp's prior fence for exactly this batch, as a live route presents it on
	// resume (fenced but not yet switched).
	gw.Route.Rules["fencing"] = []any{map[string]any{"topics": []any{"t1", "t2", "t3"}, "blocked": true}}
	in := ReconcileInput{Topics: []string{"t1", "t2", "t3"}, Route: "migration-route", TargetDomain: "cc"}
	sourceTopics := []string{"t1", "t2", "t3"}
	targetTopics := []string{"t1", "t2", "t3"}
	mirrors := map[string]MirrorState{"t1": MirrorActive, "t2": MirrorActive, "t3": MirrorActive}

	plan := reconcileDynamic(in, gw, sourceTopics, targetTopics, mirrors, false, ClusterIDs{})
	if plan.Artifacts == nil {
		t.Fatal("Artifacts nil, want a resume plan")
	}

	var parsed map[string]any
	if err := yaml.Unmarshal(plan.Artifacts.SwitchoverRules, &parsed); err != nil {
		t.Fatalf("parsing SwitchoverRules: %v", err)
	}
	rules, _ := parsed["rules"].(map[string]any)
	fencing, _ := rules["fencing"].([]any)
	for _, e := range fencing {
		if isKcpFenceFor(e, []string{"t1", "t2", "t3"}) {
			t.Fatalf("switchover must drop kcp's own fence on switch, but it is still present:\n%s", plan.Artifacts.SwitchoverRules)
		}
	}
}

func TestReconcileStatic_ResumeCarriesAwaitStoppedSeparately(t *testing.T) {
	gw := staticGateway()
	in := ReconcileInput{Topics: []string{"t1", "t2", "t3"}, Route: "migration-route", TargetDomain: "cc"}
	sourceTopics := []string{"t1", "t2", "t3"}
	targetTopics := []string{"t1", "t2", "t3"}
	mirrors := map[string]MirrorState{"t1": MirrorStopped, "t2": MirrorPending, "t3": MirrorActive}

	plan := reconcileStatic(in, gw, sourceTopics, targetTopics, mirrors, false, ClusterIDs{}, nil, "")

	assertSetEqual(t, "promote", plan.Artifacts.PromoteTopics, []string{"t2", "t3"})
	assertSetEqual(t, "awaitStopped", plan.Artifacts.AwaitStopped, []string{"t2"})
}

func TestReconcileDynamic_ResumeCarriesAwaitStoppedSeparately(t *testing.T) {
	gw := dynGateway()
	in := ReconcileInput{Topics: []string{"t1", "t2", "t3"}, Route: "migration-route", TargetDomain: "cc"}
	sourceTopics := []string{"t1", "t2", "t3"}
	targetTopics := []string{"t1", "t2", "t3"}
	mirrors := map[string]MirrorState{"t1": MirrorStopped, "t2": MirrorPending, "t3": MirrorActive}

	plan := reconcileDynamic(in, gw, sourceTopics, targetTopics, mirrors, false, ClusterIDs{})

	assertSetEqual(t, "promote", plan.Artifacts.PromoteTopics, []string{"t2", "t3"})
	assertSetEqual(t, "awaitStopped", plan.Artifacts.AwaitStopped, []string{"t2"})
}

// TestReconcileStatic_RestoreOffsetSync pins when a static run owes an
// offset-sync restore: only with the pause opted in, and then either because
// this run pauses (a cutover is in flight) or because an earlier run's pause
// was never restored (the link's live offset sync still differs from the
// declared baseline). A refused run owes nothing.
func TestReconcileStatic_RestoreOffsetSync(t *testing.T) {
	type tc struct {
		name            string
		pause           bool
		baselineEnabled bool
		linkEnabled     bool
		switched        bool // every topic already migrated
		refused         bool
		want            bool
	}
	cases := []tc{
		{name: "pause not requested, cutover in flight", pause: false, baselineEnabled: true, linkEnabled: true, want: false},
		{name: "pause not requested, link left off", pause: false, baselineEnabled: true, linkEnabled: false, switched: true, want: false},
		{name: "cutover in flight", pause: true, baselineEnabled: true, linkEnabled: true, want: true},
		{name: "migrated, link at its enabled baseline", pause: true, baselineEnabled: true, linkEnabled: true, switched: true, want: false},
		{name: "migrated, link still paused", pause: true, baselineEnabled: true, linkEnabled: false, switched: true, want: true},
		{name: "migrated, link at its disabled baseline", pause: true, baselineEnabled: false, linkEnabled: false, switched: true, want: false},
		{name: "migrated, link enabled against a disabled baseline", pause: true, baselineEnabled: false, linkEnabled: true, switched: true, want: true},
		{name: "refused", pause: true, baselineEnabled: true, linkEnabled: false, refused: true, want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gw := staticGateway()
			mirrors := map[string]MirrorState{"t1": MirrorActive, "t2": MirrorActive}
			if c.switched {
				route := gw.RawObj["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)
				route["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}
				mirrors = map[string]MirrorState{"t1": MirrorStopped, "t2": MirrorStopped}
			}
			selected := []string{"t1", "t2"}
			if c.refused {
				selected = append(selected, "missing")
			}
			in := ReconcileInput{Topics: selected, Route: "migration-route", TargetDomain: "cc",
				PauseConsumerOffsetSync: c.pause, OffsetSyncBaselineEnabled: c.baselineEnabled}

			p := Reconcile(in, gw, []string{"t1", "t2"}, []string{"t1", "t2"}, mirrors, c.linkEnabled, ClusterIDs{}, nil, "")

			if p.Mode != "static" {
				t.Fatalf("Mode = %q, want static", p.Mode)
			}
			if p.Report.Refused() != c.refused {
				t.Fatalf("Refused = %v, want %v: %+v", p.Report.Refused(), c.refused, p.Report)
			}
			if p.Report.RestoreOffsetSync != c.want {
				t.Fatalf("RestoreOffsetSync = %v, want %v", p.Report.RestoreOffsetSync, c.want)
			}
		})
	}
}

// TestReconcileDynamic_RefusesOffsetSyncPause: a dynamic route requires the
// link's consumer offset sync to be off, so there is nothing to pause. A
// manifest that asks for the pause is refused on the "offset-sync pause not
// requested" precondition and owes no restore; without it the precondition
// passes.
func TestReconcileDynamic_RefusesOffsetSyncPause(t *testing.T) {
	for _, pause := range []bool{true, false} {
		t.Run(fmt.Sprintf("pause=%v", pause), func(t *testing.T) {
			in := ReconcileInput{Topics: []string{"t1"}, Route: "migration-route", TargetDomain: "cc",
				PauseConsumerOffsetSync: pause, OffsetSyncBaselineEnabled: true}
			p := Reconcile(in, dynGateway(), []string{"t1"}, []string{"t1"}, map[string]MirrorState{"t1": MirrorActive}, false, ClusterIDs{}, nil, "")

			if p.Mode != "dynamic" {
				t.Fatalf("Mode = %q, want dynamic", p.Mode)
			}
			var found *PreconditionResult
			for i := range p.Report.Preconditions {
				if p.Report.Preconditions[i].Name == "offset-sync pause not requested" {
					found = &p.Report.Preconditions[i]
				}
			}
			if found == nil {
				t.Fatalf("no %q precondition in %+v", "offset-sync pause not requested", p.Report.Preconditions)
			}
			if found.OK == pause {
				t.Fatalf("precondition OK = %v with pause=%v: %+v", found.OK, pause, *found)
			}
			if p.Report.Refused() != pause {
				t.Fatalf("Refused = %v, want %v", p.Report.Refused(), pause)
			}
			if pause && !strings.Contains(found.Detail, "pauseConsumerOffsetSync") {
				t.Fatalf("the refusal must name the manifest field, got %q", found.Detail)
			}
			if p.Report.RestoreOffsetSync || p.NothingToDo {
				t.Fatalf("RestoreOffsetSync=%v NothingToDo=%v, want false/false", p.Report.RestoreOffsetSync, p.NothingToDo)
			}
		})
	}
}

// TestReconcileNothingToDo pins the one verdict that lets the caller skip the
// whole run: NothingToDo is true only when the run is not refused and no topic
// is left to migrate. A promoted-but-not-switched batch has no topic to promote
// but still owes the switch, so it is not nothing-to-do.
func TestReconcileNothingToDo(t *testing.T) {
	type tc struct {
		name     string
		selected []string
		pattern  string
		source   []string
		mirrors  map[string]MirrorState
		switched bool // the route already sends the batch to the target
		want     bool
	}
	cases := []tc{
		{name: "every topic already migrated", selected: []string{"t1", "t2"}, source: []string{"t1", "t2"},
			mirrors: map[string]MirrorState{"t1": MirrorStopped, "t2": MirrorStopped}, switched: true, want: true},
		{name: "selector matches no source topic", pattern: "nomatch-.*", source: []string{"t1"}, want: true},
		{name: "fresh batch", selected: []string{"t1", "t2"}, source: []string{"t1", "t2"},
			mirrors: map[string]MirrorState{"t1": MirrorActive, "t2": MirrorActive}},
		{name: "mixed resume batch", selected: []string{"t1", "t2", "t3"}, source: []string{"t1", "t2", "t3"},
			mirrors: map[string]MirrorState{"t1": MirrorStopped, "t2": MirrorPending, "t3": MirrorActive}},
		{name: "promoted, not switched", selected: []string{"t1", "t2"}, source: []string{"t1", "t2"},
			mirrors: map[string]MirrorState{"t1": MirrorStopped, "t2": MirrorStopped}},
		{name: "promotion in flight", selected: []string{"t1", "t2"}, source: []string{"t1", "t2"},
			mirrors: map[string]MirrorState{"t1": MirrorPending, "t2": MirrorPending}},
		{name: "refused", selected: []string{"t1", "missing"}, source: []string{"t1"},
			mirrors: map[string]MirrorState{"t1": MirrorActive}},
	}
	gateways := map[string]func(switched bool, topics []string) *GatewayConfig{
		"dynamic": func(switched bool, topics []string) *GatewayConfig {
			gw := dynGateway()
			if switched {
				gw.Route.Rules = map[string]any{"routing": map[string]any{
					"coordination": map[string]any{"group": "msk"},
					"conditions":   []any{map[string]any{"topics": toAny(topics), "streamingDomain": "cc"}},
					"default":      "msk",
				}}
			}
			return gw
		},
		"static": func(switched bool, _ []string) *GatewayConfig {
			gw := staticGateway()
			if switched {
				route := gw.RawObj["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)
				route["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}
			}
			return gw
		},
	}
	for mode, gateway := range gateways {
		for _, c := range cases {
			t.Run(mode+"/"+c.name, func(t *testing.T) {
				in := ReconcileInput{Topics: c.selected, Route: "migration-route", TargetDomain: "cc"}
				if c.pattern != "" {
					in.TopicPatterns = []string{c.pattern}
				}
				p := Reconcile(in, gateway(c.switched, c.selected), c.source, c.source, c.mirrors, false, ClusterIDs{}, nil, "")
				if p.Mode != mode {
					t.Fatalf("Mode = %q, want %q", p.Mode, mode)
				}
				if p.NothingToDo != c.want {
					t.Fatalf("NothingToDo = %v, want %v (report %+v)", p.NothingToDo, c.want, p.Report)
				}
				if p.NothingToDo && p.Artifacts != nil {
					t.Fatal("a nothing-to-do plan must carry no artifacts")
				}
			})
		}
	}
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// TestReconcileStatic_RestoreOwedIsNotNothingToDo: every topic is migrated,
// but the link's consumer offset sync is still paused against an enabled
// baseline, so the run still owes the restore and is not nothing-to-do.
func TestReconcileStatic_RestoreOwedIsNotNothingToDo(t *testing.T) {
	gw := staticGateway()
	route := gw.RawObj["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)
	route["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}
	in := ReconcileInput{Topics: []string{"t1"}, Route: "migration-route", TargetDomain: "cc",
		PauseConsumerOffsetSync: true, OffsetSyncBaselineEnabled: true}

	p := Reconcile(in, gw, []string{"t1"}, []string{"t1"}, map[string]MirrorState{"t1": MirrorStopped}, false, ClusterIDs{}, nil, "")

	if !p.Report.RestoreOffsetSync {
		t.Fatal("the paused link must owe a restore")
	}
	if p.NothingToDo {
		t.Fatal("a run that owes an offset-sync restore is not nothing-to-do")
	}
}
