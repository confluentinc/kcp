package migplan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

func gm(name, target string, tgs []manifest.TopicGroupEntry) *manifest.GatewayMigration {
	g := &manifest.GatewayMigration{}
	g.Spec.Route = manifest.Route{Name: name, TopicGroup: tgs, TargetStreamingDomain: target}
	return g
}

func strs(s ...string) *[]string { return &s }

func TestBuildReconcileInput(t *testing.T) {
	in, err := buildReconcileInput(gm("migration-route", "cc", []manifest.TopicGroupEntry{{
		Topics:        strs("a", "b"),
		TopicPatterns: strs("team-.*"),
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if in.Route != "migration-route" || in.TargetDomain != "cc" {
		t.Errorf("route/target = %q/%q", in.Route, in.TargetDomain)
	}
	if len(in.Topics) != 2 || len(in.TopicPatterns) != 1 {
		t.Errorf("topics/patterns = %v / %v", in.Topics, in.TopicPatterns)
	}
}

func TestBuildReconcileInputValidation(t *testing.T) {
	cases := []struct {
		name   string
		route  string
		target string
		tgs    []manifest.TopicGroupEntry
	}{
		{"zero topicGroups", "r", "cc", nil},
		{"more than one topicGroup", "r", "cc", []manifest.TopicGroupEntry{
			{Topics: strs("a")},
			{Topics: strs("b")},
		}},
		{"missing route", "", "cc", []manifest.TopicGroupEntry{{Topics: strs("a")}}},
		{"missing targetStreamingDomain", "r", "", []manifest.TopicGroupEntry{{Topics: strs("a")}}},
		{"no topics or patterns", "r", "cc", []manifest.TopicGroupEntry{{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := buildReconcileInput(gm(c.route, c.target, c.tgs)); err == nil {
				t.Errorf("%s must error", c.name)
			}
		})
	}
}

func TestNewResult(t *testing.T) {
	// success: artifacts mapped, not refused, no reasons
	ok := newResult(&reconcile.Plan{
		Report: reconcile.Report{Migratable: []reconcile.TopicVerdict{{Topic: "a"}}},
		Artifacts: &reconcile.Artifacts{
			Topics:          []string{"a", "b"},
			FenceRules:      []byte("fence-yaml"),
			SwitchoverRules: []byte("switch-yaml"),
		},
		GatewayYAML: "gw-yaml",
		Mode:        "static",
	})
	if ok.Refused {
		t.Error("a plan with artifacts must not be Refused")
	}
	if ok.FenceYAML != "fence-yaml" || ok.SwitchoverYAML != "switch-yaml" {
		t.Errorf("artifact strings = %q / %q", ok.FenceYAML, ok.SwitchoverYAML)
	}
	if ok.GatewayYAML != "gw-yaml" {
		t.Errorf("GatewayYAML = %q, want gw-yaml", ok.GatewayYAML)
	}
	if len(ok.Topics) != 2 || len(ok.Reasons) != 0 {
		t.Errorf("topics=%v reasons=%v", ok.Topics, ok.Reasons)
	}
	if ok.Mode != "static" {
		t.Errorf("Mode = %q, want static (must mirror plan.Mode)", ok.Mode)
	}

	// refused: nil artifacts, Refused true, reasons from failed checks + fail-fast,
	// but the pulled gateway YAML is still carried (the pull precedes the checks).
	ref := newResult(&reconcile.Plan{
		Report: reconcile.Report{
			Preconditions: []reconcile.PreconditionResult{{Name: "route is dynamic", OK: false, Detail: "is static"}},
			FailFast:      []reconcile.TopicVerdict{{Topic: "x", Reason: "not on the cluster link"}},
		},
		GatewayYAML: "gw-yaml",
	})
	if !ref.Refused {
		t.Error("a refused plan must have Refused=true")
	}
	if ref.FenceYAML != "" || len(ref.Topics) != 0 {
		t.Error("a refused plan must carry no artifacts")
	}
	if ref.GatewayYAML != "gw-yaml" {
		t.Errorf("a refused plan must still carry the pulled GatewayYAML, got %q", ref.GatewayYAML)
	}
	if len(ref.Reasons) != 2 {
		t.Fatalf("expected 2 reasons, got %v", ref.Reasons)
	}
	if ref.Reasons[0] != "route is dynamic: is static" || ref.Reasons[1] != "x: not on the cluster link" {
		t.Errorf("reasons = %v", ref.Reasons)
	}
}
