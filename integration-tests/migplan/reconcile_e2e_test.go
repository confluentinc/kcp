//go:build e2e

package migplan_e2e

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/providers"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

// newLiveEngine wires the reconciliation engine to the live source + dest
// clusters and cluster link, plus the static gateway-config fixture.
func newLiveEngine(t *testing.T) *migplan.ReconciliationEngine {
	t.Helper()
	gw := providers.NewGatewayFile("testdata/gateway.yaml", "migration-route")
	source := newPlaintextLister(t, sourceBroker)
	target := newPlaintextLister(t, destBroker)

	svc := clusterlink.NewConfluentCloudService(http.DefaultClient)
	cfg := clusterlink.Config{
		RestEndpoint: destRESTEndpoint,
		ClusterID:    destClusterID,
		LinkName:     linkName,
		Topics:       []string{},
		Auth:         nil,
	}
	link := providers.NewClusterLinkStatus(svc, cfg, false)

	return migplan.NewReconciliationEngine(gw, source, target, link)
}

// TestEngineHappyPathLive runs the whole engine end-to-end against the live
// environment for the three ACTIVE-mirror topics and asserts they are all
// classified MIGRATABLE with the expected artifacts.
func TestEngineHappyPathLive(t *testing.T) {
	eng := newLiveEngine(t)
	in := reconcile.ReconcileInput{
		Topics:       []string{"team-a.orders", "team-a.payments", "billing-v2"},
		Route:        "migration-route",
		TargetDomain: "cc",
	}

	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if plan.Report.Refused() {
		t.Fatalf("expected success, got refusal: preconditions=%+v failFast=%+v",
			plan.Report.Preconditions, plan.Report.FailFast)
	}
	if len(plan.Report.Migratable) != 3 {
		t.Fatalf("expected 3 migratable, got %d: %+v", len(plan.Report.Migratable), plan.Report.Migratable)
	}
	for _, tv := range plan.Report.Migratable {
		t.Logf("migratable: %s (S=%s M=%s T=%s R=%s)", tv.Topic, tv.S, tv.M, tv.T, tv.R)
	}

	if plan.Artifacts == nil {
		t.Fatal("expected non-nil Artifacts")
	}
	wantTopics := []string{"billing-v2", "team-a.orders", "team-a.payments"} // sorted
	if !reflect.DeepEqual(plan.Artifacts.Topics, wantTopics) {
		t.Errorf("Artifacts.Topics = %v, want %v", plan.Artifacts.Topics, wantTopics)
	}

	fence := string(plan.Artifacts.FenceRules)
	switchover := string(plan.Artifacts.SwitchoverRules)
	t.Logf("fence-rules.yaml:\n%s", fence)
	t.Logf("switchover-rules.yaml:\n%s", switchover)

	// Both artifacts must be the whole `rules` subtree, wrapped under a
	// top-level rules: key (the hot-reloadable, KCP-patched subtree the
	// gateway consumes).
	for _, a := range []struct{ name, body string }{{"FenceRules", fence}, {"SwitchoverRules", switchover}} {
		if !strings.HasPrefix(strings.TrimSpace(a.body), "rules:") {
			t.Errorf("%s must be wrapped under a top-level rules: key:\n%s", a.name, a.body)
		}
	}

	for _, topic := range wantTopics {
		if !strings.Contains(fence, topic) {
			t.Errorf("FenceRules missing topic %q:\n%s", topic, fence)
		}
		if !strings.Contains(switchover, topic) {
			t.Errorf("SwitchoverRules missing topic %q:\n%s", topic, switchover)
		}
	}
	// The switchover must route the migrated topics to the target domain (cc).
	if !strings.Contains(switchover, "cc") {
		t.Errorf("SwitchoverRules does not route to target domain cc:\n%s", switchover)
	}
}

// TestEngineFailFastLive runs the engine for team-b.audit, which exists on the
// source but is deliberately NOT mirrored on the link, so the engine must
// refuse (F3) with nil artifacts and a fail-fast reason that references the
// cluster link.
func TestEngineFailFastLive(t *testing.T) {
	eng := newLiveEngine(t)
	in := reconcile.ReconcileInput{
		Topics:       []string{"team-b.audit"},
		Route:        "migration-route",
		TargetDomain: "cc",
	}

	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !plan.Report.Refused() {
		t.Fatalf("expected refusal for an un-mirrored topic, got success: %+v", plan.Report)
	}
	if plan.Artifacts != nil {
		t.Errorf("expected nil Artifacts on refusal, got %+v", plan.Artifacts)
	}
	if len(plan.Report.FailFast) != 1 {
		t.Fatalf("expected 1 fail-fast topic, got %d: %+v", len(plan.Report.FailFast), plan.Report.FailFast)
	}

	reason := plan.Report.FailFast[0].Reason
	t.Logf("fail-fast: %s -> %s", plan.Report.FailFast[0].Topic, reason)
	// reason reads: "<topic> is not on the cluster link"
	if !strings.Contains(reason, "cluster link") {
		t.Errorf("fail-fast reason should mention the cluster link, got %q", reason)
	}
}
