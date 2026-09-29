package reconcile

import (
	"strings"
	"testing"
)

// staticGateway is defined in preconditions_test.go, alongside dynGateway.

func TestStaticPreconditionsHappy(t *testing.T) {
	in := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}
	res, view, ok := CheckStaticPreconditions(in, staticGateway(), nil, "", ClusterIDs{})
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
	// Already bound to the target is NOT a precondition failure any more: it is a
	// valid done-state (the classifier lands the topics Unchanged), so a completed
	// static migration re-reconciles to a clean no-op instead of a refusal.
	// Preconditions pass and RoutesToTarget is correctly true.
	res, view, ok := CheckStaticPreconditions(in, gw, nil, "", ClusterIDs{})
	if !ok {
		t.Fatalf("an already-bound route must NOT fail preconditions (it is a valid done-state), got: %+v", res)
	}
	if !view.RoutesToTarget {
		t.Error("RoutesToTarget must be true: route is now bound to cc")
	}
}

// staticFenceCheck is the precondition that refuses a route fenced by someone
// other than kcp.
const staticFenceCheck = "route has no fence kcp didn't write"

// TestStaticPreconditionsKcpFenceIsAResume: a route carrying kcp's own fence is
// the state a resume finds after the fence step, so it passes.
func TestStaticPreconditionsKcpFenceIsAResume(t *testing.T) {
	gw := staticGateway()
	gw.Route.Raw["fence"] = map[string]any{"scope": "ALL", "errorCode": "BROKER_NOT_AVAILABLE"}
	in := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}

	if res, _, ok := CheckStaticPreconditions(in, gw, nil, "", ClusterIDs{}); !ok {
		t.Fatalf("a route fenced by kcp must pass (it is a resume), got: %+v", res)
	}
}

// TestStaticPreconditionsFenceKcpDidNotWrite: a route that already carries any
// fence other than kcp's exact one is refused, because the switch and a
// rollback would remove it. scope NONE is no fence.
func TestStaticPreconditionsFenceKcpDidNotWrite(t *testing.T) {
	cases := []struct {
		name    string
		fence   map[string]any
		refused bool
	}{
		{"custom errorMessage", map[string]any{"scope": "ALL", "errorMessage": "maintenance window: back 14:00"}, true},
		{"custom errorMessage beside the defaulted errorCode", map[string]any{"scope": "ALL", "errorCode": "BROKER_NOT_AVAILABLE", "errorMessage": "maintenance window: back 14:00"}, true},
		{"other errorCode", map[string]any{"scope": "ALL", "errorCode": "NOT_LEADER_OR_FOLLOWER"}, true},
		{"lower-case scope", map[string]any{"scope": "all", "errorCode": "BROKER_NOT_AVAILABLE"}, true},
		{"no errorCode", map[string]any{"scope": "ALL"}, true},
		{"kcp's fence", map[string]any{"scope": "ALL", "errorCode": "BROKER_NOT_AVAILABLE"}, false},
		{"kcp's fence read back with an empty errorMessage", map[string]any{"scope": "ALL", "errorCode": "BROKER_NOT_AVAILABLE", "errorMessage": ""}, false},
		{"scope NONE", map[string]any{"scope": "NONE"}, false},
		{"scope none, with the defaulted errorCode", map[string]any{"scope": "none", "errorCode": "BROKER_NOT_AVAILABLE"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := staticGateway()
			gw.Route.Raw["fence"] = tc.fence
			in := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}

			res, _, ok := CheckStaticPreconditions(in, gw, nil, "", ClusterIDs{})

			if ok == tc.refused {
				t.Fatalf("ok = %v, want %v for fence %v: %+v", ok, !tc.refused, tc.fence, res)
			}
			var check *PreconditionResult
			for i := range res {
				if res[i].Name == staticFenceCheck {
					check = &res[i]
				}
			}
			if check == nil {
				t.Fatalf("no %q result in %+v", staticFenceCheck, res)
			}
			if check.OK == tc.refused {
				t.Fatalf("%q OK = %v, want %v: %+v", staticFenceCheck, check.OK, !tc.refused, *check)
			}
			if tc.refused && !strings.Contains(check.Detail, "remove it") {
				t.Fatalf("the refusal must say what to do, got %q", check.Detail)
			}
		})
	}
}

func TestStaticPreconditionsNulledFenceCountsAsUnfenced(t *testing.T) {
	// fence: null (explicitly present but nil) must NOT trip the already-fenced
	// check — it means "being removed/unset," not "being set." Everything else
	// about staticGateway() already satisfies every other precondition, so this
	// must pass outright.
	gw := staticGateway()
	gw.Route.Raw["fence"] = nil
	in := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}

	if _, _, ok := CheckStaticPreconditions(in, gw, nil, "", ClusterIDs{}); !ok {
		t.Error("a nulled fence must count as unfenced and pass")
	}
}

// TestStaticPreconditionsSecretCheckSkipped proves a non-empty
// secretCheckSkipped produces a Skipped (OK: true, Skipped: true) result on
// "staged auth secrets exist" — never a plain pass — and does not refuse the
// run, regardless of what missingSecrets says (which should be empty
// whenever the check itself was skipped, since there was nothing to report).
func TestStaticPreconditionsSecretCheckSkipped(t *testing.T) {
	in := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}
	const reason = "no permission to read secrets in namespace \"confluent\""
	res, _, ok := CheckStaticPreconditions(in, staticGateway(), nil, reason, ClusterIDs{})
	if !ok {
		t.Fatalf("a skipped secret check must not refuse the run, got %+v", res)
	}
	found := false
	for _, r := range res {
		if r.Name == "staged auth secrets exist" {
			found = true
			if !r.OK || !r.Skipped {
				t.Errorf("staged auth secrets exist = %+v, want OK: true, Skipped: true", r)
			}
			if r.Detail != reason {
				t.Errorf("Detail = %q, want %q", r.Detail, reason)
			}
		}
	}
	if !found {
		t.Fatalf("expected a %q result, got %+v", "staged auth secrets exist", res)
	}
}

func TestStaticPreconditionsFailures(t *testing.T) {
	base := ReconcileInput{Route: "migration-route", TargetDomain: "cc"}

	// target domain not declared
	if _, _, ok := CheckStaticPreconditions(ReconcileInput{Route: "migration-route", TargetDomain: "gcp"}, staticGateway(), nil, "", ClusterIDs{}); ok {
		t.Error("undeclared target domain must fail")
	}
	// route not found
	if _, _, ok := CheckStaticPreconditions(ReconcileInput{Route: "nope", TargetDomain: "cc"}, staticGateway(), nil, "", ClusterIDs{}); ok {
		t.Error("missing route must fail")
	}
	// no staged auth block for target
	gw := staticGateway()
	delete(gw.RawObj["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any), "security")
	if _, _, ok := CheckStaticPreconditions(base, gw, nil, "", ClusterIDs{}); ok {
		t.Error("missing staged auth block must fail")
	}
	// missing secret
	if _, _, ok := CheckStaticPreconditions(base, staticGateway(), []string{"cc-sasl-secret"}, "", ClusterIDs{}); ok {
		t.Error("a reported-missing secret must fail")
	}
	// route mode is dynamic, not static
	dyn := staticGateway()
	dyn.Route.Mode = "dynamic"
	if _, _, ok := CheckStaticPreconditions(base, dyn, nil, "", ClusterIDs{}); ok {
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
