package reconcile

import "testing"

func TestReconcileHappyPath(t *testing.T) {
	gw := dynGateway()
	gw.Route.Rules = map[string]any{"routing": map[string]any{
		"coordination": map[string]any{"group": "msk"},
		"conditions":   []any{map[string]any{"topicPatterns": []any{"team-a.*"}, "streamingDomain": "msk"}},
		"default":      "msk",
	}}
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
