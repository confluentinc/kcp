package migplan

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// fakeCRReader is a stand-in for the gateway service: it returns canned CR bytes
// (or an error) and records what it was asked for.
type fakeCRReader struct {
	yaml         []byte
	err          error
	gotNamespace string
	gotName      string
}

func (f *fakeCRReader) GetGatewayYAML(_ context.Context, namespace, name string) ([]byte, error) {
	f.gotNamespace, f.gotName = namespace, name
	if f.err != nil {
		return nil, f.err
	}
	return f.yaml, nil
}

// TestGatewayLiveLoad drives GatewayLive against a realistic CR and asserts it
// reads the CR by the given namespace + name and resolves the named route with
// the same findRoute the file source uses.
func TestGatewayLiveLoad(t *testing.T) {
	cr, err := os.ReadFile("testdata/gateway-dynamic.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fake := &fakeCRReader{yaml: cr}

	gc, err := NewGatewayLive(fake, "gateway", "migration-gateway", "migration-route").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if fake.gotNamespace != "gateway" || fake.gotName != "migration-gateway" {
		t.Errorf("read CR %q/%q, want gateway/migration-gateway", fake.gotNamespace, fake.gotName)
	}
	if gc.Route == nil {
		t.Fatal("expected a resolved route")
	}
	if gc.Route.Name != "migration-route" || gc.Route.Mode != "dynamic" {
		t.Errorf("route = %q mode %q, want migration-route/dynamic", gc.Route.Name, gc.Route.Mode)
	}
	if !reflect.DeepEqual(gc.Route.BoundDomains, []string{"msk", "cc"}) {
		t.Errorf("boundDomains = %v, want [msk cc]", gc.Route.BoundDomains)
	}
	if gc.Route.Rules == nil {
		t.Error("expected the route's rules subtree to be carried through")
	}
	// RawYAML is the cleaned CR (fixture already has no server-managed fields
	// to strip, so cleaning is a no-op) — compare parsed content rather than
	// raw bytes, since marshalling can reformat without changing meaning.
	var gotDoc, wantDoc map[string]any
	if err := yaml.Unmarshal([]byte(gc.RawYAML), &gotDoc); err != nil {
		t.Fatalf("unmarshal RawYAML: %v", err)
	}
	if err := yaml.Unmarshal(cr, &wantDoc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		t.Errorf("RawYAML must round-trip the pulled CR's content:\ngot:\n%s\nwant (from fixture):\n%s", gc.RawYAML, cr)
	}
	if gc.RawObj == nil {
		t.Fatal("expected RawObj to be populated")
	}
}

// TestGatewayLiveLoadRouteNotFound: the CR is read fine but has no such route.
func TestGatewayLiveLoadRouteNotFound(t *testing.T) {
	cr, _ := os.ReadFile("testdata/gateway-dynamic.yaml")
	fake := &fakeCRReader{yaml: cr}

	_, err := NewGatewayLive(fake, "gateway", "migration-gateway", "no-such-route").Load(context.Background())
	if err == nil {
		t.Fatal("expected an error for a route absent from the CR")
	}
}

// TestGatewayLiveLoadReadError: a failure reading the CR from the cluster is an
// I/O error, surfaced (not swallowed).
func TestGatewayLiveLoadReadError(t *testing.T) {
	fake := &fakeCRReader{err: errors.New("connection refused")}

	_, err := NewGatewayLive(fake, "gateway", "migration-gateway", "migration-route").Load(context.Background())
	if err == nil {
		t.Fatal("expected the read error to surface")
	}
}

func TestGatewayLive_Load_CleansServerManagedFields(t *testing.T) {
	// testdata fixture must carry managedFields/resourceVersion/uid/
	// creationTimestamp/generation under metadata, plus a top-level status —
	// add a new fixture file (e.g. testdata/gateway-dynamic-with-metadata.yaml)
	// cloning an existing dynamic fixture with these fields added, since
	// existing fixtures are presumably already clean.
	cr, err := os.ReadFile("testdata/gateway-dynamic-with-metadata.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fake := &fakeCRReader{yaml: cr}

	gw, err := NewGatewayLive(fake, "gateway", "migration-gateway", "migration-route").Load(context.Background())
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

func TestGatewayLive_Load_ResolvesStaticModeStructurally(t *testing.T) {
	// A route with no mode field and a singular streamingDomain (not
	// streamingDomains) must resolve to "static" — add
	// testdata/gateway-static-no-mode-field.yaml: a route with
	// streamingDomain: {name: msk, bootstrapServerId: x} and no mode: key.
	cr, err := os.ReadFile("testdata/gateway-static-no-mode-field.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fake := &fakeCRReader{yaml: cr}

	gw, err := NewGatewayLive(fake, "gateway", "migration-gateway", "migration-route").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gw.Route.Mode != "static" {
		t.Fatalf("Mode = %q, want static (structural fallback)", gw.Route.Mode)
	}
}

func TestGatewayLive_Load_ResolvesDynamicModeStructurally(t *testing.T) {
	// A route with no mode field and a plural streamingDomains array must
	// resolve to "dynamic" — add testdata/gateway-dynamic-no-mode-field.yaml.
	cr, err := os.ReadFile("testdata/gateway-dynamic-no-mode-field.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fake := &fakeCRReader{yaml: cr}

	gw, err := NewGatewayLive(fake, "gateway", "migration-gateway", "migration-route").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gw.Route.Mode != "dynamic" {
		t.Fatalf("Mode = %q, want dynamic (structural fallback)", gw.Route.Mode)
	}
}

func TestGatewayLive_Load_ModeFieldWinsWhenPresent(t *testing.T) {
	// testdata/gateway-dynamic.yaml already carries an explicit `mode: dynamic`
	// field per the existing fixture — confirm it still wins even though this
	// task adds a structural fallback (field-first, per the design doc).
	cr, err := os.ReadFile("testdata/gateway-dynamic.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fake := &fakeCRReader{yaml: cr}

	gw, err := NewGatewayLive(fake, "gateway", "migration-gateway", "migration-route").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gw.Route.Mode != "dynamic" {
		t.Fatalf("Mode = %q, want dynamic (explicit field)", gw.Route.Mode)
	}
}
