package reconcile

import (
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
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
