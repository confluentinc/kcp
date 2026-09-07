package migplan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

func gm(tgs []manifest.TopicGroup) *manifest.GatewayMigration {
	g := &manifest.GatewayMigration{}
	g.Spec.TopicGroups = tgs
	return g
}

func strs(s ...string) *[]string { return &s }

func TestBuildReconcileInput(t *testing.T) {
	in, err := buildReconcileInput(gm([]manifest.TopicGroup{{
		Topics:                strs("a", "b"),
		TopicPatterns:         strs("team-.*"),
		Route:                 "migration-route",
		TargetStreamingDomain: "cc",
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
		name string
		tgs  []manifest.TopicGroup
	}{
		{"zero topicGroups", nil},
		{"more than one topicGroup", []manifest.TopicGroup{
			{Topics: strs("a"), Route: "r", TargetStreamingDomain: "cc"},
			{Topics: strs("b"), Route: "r", TargetStreamingDomain: "cc"},
		}},
		{"missing route", []manifest.TopicGroup{{Topics: strs("a"), TargetStreamingDomain: "cc"}}},
		{"missing targetStreamingDomain", []manifest.TopicGroup{{Topics: strs("a"), Route: "r"}}},
		{"no topics or patterns", []manifest.TopicGroup{{Route: "r", TargetStreamingDomain: "cc"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := buildReconcileInput(gm(c.tgs)); err == nil {
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
	})
	if ok.Refused {
		t.Error("a plan with artifacts must not be Refused")
	}
	if ok.FenceYAML != "fence-yaml" || ok.SwitchoverYAML != "switch-yaml" {
		t.Errorf("artifact strings = %q / %q", ok.FenceYAML, ok.SwitchoverYAML)
	}
	if len(ok.Topics) != 2 || len(ok.Reasons) != 0 {
		t.Errorf("topics=%v reasons=%v", ok.Topics, ok.Reasons)
	}

	// refused: nil artifacts, Refused true, reasons from failed checks + fail-fast
	ref := newResult(&reconcile.Plan{
		Report: reconcile.Report{
			Preconditions: []reconcile.PreconditionResult{{Name: "route is dynamic", OK: false, Detail: "is static"}},
			FailFast:      []reconcile.TopicVerdict{{Topic: "x", Reason: "not on the cluster link"}},
		},
	})
	if !ref.Refused {
		t.Error("a refused plan must have Refused=true")
	}
	if ref.FenceYAML != "" || len(ref.Topics) != 0 {
		t.Error("a refused plan must carry no artifacts")
	}
	if len(ref.Reasons) != 2 {
		t.Fatalf("expected 2 reasons, got %v", ref.Reasons)
	}
	if ref.Reasons[0] != "route is dynamic: is static" || ref.Reasons[1] != "x: not on the cluster link" {
		t.Errorf("reasons = %v", ref.Reasons)
	}
}
