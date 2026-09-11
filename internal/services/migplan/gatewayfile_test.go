package migplan

import (
	"context"
	"reflect"
	"strings"
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

func TestFindRouteModeResolvesStructurallyWhenAbsent(t *testing.T) {
	// A route with a plural streamingDomains array but no explicit `mode` must
	// resolve to "dynamic" via the structural fallback (resolveModeStructurally)
	// — mirroring gateway.ResolveRouteMode's production behavior, since the real
	// Gateway CRD carries no mode field at all.
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
	if rc.Mode != "dynamic" {
		t.Errorf("mode = %q, want dynamic (structural fallback from plural streamingDomains)", rc.Mode)
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

func TestGatewayFile_Load_CleansServerManagedFields(t *testing.T) {
	// testdata fixture must carry managedFields/resourceVersion/uid/
	// creationTimestamp/generation under metadata, plus a top-level status —
	// add a new fixture file (e.g. testdata/gateway-dynamic-with-metadata.yaml)
	// cloning an existing dynamic fixture with these fields added, since
	// existing fixtures are presumably already clean.
	gw, err := NewGatewayFile("testdata/gateway-dynamic-with-metadata.yaml", "migration-route").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Contains(gw.RawYAML, "managedFields") || strings.Contains(gw.RawYAML, "resourceVersion") ||
		strings.Contains(gw.RawYAML, "creationTimestamp") {
		t.Fatalf("RawYAML must have server-managed metadata stripped, got: %s", gw.RawYAML)
	}
	if metadata, ok := gw.RawObj["metadata"].(map[string]any); ok {
		if _, has := metadata["uid"]; has {
			t.Fatal("RawObj must have metadata.uid stripped")
		}
	}
	if _, has := gw.RawObj["status"]; has {
		t.Fatal("RawObj must have top-level status stripped")
	}
	if gw.Route.Raw == nil {
		t.Fatal("Route.Raw must be populated — the route's own raw map, for static-mode reads")
	}
	if name, _ := gw.Route.Raw["name"].(string); name != "migration-route" {
		t.Fatalf("Route.Raw[\"name\"] = %q, want migration-route", name)
	}
}

func TestGatewayFile_Load_ResolvesStaticModeStructurally(t *testing.T) {
	// A route with no mode field and a singular streamingDomain (not
	// streamingDomains) must resolve to "static" — add
	// testdata/gateway-static-no-mode-field.yaml: a route with
	// streamingDomain: {name: msk, bootstrapServerId: x} and no mode: key.
	gw, err := NewGatewayFile("testdata/gateway-static-no-mode-field.yaml", "migration-route").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gw.Route.Mode != "static" {
		t.Fatalf("Mode = %q, want static (structural fallback)", gw.Route.Mode)
	}
}

func TestGatewayFile_Load_ResolvesDynamicModeStructurally(t *testing.T) {
	// A route with no mode field and a plural streamingDomains array must
	// resolve to "dynamic" — add testdata/gateway-dynamic-no-mode-field.yaml.
	gw, err := NewGatewayFile("testdata/gateway-dynamic-no-mode-field.yaml", "migration-route").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gw.Route.Mode != "dynamic" {
		t.Fatalf("Mode = %q, want dynamic (structural fallback)", gw.Route.Mode)
	}
}

func TestGatewayFile_Load_ModeFieldWinsWhenPresent(t *testing.T) {
	// testdata/gateway-dynamic.yaml already carries an explicit `mode: dynamic`
	// field per the existing fixture — confirm it still wins even though this
	// task adds a structural fallback (field-first, per the design doc).
	gw, err := NewGatewayFile("testdata/gateway-dynamic.yaml", "migration-route").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gw.Route.Mode != "dynamic" {
		t.Fatalf("Mode = %q, want dynamic (explicit field)", gw.Route.Mode)
	}
}
