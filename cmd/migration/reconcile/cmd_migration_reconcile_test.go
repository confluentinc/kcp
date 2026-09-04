package reconcile

import "testing"

func TestBuildReconcileInput(t *testing.T) {
	in, err := buildReconcileInput(reconcileFlags{
		route:         "migration-route",
		targetDomain:  "cc",
		topics:        []string{"a", "b"},
		topicPatterns: []string{"team-.*"},
	})
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
	// missing route
	if _, err := buildReconcileInput(reconcileFlags{targetDomain: "cc", topics: []string{"a"}}); err == nil {
		t.Error("missing route must error")
	}
	// missing target domain
	if _, err := buildReconcileInput(reconcileFlags{route: "r", topics: []string{"a"}}); err == nil {
		t.Error("missing target-domain must error")
	}
	// neither topics nor patterns
	if _, err := buildReconcileInput(reconcileFlags{route: "r", targetDomain: "cc"}); err == nil {
		t.Error("no topics/patterns must error")
	}
}
