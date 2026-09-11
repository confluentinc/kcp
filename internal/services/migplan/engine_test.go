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
	topics    []string
	clusterID string
	err       error
}

func (f *fakeLister) ListTopics(context.Context) ([]string, error) { return f.topics, f.err }
func (f *fakeLister) ClusterID(context.Context) (string, error)    { return f.clusterID, f.err }

type fakeLink struct {
	ls  *LinkStatus
	err error
}

func (f *fakeLink) LinkStatus(context.Context) (*LinkStatus, error) { return f.ls, f.err }

type fakeSecretChecker struct {
	missing    []string
	calledWith []string
}

func (f *fakeSecretChecker) MissingSecrets(_ context.Context, names []string) ([]string, error) {
	f.calledWith = names
	return f.missing, nil
}

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

// staticGatewayConfig mirrors reconcile's own package-scoped staticGateway()
// test helper (internal/services/migplan/reconcile/preconditions_test.go) —
// duplicated here rather than imported since that helper is unexported and
// this package's tests exercise the engine, not the pure core directly. The
// route is bound to "msk" (not the target "cc"), and stages redundant auth
// for "cc" referencing a single Secret, "cc-sasl-secret" — the one live
// secret-existence check a static-mode Run must make.
func staticGatewayConfig() *reconcile.GatewayConfig {
	route := map[string]any{
		"name":            "migration-route",
		"streamingDomain": map[string]any{"name": "msk", "bootstrapServerId": "msk-bootstrap"},
		"security": map[string]any{
			"cluster": map[string]any{
				"cc": map[string]any{
					"secretStore": "vault",
					"authentication": map[string]any{
						"sasl": map[string]any{"secretRef": "cc-sasl-secret"},
					},
				},
			},
		},
	}
	obj := map[string]any{
		"spec": map[string]any{
			"streamingDomains": []any{
				map[string]any{
					"name":         "msk",
					"kafkaCluster": map[string]any{"bootstrapServers": []any{map[string]any{"id": "msk-bootstrap"}}},
				},
				map[string]any{
					"name":         "cc",
					"kafkaCluster": map[string]any{"bootstrapServers": []any{map[string]any{"id": "cc-bootstrap"}}},
				},
			},
			"routes": []any{route},
		},
	}
	return &reconcile.GatewayConfig{
		Route:  &reconcile.RouteConfig{Name: "migration-route", Mode: "static", BoundDomains: []string{"msk"}, Raw: route},
		RawObj: obj,
	}
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
		&fakeSecretChecker{},
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
		{"gateway", NewReconciliationEngine(&fakeGateway{err: boom}, &fakeLister{}, &fakeLister{}, okLink, &fakeSecretChecker{})},
		{"source", NewReconciliationEngine(&fakeGateway{gw: dynGatewayConfig()}, &fakeLister{err: boom}, &fakeLister{}, okLink, &fakeSecretChecker{})},
		{"target", NewReconciliationEngine(&fakeGateway{gw: dynGatewayConfig()}, &fakeLister{}, &fakeLister{err: boom}, okLink, &fakeSecretChecker{})},
		{"link", NewReconciliationEngine(&fakeGateway{gw: dynGatewayConfig()}, &fakeLister{}, &fakeLister{}, &fakeLink{err: boom}, &fakeSecretChecker{})},
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

// TestReconciliationEngine_Run_StaticMode_ChecksOnlyTargetSecret proves that a
// static-mode route causes Run to resolve exactly the target's staged secret
// name(s) via reconcile.ResolveStagedSecretNames and invoke the secrets
// provider with exactly that list — not an empty/whole-CR scan — and that the
// provider's result flows through to the returned Plan (via Reconcile's
// missingSecrets arg, surfaced as a precondition failure when non-empty).
func TestReconciliationEngine_Run_StaticMode_ChecksOnlyTargetSecret(t *testing.T) {
	secrets := &fakeSecretChecker{missing: nil}
	eng := NewReconciliationEngine(
		&fakeGateway{gw: staticGatewayConfig()},
		&fakeLister{topics: []string{"orders"}}, // source
		&fakeLister{topics: []string{"orders"}}, // target
		&fakeLink{ls: &LinkStatus{
			Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive},
		}},
		secrets,
	)
	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "static" {
		t.Fatalf("plan.Mode = %q, want %q", plan.Mode, "static")
	}
	if len(secrets.calledWith) != 1 || secrets.calledWith[0] != "cc-sasl-secret" {
		t.Fatalf("secrets.calledWith = %v, want [cc-sasl-secret]", secrets.calledWith)
	}

	// Now prove a reported-missing secret surfaces as a precondition failure.
	secretsMissing := &fakeSecretChecker{missing: []string{"cc-sasl-secret"}}
	eng2 := NewReconciliationEngine(
		&fakeGateway{gw: staticGatewayConfig()},
		&fakeLister{topics: []string{"orders"}},
		&fakeLister{topics: []string{"orders"}},
		&fakeLink{ls: &LinkStatus{
			Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive},
		}},
		secretsMissing,
	)
	plan2, err := eng2.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.Report.Refused() {
		t.Fatalf("expected refusal when the secrets provider reports a missing secret, got %+v", plan2.Report)
	}
}

// TestReconciliationEngine_Run_DynamicMode_NeverCallsSecretsProvider proves
// that a dynamic-mode gateway never invokes the secrets provider at all — a
// dynamic-route migration has no redundant-auth concept, so no live secret
// check should ever be made for one.
func TestReconciliationEngine_Run_DynamicMode_NeverCallsSecretsProvider(t *testing.T) {
	secrets := &fakeSecretChecker{}
	eng := NewReconciliationEngine(
		&fakeGateway{gw: dynGatewayConfig()},
		&fakeLister{topics: []string{"orders"}},
		&fakeLister{topics: []string{"orders"}},
		&fakeLink{ls: &LinkStatus{
			Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive},
		}},
		secrets,
	)
	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	if _, err := eng.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if secrets.calledWith != nil {
		t.Fatalf("secrets provider must never be called for a dynamic-mode route, calledWith=%v", secrets.calledWith)
	}
}
