package lib_test

import (
	"encoding/json"
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

// With nil planInputs, GeneratePlan still returns a full result: parseable
// plan JSON, markdown with the plan header, and a scaffolded plan-inputs.yaml
// (a `clusters:` block a UI can present for editing).
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
	if _, ok := plan["clusters"]; !ok {
		t.Fatalf("plan JSON missing `clusters`; top-level keys: %v", keys(plan))
	}
	if !strings.Contains(string(res.Markdown), "Migration Plan") {
		t.Fatalf("GeneratePlan.Markdown missing expected header; got first 200 bytes: %s", truncate(res.Markdown, 200))
	}
	var inputs map[string]any
	if err := yaml.Unmarshal(res.PlanInputs, &inputs); err != nil {
		t.Fatalf("GeneratePlan.PlanInputs is not valid YAML: %v", err)
	}
	if _, ok := inputs["clusters"]; !ok {
		t.Fatalf("PlanInputs missing `clusters` block; keys: %v", keys(inputs))
	}
	// A single-cluster scaffold pre-fills the optional target_cloud default.
	if !strings.Contains(string(res.PlanInputs), "target_cloud: aws") {
		t.Fatalf("PlanInputs should pre-fill the target_cloud default; got:\n%s", res.PlanInputs)
	}
}

// PlanInputs in the reply must echo back the caller's override under its cluster.
func TestGeneratePlan_PlanInputsEchoesOverridesAndDefaults(t *testing.T) {
	inputs := []byte("clusters:\n  demo-cluster:\n    target_cloud: azure\n")
	res, err := lib.GeneratePlan([]byte(sampleStateJSON), inputs)
	if err != nil {
		t.Fatalf("GeneratePlan: %v", err)
	}
	if !strings.Contains(string(res.PlanInputs), "target_cloud: azure") {
		t.Fatalf("PlanInputs should echo the target_cloud override; got:\n%s", res.PlanInputs)
	}
	var got map[string]any
	if err := yaml.Unmarshal(res.PlanInputs, &got); err != nil {
		t.Fatalf("PlanInputs is not valid YAML: %v", err)
	}
	if _, ok := got["clusters"]; !ok {
		t.Fatalf("PlanInputs missing `clusters` block; keys: %v", keys(got))
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
	inputs := []byte("clusters:\n  demo-cluster:\n    downtime_tolerance: zero\n")
	res, err := lib.GeneratePlan([]byte(sampleStateJSON), inputs)
	if err != nil {
		t.Fatalf("GeneratePlan with YAML inputs: %v", err)
	}
	if !strings.Contains(string(res.PlanInputs), "downtime_tolerance: zero") {
		t.Fatalf("PlanInputs should echo the downtime_tolerance override; got:\n%s", res.PlanInputs)
	}
}

func TestGeneratePlan_RejectsMalformedPlanInputs(t *testing.T) {
	if _, err := lib.GeneratePlan([]byte(sampleStateJSON), []byte(":\n  - not")); err == nil {
		t.Fatal("expected error for malformed plan-inputs")
	}
}

// Unlike the CLI (which fails hard on plan-inputs mistakes), the library returns a
// plan plus warnings so a caller can still detect a mis-typed input rather than have
// it silently dropped. An unknown key and an invalid value must both surface in
// plan.json (a "warnings" array) and plan.md.
func TestGeneratePlan_SurfacesPlanInputWarnings(t *testing.T) {
	inputs := []byte("clusters:\n  demo-cluster:\n    typo_key: whatever\n    target_cloud: marssss\n")
	res, err := lib.GeneratePlan([]byte(sampleStateJSON), inputs)
	if err != nil {
		t.Fatalf("GeneratePlan: %v", err)
	}

	var parsed struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(res.JSON, &parsed); err != nil {
		t.Fatalf("plan.json is not valid JSON: %v", err)
	}
	if len(parsed.Warnings) == 0 {
		t.Fatalf("plan.json should carry plan-input warnings; got none:\n%s", res.JSON)
	}
	joined := strings.Join(parsed.Warnings, "\n")
	if !strings.Contains(joined, "typo_key") {
		t.Errorf("expected the unknown key %q to surface in warnings; got:\n%s", "typo_key", joined)
	}
	if !strings.Contains(joined, "target_cloud") {
		t.Errorf("expected the invalid value for target_cloud to surface in warnings; got:\n%s", joined)
	}
	if !strings.Contains(string(res.Markdown), "typo_key") {
		t.Errorf("expected plan.md to note the dropped input; got first 400 bytes:\n%s", truncate(res.Markdown, 400))
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
