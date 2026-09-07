package migplan

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
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
	// The whole pulled CR is carried through verbatim for a later drift diff.
	if !bytes.Equal([]byte(gc.RawYAML), cr) {
		t.Errorf("RawYAML must be the CR exactly as pulled:\ngot:\n%s", gc.RawYAML)
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
