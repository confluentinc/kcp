package migplan

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
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

// fakePodChecker returns a canned pod check result and records the CR bytes it
// was handed.
type fakePodChecker struct {
	result    gateway.PodSync
	err       error
	gotCR     []byte
	gotPort   int
	gotNS     string
	gotName   string
	callCount int
}

func (f *fakePodChecker) CheckPodSync(_ context.Context, namespace, name string, cr []byte, port int) (gateway.PodSync, error) {
	f.callCount++
	f.gotNS, f.gotName, f.gotCR, f.gotPort = namespace, name, cr, port
	return f.result, f.err
}

// rawGatewayCR is a live-shaped CR: the server-managed fields cleanGatewayDoc
// strips are present.
const rawGatewayCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
  generation: 7
  managedFields:
  - manager: kubectl
spec:
  routes:
  - name: migration-route
    mode: dynamic
    streamingDomains:
    - name: msk
status:
  observedGeneration: 7
`

func TestGatewayLivePodCheckSeesRawCR(t *testing.T) {
	checker := &fakePodChecker{result: gateway.PodSync{Verdict: gateway.PodsInSync}}
	live := NewGatewayLive(&fakeCRReader{yaml: []byte(rawGatewayCR)}, "gateway", "migration-gateway", "migration-route").
		WithPodCheck(checker, 9181)

	gc, err := live.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := string(checker.gotCR)
	if !strings.Contains(got, "observedGeneration") || !strings.Contains(got, "managedFields") {
		t.Errorf("checker received a cleaned CR, want the raw bytes:\n%s", got)
	}
	if strings.Contains(gc.RawYAML, "observedGeneration") || strings.Contains(gc.RawYAML, "managedFields") {
		t.Errorf("RawYAML still carries server-managed fields:\n%s", gc.RawYAML)
	}
	if checker.gotNS != "gateway" || checker.gotName != "migration-gateway" || checker.gotPort != 9181 {
		t.Errorf("checker called with %q/%q port %d, want gateway/migration-gateway port 9181", checker.gotNS, checker.gotName, checker.gotPort)
	}
}

func TestGatewayLivePodCheckVerdictMapped(t *testing.T) {
	cases := []struct {
		in   gateway.PodSyncVerdict
		want reconcile.SyncVerdict
	}{
		{gateway.PodsInSync, reconcile.SyncInSync},
		{gateway.PodsOutOfSync, reconcile.SyncOutOfSync},
		{gateway.PodsCannotTell, reconcile.SyncCannotTell},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			checker := &fakePodChecker{result: gateway.PodSync{Verdict: c.in, Detail: "the detail", Evidence: []string{"a", "b"}}}
			live := NewGatewayLive(&fakeCRReader{yaml: []byte(rawGatewayCR)}, "gateway", "migration-gateway", "migration-route").
				WithPodCheck(checker, 0)

			gc, err := live.Load(context.Background())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			want := reconcile.GatewaySync{Verdict: c.want, Detail: "the detail", Evidence: []string{"a", "b"}}
			if !reflect.DeepEqual(gc.Sync, want) {
				t.Errorf("Sync = %+v, want %+v", gc.Sync, want)
			}
		})
	}
}

func TestGatewayLivePodCheckErrorIsLoadError(t *testing.T) {
	boom := errors.New("pods forbidden")
	live := NewGatewayLive(&fakeCRReader{yaml: []byte(rawGatewayCR)}, "gateway", "migration-gateway", "migration-route").
		WithPodCheck(&fakePodChecker{err: boom}, 0)

	gc, err := live.Load(context.Background())

	if err == nil || gc != nil {
		t.Fatalf("Load = %v, %v, want a nil config and an error", gc, err)
	}
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), `checking gateway "migration-gateway" pods`) {
		t.Errorf("error = %q, want it to wrap the checker error and name the gateway", err.Error())
	}
}

func TestGatewayLiveWithoutPodCheckIsUnchecked(t *testing.T) {
	live := NewGatewayLive(&fakeCRReader{yaml: []byte(rawGatewayCR)}, "gateway", "migration-gateway", "migration-route")

	gc, err := live.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gc.Sync.Verdict != reconcile.SyncUnchecked {
		t.Errorf("Sync.Verdict = %v, want SyncUnchecked", gc.Sync.Verdict)
	}
}

func TestGatewayLiveLoad_Generation(t *testing.T) {
	cr := []byte(`apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
  generation: 9
spec:
  routes:
    - name: migration-route
      streamingDomains:
        - name: msk
        - name: cc
`)
	gw, err := NewGatewayLive(&fakeCRReader{yaml: cr}, "gateway", "migration-gateway", "migration-route").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gw.Generation != 9 {
		t.Errorf("Generation = %d, want 9", gw.Generation)
	}
	if strings.Contains(gw.RawYAML, "generation") {
		t.Errorf("RawYAML must still have generation stripped, got: %s", gw.RawYAML)
	}
}
