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
	b, err := yaml.Marshal(doc["fencing"])
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

	p := Reconcile(in, gw, source, target, mirrors, false)
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
	// present on source, but not a mirror -> F3
	p := Reconcile(in, gw, []string{"lonely"}, nil, map[string]MirrorState{}, false)
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
	p := Reconcile(in, gw, []string{"x"}, nil, map[string]MirrorState{"x": MirrorActive}, false)
	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("failed precondition must refuse and emit no artifacts")
	}
}

// TestReconcileRefusesOnOversizedRules exercises the P6 size guardrail: a
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

	p := Reconcile(in, gw, source, nil, mirrors, false)

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

	p := Reconcile(in, gw, source, target, mirrors, false)

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
