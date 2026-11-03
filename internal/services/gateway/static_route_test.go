package gateway

import (
	"strings"
	"testing"
)

func baseStaticCR(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"spec": map[string]any{
			"routes": []any{
				map[string]any{
					"name":            "migration-route",
					"streamingDomain": map[string]any{"name": "msk", "bootstrapServerId": "msk-bootstrap"},
				},
			},
		},
	}
}

func TestReplaceRouteFenceObj(t *testing.T) {
	obj := baseStaticCR(t)
	fragment := []byte("fence:\n  scope: ALL\n  errorCode: BROKER_NOT_AVAILABLE\n")
	out, err := ReplaceRouteFenceObj(obj, "migration-route", fragment)
	if err != nil {
		t.Fatalf("ReplaceRouteFenceObj: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "fence:") || !strings.Contains(s, "scope: ALL") {
		t.Fatalf("output = %q, want a spliced fence block", s)
	}
}

func TestReplaceRouteFenceObj_RouteNotFound(t *testing.T) {
	obj := baseStaticCR(t)
	fragment := []byte("fence:\n  scope: ALL\n  errorCode: BROKER_NOT_AVAILABLE\n")
	if _, err := ReplaceRouteFenceObj(obj, "nope", fragment); err == nil {
		t.Fatal("expected an error for a route not present in spec.routes")
	}
}

func TestReplaceRouteFenceObj_FragmentMissingFenceKey(t *testing.T) {
	obj := baseStaticCR(t)
	if _, err := ReplaceRouteFenceObj(obj, "migration-route", []byte("notfence: {}\n")); err == nil {
		t.Fatal("expected an error when the fragment has no top-level fence key")
	}
}

func TestReplaceRouteObj(t *testing.T) {
	obj := baseStaticCR(t)
	obj["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)["fence"] = map[string]any{"scope": "ALL"}
	artifact := []byte("route:\n  name: migration-route\n  streamingDomain:\n    name: cc\n    bootstrapServerId: cc-bootstrap\n")
	out, err := ReplaceRouteObj(obj, "migration-route", artifact)
	if err != nil {
		t.Fatalf("ReplaceRouteObj: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "name: cc") || !strings.Contains(s, "bootstrapServerId: cc-bootstrap") {
		t.Fatalf("output = %q, want the artifact's route in place", s)
	}
	if strings.Contains(s, "fence") {
		t.Fatalf("output = %q, want the whole route replaced, so the old fence is gone", s)
	}
}

func TestReplaceRouteObj_RouteNotFound(t *testing.T) {
	obj := baseStaticCR(t)
	if _, err := ReplaceRouteObj(obj, "nope", []byte("route:\n  name: nope\n")); err == nil {
		t.Fatal("expected an error for a route not present in spec.routes")
	}
}

func TestReplaceRouteObj_ArtifactMissingKey(t *testing.T) {
	obj := baseStaticCR(t)
	if _, err := ReplaceRouteObj(obj, "migration-route", []byte("notit: {}\n")); err == nil {
		t.Fatal("expected an error when the artifact has no top-level route key")
	}
}
