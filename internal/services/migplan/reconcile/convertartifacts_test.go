package reconcile

import (
	"testing"

	"github.com/goccy/go-yaml"
)

func dynamicRouteRaw() map[string]any {
	return map[string]any{
		"name":                         "migration-route",
		"mode":                         "dynamic",
		"endpoint":                     "bootstrap.gw.local:9595",
		"brokerIdentificationStrategy": map[string]any{"type": "host", "pattern": "broker$(nodeId).gw.local"},
		"streamingDomains": []any{
			map[string]any{"name": "msk", "bootstrapServerId": "MSK"},
			map[string]any{"name": "cc", "bootstrapServerId": "CC"},
		},
		"rules": map[string]any{"routing": map[string]any{"coordination": map[string]any{"group": "msk"}}},
		"security": map[string]any{"cluster": map[string]any{
			"msk": map[string]any{"auth": "passthrough"},
			"cc":  map[string]any{"auth": "passthrough"},
		}},
	}
}

func switchedRoute(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal switched route: %v", err)
	}
	route, ok := doc["route"].(map[string]any)
	if !ok {
		t.Fatalf("switched route must be wrapped as {route: …}, got: %s", raw)
	}
	return route
}

func TestBuildConvertedStaticRoute(t *testing.T) {
	raw := dynamicRouteRaw()

	b, err := BuildConvertedStaticRoute(raw, "cc", "CC")
	if err != nil {
		t.Fatal(err)
	}
	route := switchedRoute(t, b)

	if _, ok := route["streamingDomains"]; ok {
		t.Error("the static route must not keep the two-domain streamingDomains binding")
	}
	if _, ok := route["rules"]; ok {
		t.Error("the static route must not keep the rules tree")
	}
	sd, _ := route["streamingDomain"].(map[string]any)
	if sd["name"] != "cc" || sd["bootstrapServerId"] != "CC" {
		t.Errorf("streamingDomain = %v, want {name: cc, bootstrapServerId: CC}", sd)
	}
	if route["mode"] != "static" {
		t.Errorf("mode = %v, want static", route["mode"])
	}
	if route["endpoint"] != "bootstrap.gw.local:9595" {
		t.Errorf("endpoint = %v, want it unchanged", route["endpoint"])
	}
	if _, ok := route["brokerIdentificationStrategy"]; !ok {
		t.Error("brokerIdentificationStrategy must be kept")
	}
	cluster, _ := route["security"].(map[string]any)["cluster"].(map[string]any)
	if _, ok := cluster["cc"]; !ok {
		t.Error("security.cluster.cc must be kept")
	}
	if _, ok := raw["streamingDomains"]; !ok {
		t.Error("raw must not be modified")
	}
}

func TestBuildConvertedStaticRoute_AddsNoModeWhenTheRouteHasNone(t *testing.T) {
	raw := dynamicRouteRaw()
	delete(raw, "mode")

	b, err := BuildConvertedStaticRoute(raw, "cc", "CC")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := switchedRoute(t, b)["mode"]; ok {
		t.Error("a route resolved structurally must stay structural: no mode key added")
	}
}

func TestTargetBinding(t *testing.T) {
	raw := dynamicRouteRaw()
	if id, ok := targetBinding(raw, "cc"); !ok || id != "CC" {
		t.Errorf("targetBinding(cc) = %q, %v, want CC, true", id, ok)
	}
	if _, ok := targetBinding(raw, "gcp"); ok {
		t.Error("targetBinding for an unbound domain must report false")
	}
	raw["streamingDomains"] = []any{map[string]any{"name": "cc"}}
	if _, ok := targetBinding(raw, "cc"); ok {
		t.Error("targetBinding with no bootstrapServerId must report false")
	}
}
