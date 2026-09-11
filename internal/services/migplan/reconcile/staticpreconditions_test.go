package reconcile

import "testing"

// staticGateway is defined in preconditions_test.go, alongside dynGateway.

func TestStaticPreconditionsHappy(t *testing.T) {
	in := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}
	res, view, ok := CheckStaticPreconditions(in, staticGateway(), nil, ClusterIDs{})
	if !ok {
		t.Fatalf("expected pass, got %+v", res)
	}
	if view.BootstrapServerID != "cc-bootstrap" {
		t.Errorf("BootstrapServerID = %q, want cc-bootstrap", view.BootstrapServerID)
	}
	if view.RoutesToTarget {
		t.Error("RoutesToTarget must be false: route is currently bound to msk, not cc")
	}
}

func TestStaticPreconditionsRoutesToTargetWhenAlreadyBound(t *testing.T) {
	gw := staticGateway()
	route := gw.RawObj["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)
	route["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}
	in := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}
	// Already bound to the target — this specific precondition ("not already
	// bound") must now fail even though RoutesToTarget itself is correctly true.
	res, view, ok := CheckStaticPreconditions(in, gw, nil, ClusterIDs{})
	if ok {
		t.Fatalf("expected refusal (already bound to target), got pass: %+v", res)
	}
	if !view.RoutesToTarget {
		t.Error("RoutesToTarget must be true: route is now bound to cc")
	}
}

func TestStaticPreconditionsFailures(t *testing.T) {
	base := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}

	// target domain not declared
	if _, _, ok := CheckStaticPreconditions(ReconcileInput{Route: "migration-route", TargetDomain: "gcp"}, staticGateway(), nil, ClusterIDs{}); ok {
		t.Error("undeclared target domain must fail")
	}
	// route not found
	if _, _, ok := CheckStaticPreconditions(ReconcileInput{Route: "nope", TargetDomain: "cc"}, staticGateway(), nil, ClusterIDs{}); ok {
		t.Error("missing route must fail")
	}
	// no staged auth block for target
	gw := staticGateway()
	delete(gw.RawObj["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any), "security")
	if _, _, ok := CheckStaticPreconditions(base, gw, nil, ClusterIDs{}); ok {
		t.Error("missing staged auth block must fail")
	}
	// missing secret
	if _, _, ok := CheckStaticPreconditions(base, staticGateway(), []string{"cc-sasl-secret"}, ClusterIDs{}); ok {
		t.Error("a reported-missing secret must fail")
	}
	// route mode is dynamic, not static
	dyn := staticGateway()
	dyn.Route.Mode = "dynamic"
	if _, _, ok := CheckStaticPreconditions(base, dyn, nil, ClusterIDs{}); ok {
		t.Error("a dynamic-mode route must fail static preconditions")
	}
}

func TestResolveStagedSecretNames(t *testing.T) {
	names := ResolveStagedSecretNames(staticGateway(), "cc")
	if len(names) != 1 || names[0] != "cc-sasl-secret" {
		t.Fatalf("names = %v, want [cc-sasl-secret]", names)
	}
	if got := ResolveStagedSecretNames(staticGateway(), "gcp"); got != nil {
		t.Fatalf("names for an undeclared/unstaged domain = %v, want nil", got)
	}
}
