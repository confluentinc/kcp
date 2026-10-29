package reconcile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestBuildFenceFragment(t *testing.T) {
	b, err := BuildFenceFragment()
	if err != nil {
		t.Fatalf("BuildFenceFragment: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "fence:") || !strings.Contains(s, "scope: ALL") || !strings.Contains(s, "errorCode: BROKER_NOT_AVAILABLE") {
		t.Fatalf("fragment = %q, want a fence block with scope ALL and errorCode BROKER_NOT_AVAILABLE", s)
	}
}

// fencedStaticRoute is a static route as a resumed run pulls it: bound to the
// source domain, carrying staged auth, and already fenced by an earlier run.
func fencedStaticRoute() map[string]any {
	return map[string]any{
		"name":            "migration-route",
		"endpoint":        "gateway:9595",
		"streamingDomain": map[string]any{"name": "msk", "bootstrapServerId": "msk-bootstrap"},
		"security":        map[string]any{"cluster": map[string]any{"cc": map[string]any{"secretStore": "vault"}}},
		"fence":           map[string]any{"scope": "ALL", "errorCode": "BROKER_NOT_AVAILABLE"},
	}
}

// routeArtifact parses a {route: …} artifact back into the route map.
func routeArtifact(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal route artifact: %v\n%s", err, b)
	}
	route, ok := doc["route"].(map[string]any)
	if !ok {
		t.Fatalf("artifact has no top-level route map:\n%s", b)
	}
	return route
}

func TestBuildSwitchoverRoute(t *testing.T) {
	raw := fencedStaticRoute()
	b, err := BuildSwitchoverRoute(raw, "cc", "cc-bootstrap")
	if err != nil {
		t.Fatalf("BuildSwitchoverRoute: %v", err)
	}
	route := routeArtifact(t, b)

	if _, fenced := route["fence"]; fenced {
		t.Fatalf("the switched route must carry no fence:\n%s", b)
	}
	want := map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}
	if got := route["streamingDomain"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("streamingDomain = %v, want %v", got, want)
	}
	if route["name"] != "migration-route" || route["endpoint"] != "gateway:9595" || route["security"] == nil {
		t.Fatalf("the switched route must keep every other field of the start-of-run route:\n%s", b)
	}
	if _, stillFenced := raw["fence"]; !stillFenced || raw["streamingDomain"].(map[string]any)["name"] != "msk" {
		t.Fatal("BuildSwitchoverRoute must not modify the route it is given")
	}
}

func TestBuildRollbackFenceRoute(t *testing.T) {
	raw := fencedStaticRoute()
	b, err := BuildRollbackFenceRoute(raw)
	if err != nil {
		t.Fatalf("BuildRollbackFenceRoute: %v", err)
	}
	route := routeArtifact(t, b)

	if _, fenced := route["fence"]; fenced {
		t.Fatalf("the rolled-back route must carry no fence:\n%s", b)
	}
	want := map[string]any{"name": "msk", "bootstrapServerId": "msk-bootstrap"}
	if got := route["streamingDomain"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("streamingDomain = %v, want the source binding %v", got, want)
	}
	if route["name"] != "migration-route" || route["endpoint"] != "gateway:9595" || route["security"] == nil {
		t.Fatalf("the rolled-back route must keep every other field of the start-of-run route:\n%s", b)
	}
	if _, stillFenced := raw["fence"]; !stillFenced {
		t.Fatal("BuildRollbackFenceRoute must not modify the route it is given")
	}
}
