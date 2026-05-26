package lib_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/pkg/lib"
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

func TestGeneratePlan_ReturnsBothRenderings(t *testing.T) {
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
	var plan map[string]any
	if err := json.Unmarshal(res.JSON, &plan); err != nil {
		t.Fatalf("GeneratePlan.JSON is not valid JSON: %v", err)
	}
	if !strings.Contains(string(res.Markdown), "Migration Plan") {
		t.Fatalf("GeneratePlan.Markdown missing expected header; got first 200 bytes: %s", truncate(res.Markdown, 200))
	}
}

// Plan-inputs as JSON: goccy/go-yaml accepts JSON (subset of YAML 1.2),
// so HTTP callers can pass an incoming request body straight through
// without a YAML dependency.
func TestGeneratePlan_AcceptsJSONPlanInputs(t *testing.T) {
	inputs := []byte(`{"target_cloud":"azure","headroom_fraction":0.4}`)
	res, err := lib.GeneratePlan([]byte(sampleStateJSON), inputs)
	if err != nil {
		t.Fatalf("GeneratePlan with JSON inputs: %v", err)
	}
	if !strings.Contains(string(res.Markdown), "azure") {
		t.Fatal("plan markdown should mention azure target_cloud from the JSON inputs")
	}
}

func TestGeneratePlan_AcceptsYAMLPlanInputs(t *testing.T) {
	inputs := []byte("target_cloud: azure\nheadroom_fraction: 0.4\n")
	res, err := lib.GeneratePlan([]byte(sampleStateJSON), inputs)
	if err != nil {
		t.Fatalf("GeneratePlan with YAML inputs: %v", err)
	}
	if !strings.Contains(string(res.Markdown), "azure") {
		t.Fatal("plan markdown should mention azure target_cloud from the YAML inputs")
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
