//go:build e2e

package migplan_e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

// TestGatewayPermutationsLive drives the whole engine (real gateway-file parse +
// live source/target/link) against a range of gateway-config shapes, asserting
// each precondition gate fires end-to-end — not merely in the pure unit tests.
// One row passes (the rich fixture); the rest each violate exactly one
// precondition and must refuse with no artifacts.
func TestGatewayPermutationsLive(t *testing.T) {
	batch := []string{"team-a.orders", "team-a.payments", "billing-v2"}

	cases := []struct {
		name         string
		gatewayFile  string
		targetDomain string
		wantRefuse   bool
		failPrecond  string // substring of the precondition name that must be failing
	}{
		{"rich dynamic route passes", "testdata/gateway.yaml", "cc", false, ""},
		{"static route refused", "testdata/gateway-static.yaml", "cc", true, "route is dynamic"},
		{"three bound domains refused", "testdata/gateway-three-domains.yaml", "cc", true, "binds exactly two"},
		{"coordination on target refused", "testdata/gateway-coord-on-target.yaml", "cc", true, "coordination.group pinned on source"},
		{"unbound target domain refused", "testdata/gateway.yaml", "gcp", true, "target domain is bound"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng := newLiveEngineFor(t, c.gatewayFile, "migration-route")
			in := reconcile.ReconcileInput{Topics: batch, Route: "migration-route", TargetDomain: c.targetDomain}

			plan, err := eng.Run(context.Background(), in)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if plan.Report.Refused() != c.wantRefuse {
				t.Fatalf("Refused()=%v, want %v; preconditions=%+v failFast=%+v",
					plan.Report.Refused(), c.wantRefuse, plan.Report.Preconditions, plan.Report.FailFast)
			}
			if c.wantRefuse {
				if plan.Artifacts != nil {
					t.Errorf("a refused run must emit no artifacts, got %+v", plan.Artifacts)
				}
				if !hasFailedPrecondition(plan.Report, c.failPrecond) {
					t.Errorf("expected a failed precondition matching %q, got %+v", c.failPrecond, plan.Report.Preconditions)
				}
			}
		})
	}
}

// TestOffsetSyncEnabledRefusesLive flips the REAL cluster link's
// consumer.offset.sync.enable to true and asserts the engine refuses on the
// "consumer offset sync disabled on link" precondition — proving that gate is
// driven by the live link config, not a supplied flag. Resets the link on exit.
func TestOffsetSyncEnabledRefusesLive(t *testing.T) {
	setOffsetSync(t, "true")
	t.Cleanup(func() { setOffsetSync(t, "false") })

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
	if !plan.Report.Refused() {
		t.Fatalf("offset sync enabled on the link must refuse; preconditions=%+v", plan.Report.Preconditions)
	}
	if plan.Artifacts != nil {
		t.Errorf("a refused run must emit no artifacts")
	}
	if !hasFailedPrecondition(plan.Report, "consumer offset sync disabled on link") {
		t.Errorf("expected the offset-sync precondition to fail, got %+v", plan.Report.Preconditions)
	}
}

func hasFailedPrecondition(r reconcile.Report, nameSubstr string) bool {
	for _, p := range r.Preconditions {
		if !p.OK && strings.Contains(p.Name, nameSubstr) {
			return true
		}
	}
	return false
}

// setOffsetSync PUTs the cluster link's consumer.offset.sync.enable to value.
func setOffsetSync(t *testing.T, value string) {
	t.Helper()
	url := destRESTEndpoint + "/kafka/v3/clusters/" + destClusterID +
		"/links/" + linkName + "/configs/consumer.offset.sync.enable"
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(`{"value":"`+value+`"}`))
	if err != nil {
		t.Fatalf("build offset-sync request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("set offset sync %s: %v", value, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("set offset sync %s: HTTP %d", value, resp.StatusCode)
	}
}
