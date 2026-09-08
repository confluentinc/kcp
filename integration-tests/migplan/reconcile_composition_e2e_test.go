//go:build e2e

package migplan_e2e

import (
	"context"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/goccy/go-yaml"
)

// TestReconcileResultReflectsManifest drives the in-code entry point
// migplan.Reconcile (the one the FSM calls) end-to-end against the docker env,
// with the gateway source injected as a static fixture (via WithGatewaySource)
// so the whole composition — manifest -> providers -> engine -> Result — is
// exercised without reaching Kubernetes, and asserts the Result faithfully
// encodes the operator's declared intent:
//
//   - Result.Topics is the resolved selector (the promote list);
//   - both YAML artifacts are wrapped under a top-level rules: key;
//   - the fence fences exactly the selected batch;
//   - the switchover routes exactly that batch to the manifest's target (cc);
//   - the operator's pre-existing gateway edits survive (preservation);
//   - Result.GatewayYAML carries the pulled gateway CR.
func TestReconcileResultReflectsManifest(t *testing.T) {
	g, err := manifest.LoadGatewayMigrationFile("testdata/manifest.yaml")
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	route := g.Spec.TopicGroup[0].Route

	res, err := migplan.Reconcile(context.Background(), g,
		migplan.WithGatewaySource(migplan.NewGatewayFile("testdata/gateway.yaml", route)),
		migplan.WithOutput(io.Discard),
	)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Refused {
		t.Fatalf("expected success, refused: %v", res.Reasons)
	}

	// the route the artifacts apply to, echoed from the manifest
	if res.Route != route {
		t.Errorf("Result.Route = %q, want %q", res.Route, route)
	}

	want := []string{"billing-v2", "team-a.orders", "team-a.payments"} // sorted
	if !reflect.DeepEqual(res.Topics, want) {
		t.Errorf("Result.Topics = %v, want %v", res.Topics, want)
	}

	// both artifacts wrapped under a top-level rules: key
	for _, y := range []struct{ name, body string }{{"FenceYAML", res.FenceYAML}, {"SwitchoverYAML", res.SwitchoverYAML}} {
		if !strings.HasPrefix(strings.TrimSpace(y.body), "rules:") {
			t.Errorf("%s must be wrapped under a top-level rules: key:\n%s", y.name, y.body)
		}
	}

	// fence: fences exactly the batch
	fence := parseRules(t, res.FenceYAML)
	if got := firstFencingTopics(t, fence); !reflect.DeepEqual(got, want) {
		t.Errorf("fence topics = %v, want %v", got, want)
	}

	// switchover: routes exactly the batch to the manifest's target (cc)
	sw := parseRules(t, res.SwitchoverYAML)
	domain, condTopics := firstSwitchCondition(t, sw)
	if domain != "cc" {
		t.Errorf("switchover routes to %q, want cc", domain)
	}
	if !reflect.DeepEqual(condTopics, want) {
		t.Errorf("switchover condition topics = %v, want %v", condTopics, want)
	}

	// PRESERVATION: the operator's pre-existing edits survive in both artifacts.
	for _, must := range []string{"TRANSACTION", "ops-audit"} {
		if !strings.Contains(res.FenceYAML, must) {
			t.Errorf("FenceYAML dropped the operator's fencing entry %q:\n%s", must, res.FenceYAML)
		}
		if !strings.Contains(res.SwitchoverYAML, must) {
			t.Errorf("SwitchoverYAML dropped the operator's fencing entry %q:\n%s", must, res.SwitchoverYAML)
		}
	}
	if !strings.Contains(res.SwitchoverYAML, "team-a.*") {
		t.Errorf("SwitchoverYAML dropped the operator's routing condition (team-a.*):\n%s", res.SwitchoverYAML)
	}

	// the pulled gateway CR is carried through for the caller's drift diff
	if !strings.Contains(res.GatewayYAML, "migration-route") {
		t.Errorf("Result.GatewayYAML should carry the pulled gateway CR:\n%s", res.GatewayYAML)
	}
}

// parseRules unmarshals a rules artifact and returns the subtree under its
// required top-level rules: key.
func parseRules(t *testing.T, y string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("unmarshal rules: %v", err)
	}
	rules, ok := doc["rules"].(map[string]any)
	if !ok {
		t.Fatalf("artifact must be wrapped under a top-level rules: key:\n%s", y)
	}
	return rules
}

func firstFencingTopics(t *testing.T, rules map[string]any) []string {
	t.Helper()
	fencing, ok := rules["fencing"].([]any)
	if !ok || len(fencing) == 0 {
		t.Fatalf("rules.fencing missing or empty: %+v", rules)
	}
	first, _ := fencing[0].(map[string]any)
	return sortedStrings(first["topics"])
}

func firstSwitchCondition(t *testing.T, rules map[string]any) (string, []string) {
	t.Helper()
	routing, _ := rules["routing"].(map[string]any)
	conds, ok := routing["conditions"].([]any)
	if !ok || len(conds) == 0 {
		t.Fatalf("rules.routing.conditions missing or empty: %+v", rules)
	}
	first, _ := conds[0].(map[string]any)
	domain, _ := first["streamingDomain"].(string)
	return domain, sortedStrings(first["topics"])
}

func sortedStrings(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
