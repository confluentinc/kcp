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
// small {fence: {...}} / {streamingDomain: {...}} fragment each — never the
// dynamic strategy's whole rules: block.
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
	if !reflect.DeepEqual(plan.Artifacts.Topics, wantTopics) {
		t.Errorf("Artifacts.Topics = %v, want %v", plan.Artifacts.Topics, wantTopics)
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

	sw := parseFragment(t, plan.Artifacts.SwitchoverRules)
	swBlock, ok := sw["streamingDomain"].(map[string]any)
	if !ok {
		t.Fatalf("SwitchoverRules must be a {streamingDomain: {...}} fragment, got:\n%s", plan.Artifacts.SwitchoverRules)
	}
	if swBlock["name"] != "cc" {
		t.Errorf("streamingDomain.name = %v, want cc", swBlock["name"])
	}
	if swBlock["bootstrapServerId"] != "CC" {
		t.Errorf("streamingDomain.bootstrapServerId = %v, want CC", swBlock["bootstrapServerId"])
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
