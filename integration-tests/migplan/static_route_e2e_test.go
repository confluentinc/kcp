//go:build e2e

package migplan_e2e

import (
	"context"
	"reflect"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/goccy/go-yaml"
)

// TestStaticRouteHappyPathLive runs the engine end-to-end (real gateway-file
// parse + live source/target/link) against testdata/gateway-static-redundant-
// auth.yaml, a static (AAO) route that satisfies every CheckStaticPreconditions
// check, and asserts the static-route strategy's distinct artifact shape: a
// small {fence: {...}} block for the fence, and a whole {route: {...}} for the
// switch and the rollback (both remove the fence) — never the dynamic
// strategy's whole rules: block.
func TestStaticRouteHappyPathLive(t *testing.T) {
	eng := newLiveEngineFor(t, "testdata/gateway-static-redundant-auth.yaml", "migration-route", fakeSecretChecker{})
	in := reconcile.ReconcileInput{
		Topics:          []string{"team-a.orders", "team-a.payments", "billing-v2"},
		Route:           "migration-route",
		TargetDomain:    "cc",
		TargetClusterID: destClusterID,
	}

	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if plan.Report.Refused() {
		t.Fatalf("expected success, got refusal: preconditions=%+v failFast=%+v",
			plan.Report.Preconditions, plan.Report.FailFast)
	}
	if plan.Mode != "static" {
		t.Errorf("Plan.Mode = %q, want %q", plan.Mode, "static")
	}
	if plan.Artifacts == nil {
		t.Fatal("expected non-nil Artifacts")
	}

	wantTopics := []string{"billing-v2", "team-a.orders", "team-a.payments"} // sorted
	if !reflect.DeepEqual(plan.Artifacts.PromoteTopics, wantTopics) {
		t.Errorf("Artifacts.PromoteTopics = %v, want %v", plan.Artifacts.PromoteTopics, wantTopics)
	}

	fence := parseFragment(t, plan.Artifacts.FenceRules)
	fenceBlock, ok := fence["fence"].(map[string]any)
	if !ok {
		t.Fatalf("FenceRules must be a {fence: {...}} fragment, got:\n%s", plan.Artifacts.FenceRules)
	}
	if fenceBlock["scope"] != "ALL" {
		t.Errorf("fence.scope = %v, want ALL", fenceBlock["scope"])
	}
	if fenceBlock["errorCode"] != "BROKER_NOT_AVAILABLE" {
		t.Errorf("fence.errorCode = %v, want BROKER_NOT_AVAILABLE", fenceBlock["errorCode"])
	}

	assertStaticRouteArtifacts(t, plan)
	if !plan.Artifacts.RollbackAllowed {
		t.Error("a fresh plan (nothing promoted) must allow a rollback")
	}
}

// TestStaticRouteAlreadyFencedLive: a resume against a static route an
// interrupted run already fenced (testdata/gateway-static-already-fenced.yaml).
// The switch and rollback routes are built from that fenced start-of-run
// route, and neither may carry its fence.
func TestStaticRouteAlreadyFencedLive(t *testing.T) {
	eng := newLiveEngineFor(t, "testdata/gateway-static-already-fenced.yaml", "migration-route", fakeSecretChecker{})
	in := reconcile.ReconcileInput{
		Topics:          []string{"team-a.orders", "team-a.payments", "billing-v2"},
		Route:           "migration-route",
		TargetDomain:    "cc",
		TargetClusterID: destClusterID,
	}

	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if plan.Report.Refused() || plan.Artifacts == nil {
		t.Fatalf("an already-fenced static route is a valid resume, got preconditions=%+v failFast=%+v",
			plan.Report.Preconditions, plan.Report.FailFast)
	}
	assertStaticRouteArtifacts(t, plan)
}

// assertStaticRouteArtifacts checks a static plan's switch and rollback routes:
// both whole routes without a fence, keeping the route's staged auth — the
// switch bound to cc/CC, the rollback to the source msk/MSK.
func assertStaticRouteArtifacts(t *testing.T, plan *reconcile.Plan) {
	t.Helper()
	for _, a := range []struct {
		name, domain, bootstrap string
		body                    []byte
	}{
		{"SwitchoverRules", "cc", "CC", plan.Artifacts.SwitchoverRules},
		{"RollbackFenceRules", "msk", "MSK", plan.Artifacts.RollbackFenceRules},
	} {
		route, ok := parseFragment(t, a.body)["route"].(map[string]any)
		if !ok {
			t.Fatalf("%s must be a whole {route: {...}}, got:\n%s", a.name, a.body)
		}
		if _, fenced := route["fence"]; fenced {
			t.Errorf("%s must carry no fence:\n%s", a.name, a.body)
		}
		if route["name"] != "migration-route" || route["security"] == nil {
			t.Errorf("%s must keep the route's name and staged auth:\n%s", a.name, a.body)
		}
		domain, _ := route["streamingDomain"].(map[string]any)
		if domain["name"] != a.domain || domain["bootstrapServerId"] != a.bootstrap {
			t.Errorf("%s streamingDomain = %v, want %s/%s", a.name, domain, a.domain, a.bootstrap)
		}
	}
}

// parseFragment unmarshals a static-route artifact fragment (never wrapped
// under rules:, unlike the dynamic strategy's whole-rules-block artifacts).
func parseFragment(t *testing.T, y []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(y, &doc); err != nil {
		t.Fatalf("unmarshal fragment: %v\n%s", err, y)
	}
	return doc
}
