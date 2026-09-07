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
