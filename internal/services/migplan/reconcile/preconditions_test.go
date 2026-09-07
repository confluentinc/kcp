package reconcile

import "testing"

func dynGateway() *GatewayConfig {
	return &GatewayConfig{Route: &RouteConfig{
		Name: "migration-route", Mode: "dynamic",
		BoundDomains: []string{"msk", "cc"},
		Rules:        map[string]any{"routing": map[string]any{"coordination": map[string]any{"group": "msk"}, "default": "msk"}},
	}}
}

func TestPreconditionsHappy(t *testing.T) {
	in := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}
	res, view, ok := CheckPreconditions(in, dynGateway(), false, ClusterIDs{})
	if !ok {
		t.Fatalf("expected pass, got %+v", res)
	}
	if view.SourceDomain != "msk" || view.TargetDomain != "cc" {
		t.Errorf("domains: source=%q target=%q, want msk/cc", view.SourceDomain, view.TargetDomain)
	}
}

func TestPreconditionsFailures(t *testing.T) {
	base := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}

	// target not bound
	if _, _, ok := CheckPreconditions(ReconcileInput{Route: "migration-route", TargetDomain: "gcp"}, dynGateway(), false, ClusterIDs{}); ok {
		t.Error("unbound target domain must fail")
	}
	// offset sync enabled
	if _, _, ok := CheckPreconditions(base, dynGateway(), true, ClusterIDs{}); ok {
		t.Error("offset sync enabled must fail")
	}
	// coordination not on source
	gw := dynGateway()
	gw.Route.Rules["routing"].(map[string]any)["coordination"].(map[string]any)["group"] = "cc"
	if _, _, ok := CheckPreconditions(base, gw, false, ClusterIDs{}); ok {
		t.Error("coordination not on source must fail")
	}
	// static route
	gw2 := dynGateway()
	gw2.Route.Mode = "static"
	if _, _, ok := CheckPreconditions(base, gw2, false, ClusterIDs{}); ok {
		t.Error("static route must fail (dynamic-only engine)")
	}
	// three bound domains
	gw3 := dynGateway()
	gw3.Route.BoundDomains = []string{"msk", "cc", "extra"}
	if _, _, ok := CheckPreconditions(base, gw3, false, ClusterIDs{}); ok {
		t.Error("more than two bound domains must fail")
	}

	// an operator routing pattern RE2 cannot compile must refuse (rather than
	// letting OwnerRoute silently mis-route the topic).
	gw4 := dynGateway()
	gw4.Route.Rules["routing"].(map[string]any)["conditions"] = []any{
		map[string]any{"streamingDomain": "cc", "topicPatterns": []any{"team-(a"}},
	}
	res, _, ok := CheckPreconditions(base, gw4, false, ClusterIDs{})
	if ok {
		t.Error("an uncompilable operator routing pattern must fail")
	}
	if !hasFailedCheck(res, "gateway routing patterns compile") {
		t.Errorf("expected the routing-patterns-compile check to fail, got %+v", res)
	}
}

func hasFailedCheck(res []PreconditionResult, name string) bool {
	for _, r := range res {
		if r.Name == name && !r.OK {
			return true
		}
	}
	return false
}

func TestPreconditionsClusterIdentity(t *testing.T) {
	base := ReconcileInput{Route: "migration-route", TargetDomain: "cc", TargetClusterID: "tgt-1"}
	gw := dynGateway()

	// all identities line up → pass
	if _, _, ok := CheckPreconditions(base, gw, false, ClusterIDs{Source: "src-1", Target: "tgt-1", LinkSource: "src-1"}); !ok {
		t.Error("matching cluster identities must pass")
	}
	// the link mirrors from a different source than spec.source → fail
	if _, _, ok := CheckPreconditions(base, gw, false, ClusterIDs{Source: "src-1", Target: "tgt-1", LinkSource: "OTHER"}); ok {
		t.Error("link source != source cluster must fail")
	}
	// the target cluster reports a different id than spec.target.clusterId → fail
	if _, _, ok := CheckPreconditions(base, gw, false, ClusterIDs{Source: "src-1", Target: "OTHER", LinkSource: "src-1"}); ok {
		t.Error("target cluster != manifest clusterId must fail")
	}
	// empty ids (destination omits source_cluster_id / metadata unavailable) →
	// can't prove a mismatch, so it must skip (pass), not fail.
	if _, _, ok := CheckPreconditions(base, gw, false, ClusterIDs{}); !ok {
		t.Error("empty cluster ids must skip (pass), not fail")
	}
}
