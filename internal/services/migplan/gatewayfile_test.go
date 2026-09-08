package migplan

import (
	"context"
	"reflect"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

func TestGatewayFileLoad(t *testing.T) {
	src := NewGatewayFile("testdata/gateway-dynamic.yaml", "migration-route")
	gw, err := src.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gw.Route == nil {
		t.Fatal("expected a route, got nil")
	}
	if gw.Route.Name != "migration-route" {
		t.Errorf("name = %q, want migration-route", gw.Route.Name)
	}
	if gw.Route.Mode != "dynamic" {
		t.Errorf("mode = %q, want dynamic", gw.Route.Mode)
	}
	if !reflect.DeepEqual(gw.Route.BoundDomains, []string{"msk", "cc"}) {
		t.Errorf("boundDomains = %v, want [msk cc]", gw.Route.BoundDomains)
	}
	// the extracted rules subtree must be parseable + projectable by the core
	rt, err := reconcile.ParseRules(gw.Route.Rules)
	if err != nil {
		t.Fatal(err)
	}
	if g := rt.CoordinationGroup(); g != "msk" {
		t.Errorf("coordination.group = %q, want msk", g)
	}
	if len(rt.Project().Conditions) != 1 {
		t.Errorf("expected 1 condition projected, got %d", len(rt.Project().Conditions))
	}
}

func TestFindRouteModeDefaultsToStaticWhenAbsent(t *testing.T) {
	// A route with streamingDomains but no explicit `mode` must resolve to
	// "static" — never inferred as dynamic — so a static route cannot slip
	// through the "route is dynamic" precondition.
	doc := map[string]any{
		"spec": map[string]any{
			"routes": []any{
				map[string]any{
					"name": "r",
					"streamingDomains": []any{
						map[string]any{"name": "msk"},
						map[string]any{"name": "cc"},
					},
				},
			},
		},
	}
	rc, err := findRoute(doc, "r")
	if err != nil {
		t.Fatal(err)
	}
	if rc.Mode != "static" {
		t.Errorf("mode = %q, want static (a missing mode must not be inferred as dynamic)", rc.Mode)
	}
	if !reflect.DeepEqual(rc.BoundDomains, []string{"msk", "cc"}) {
		t.Errorf("boundDomains = %v, want [msk cc]", rc.BoundDomains)
	}
}

func TestGatewayFileRouteNotFound(t *testing.T) {
	src := NewGatewayFile("testdata/gateway-dynamic.yaml", "no-such-route")
	if _, err := src.Load(context.Background()); err == nil {
		t.Fatal("expected an error for a missing route")
	}
}

func TestGatewayFileBadPath(t *testing.T) {
	src := NewGatewayFile("testdata/does-not-exist.yaml", "migration-route")
	if _, err := src.Load(context.Background()); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
