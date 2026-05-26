package lib_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/pkg/lib"
	"github.com/goccy/go-yaml"
)

// minimal but valid kcp-state.json with one MSK Provisioned cluster.
// Kept tiny on purpose — the underlying processing/sizing/plan logic
// has detailed coverage in internal/*; this suite asserts only that
// the façade wires inputs to outputs and produces parseable JSON +
// non-empty markdown.
const sampleStateJSON = `{
  "timestamp": "2026-05-01T00:00:00Z",
  "kcp_build_info": {"version": "", "commit": "", "date": ""},
  "msk_sources": {
    "regions": [{
      "name": "us-east-1",
      "clusters": [{
        "name": "demo-cluster",
        "arn": "arn:aws:kafka:us-east-1:111:cluster/demo/uuid",
        "region": "us-east-1"
      }]
    }]
  }
}`

func TestScanSummary_RoundTripsValidJSON(t *testing.T) {
	out, err := lib.ScanSummary([]byte(sampleStateJSON))
	if err != nil {
		t.Fatalf("ScanSummary: %v", err)
	}
	var processed map[string]any
	if err := json.Unmarshal(out, &processed); err != nil {
		t.Fatalf("ScanSummary returned invalid JSON: %v\nbody=%s", err, out)
	}
	if len(processed) == 0 {
		t.Fatal("ScanSummary returned empty object")
	}
}

func TestScanSummary_RejectsMalformedJSON(t *testing.T) {
	if _, err := lib.ScanSummary([]byte("{not-json")); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// With nil planInputs, PlanInputs in the reply must still be populated
// with the full default set — it's the same `resolved` struct that
// Build consumed, so a follow-up call echoing it back produces an
// identical plan and a UI can use the first call to discover defaults.
func TestGeneratePlan_NilInputsEchoesDefaults(t *testing.T) {
	res, err := lib.GeneratePlan([]byte(sampleStateJSON), nil)
	if err != nil {
		t.Fatalf("GeneratePlan: %v", err)
	}
	if len(res.JSON) == 0 {
		t.Fatal("GeneratePlan.JSON is empty")
	}
	if len(res.Markdown) == 0 {
		t.Fatal("GeneratePlan.Markdown is empty")
	}
	if len(res.PlanInputs) == 0 {
		t.Fatal("GeneratePlan.PlanInputs is empty")
	}
	var plan map[string]any
	if err := json.Unmarshal(res.JSON, &plan); err != nil {
		t.Fatalf("GeneratePlan.JSON is not valid JSON: %v", err)
	}
	if !strings.Contains(string(res.Markdown), "Migration Plan") {
		t.Fatalf("GeneratePlan.Markdown missing expected header; got first 200 bytes: %s", truncate(res.Markdown, 200))
	}
	var inputs map[string]any
	if err := yaml.Unmarshal(res.PlanInputs, &inputs); err != nil {
		t.Fatalf("GeneratePlan.PlanInputs is not valid YAML: %v", err)
	}
	// Spot-check three independent default keys; if any is missing the
	// resolver is leaking nil pointers and the UI can't render its form.
	for _, k := range []string{"sizing_percentile", "headroom_fraction", "target_cloud"} {
		if _, ok := inputs[k]; !ok {
			t.Fatalf("PlanInputs missing default key %q; keys: %v", k, keys(inputs))
		}
	}
	// The echo must match what Build consumed: the inputs in the plan
	// JSON's header must equal the standalone PlanInputs field. (json
	// and yaml decoders both produce map[string]any; numeric types may
	// differ — compare via fmt.Sprint to dodge int64/float64 noise.)
	planInputs, ok := plan["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("plan.inputs missing from plan JSON; top-level keys: %v", keys(plan))
	}
	for _, k := range []string{"sizing_percentile", "headroom_fraction", "target_cloud"} {
		gotPlan, gotInputs := fmt.Sprint(planInputs[k]), fmt.Sprint(inputs[k])
		if gotPlan != gotInputs {
			t.Fatalf("plan.inputs[%q] = %s, PlanInputs[%q] = %s (must match)", k, gotPlan, k, gotInputs)
		}
	}
}

// PlanInputs in the reply must echo back the caller's overrides AND
// include the default keys they didn't set, so a UI can use an empty
// initial call to discover the full input shape.
func TestGeneratePlan_PlanInputsEchoesOverridesAndDefaults(t *testing.T) {
	inputs := []byte("target_cloud: azure\n")
	res, err := lib.GeneratePlan([]byte(sampleStateJSON), inputs)
	if err != nil {
		t.Fatalf("GeneratePlan: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(res.PlanInputs, &got); err != nil {
		t.Fatalf("PlanInputs is not valid YAML: %v", err)
	}
	if got["target_cloud"] != "azure" {
		t.Fatalf("PlanInputs.target_cloud = %v, want azure", got["target_cloud"])
	}
	// At least one default key the caller didn't set must be present —
	// the whole point of the echo is to surface defaults to the UI.
	if _, ok := got["sizing_percentile"]; !ok {
		t.Fatalf("PlanInputs missing default key sizing_percentile; keys: %v", keys(got))
	}
	// PlanInputsResolved carries a `Raw *PlanInputs` runtime helper. It
	// must not surface in the YAML echo — the echo is supposed to match
	// the flat plan-inputs.yaml shape a user edits, not the internal
	// resolved struct.
	if _, ok := got["raw"]; ok {
		t.Fatalf("PlanInputs YAML must not contain `raw` wrapper; keys: %v", keys(got))
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestGeneratePlan_AcceptsYAMLPlanInputs(t *testing.T) {
	inputs := []byte("target_cloud: azure\nheadroom_fraction: 0.4\n")
	res, err := lib.GeneratePlan([]byte(sampleStateJSON), inputs)
	if err != nil {
		t.Fatalf("GeneratePlan with YAML inputs: %v", err)
	}
	assertPlanInputsContains(t, res.PlanInputs, map[string]any{
		"target_cloud":      "azure",
		"headroom_fraction": 0.4,
	})
}

func assertPlanInputsContains(t *testing.T, planInputs []byte, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := yaml.Unmarshal(planInputs, &got); err != nil {
		t.Fatalf("PlanInputs is not valid YAML: %v", err)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("PlanInputs[%q] = %v, want %v", k, got[k], v)
		}
	}
}

func TestGeneratePlan_RejectsMalformedPlanInputs(t *testing.T) {
	if _, err := lib.GeneratePlan([]byte(sampleStateJSON), []byte(":\n  - not")); err == nil {
		t.Fatal("expected error for malformed plan-inputs")
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
