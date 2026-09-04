package migplan

import (
	"context"
	"errors"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

type fakeGateway struct {
	gw  *reconcile.GatewayConfig
	err error
}

func (f *fakeGateway) Load(context.Context) (*reconcile.GatewayConfig, error) { return f.gw, f.err }

type fakeLister struct {
	topics []string
	err    error
}

func (f *fakeLister) ListTopics(context.Context) ([]string, error) { return f.topics, f.err }

type fakeLink struct {
	ls  *LinkStatus
	err error
}

func (f *fakeLink) LinkStatus(context.Context) (*LinkStatus, error) { return f.ls, f.err }

func dynGatewayConfig() *reconcile.GatewayConfig {
	return &reconcile.GatewayConfig{Route: &reconcile.RouteConfig{
		Name:         "migration-route",
		Mode:         "dynamic",
		BoundDomains: []string{"msk", "cc"},
		Rules: map[string]any{"routing": map[string]any{
			"coordination": map[string]any{"group": "msk"},
			"default":      "msk",
		}},
	}}
}

func TestEngineRunHappyPath(t *testing.T) {
	eng := NewReconciliationEngine(
		&fakeGateway{gw: dynGatewayConfig()},
		&fakeLister{topics: []string{"orders"}}, // source
		&fakeLister{topics: []string{"orders"}}, // target
		&fakeLink{ls: &LinkStatus{
			OffsetSyncEnabled: false,
			Mirrors:           map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive},
		}},
	)
	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Report.Refused() {
		t.Fatalf("expected success, refused: %+v", plan.Report)
	}
	if plan.Artifacts == nil || len(plan.Artifacts.Topics) != 1 {
		t.Fatalf("expected 1 migratable topic, got %+v", plan.Artifacts)
	}
}

// TestEngineRunProviderErrorIsError proves that a failure in ANY of the four
// providers surfaces as a Go error (an I/O failure the caller must handle), never
// swallowed into a feasibility refusal — a refusal means "read everything, the
// plan is infeasible", which is a different outcome from "could not read".
func TestEngineRunProviderErrorIsError(t *testing.T) {
	boom := errors.New("boom")
	okLink := &fakeLink{ls: &LinkStatus{Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive}}}
	cases := []struct {
		name string
		eng  *ReconciliationEngine
	}{
		{"gateway", NewReconciliationEngine(&fakeGateway{err: boom}, &fakeLister{}, &fakeLister{}, okLink)},
		{"source", NewReconciliationEngine(&fakeGateway{gw: dynGatewayConfig()}, &fakeLister{err: boom}, &fakeLister{}, okLink)},
		{"target", NewReconciliationEngine(&fakeGateway{gw: dynGatewayConfig()}, &fakeLister{}, &fakeLister{err: boom}, okLink)},
		{"link", NewReconciliationEngine(&fakeGateway{gw: dynGatewayConfig()}, &fakeLister{}, &fakeLister{}, &fakeLink{err: boom})},
	}
	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.eng.Run(context.Background(), in); err == nil {
				t.Fatalf("a %s-provider error must be returned as a Go error, not swallowed into a plan", c.name)
			}
		})
	}
}
