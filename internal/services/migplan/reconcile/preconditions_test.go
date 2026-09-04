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
	res, view, ok := CheckPreconditions(in, dynGateway(), false)
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
	if _, _, ok := CheckPreconditions(ReconcileInput{Route: "migration-route", TargetDomain: "gcp"}, dynGateway(), false); ok {
		t.Error("unbound target domain must fail")
	}
	// offset sync enabled
	if _, _, ok := CheckPreconditions(base, dynGateway(), true); ok {
		t.Error("offset sync enabled must fail")
	}
	// coordination not on source
	gw := dynGateway()
	gw.Route.Rules["routing"].(map[string]any)["coordination"].(map[string]any)["group"] = "cc"
	if _, _, ok := CheckPreconditions(base, gw, false); ok {
		t.Error("coordination not on source must fail")
	}
	// static route
	gw2 := dynGateway()
	gw2.Route.Mode = "static"
	if _, _, ok := CheckPreconditions(base, gw2, false); ok {
		t.Error("static route must fail (dynamic-only engine)")
	}
	// three bound domains
	gw3 := dynGateway()
	gw3.Route.BoundDomains = []string{"msk", "cc", "extra"}
	if _, _, ok := CheckPreconditions(base, gw3, false); ok {
		t.Error("more than two bound domains must fail")
	}
}
