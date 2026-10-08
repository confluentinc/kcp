package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

func demoProcessed(t *testing.T) report.ProcessedState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(examplesRoot, stateArgName))
	if err != nil {
		t.Fatal(err)
	}
	state, err := types.NewStateFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	return report.NewReportService().ProcessState(*state)
}

func writeBack(t *testing.T, processed report.ProcessedState, inputs string) string {
	t.Helper()
	declared, _, err := ParseDeclaredInputs([]byte(inputs))
	if err != nil {
		t.Fatalf("parse inputs: %v", err)
	}
	return RenderPlanInputsYAML(BuildEnginePlan(processed, declared, stateArgName, fixedExampleClock))
}

// A default line the customer uncommented without changing the value stays live
// on write-back, so a second run reads the same answers as the first.
func TestPlanInputs_UncommentedDefaultStaysLive(t *testing.T) {
	processed := demoProcessed(t)
	y0 := writeBack(t, processed, "")
	const def = "# exceeds_standard_limits: false   # default"
	if !strings.Contains(y0, def) {
		t.Fatalf("expected a commented default line in first-run inputs:\n%s", y0)
	}
	edited := strings.Replace(y0, def, "exceeds_standard_limits: false", 1)
	y1 := writeBack(t, processed, edited)
	if !strings.Contains(y1, "\n  exceeds_standard_limits: false\n") {
		t.Errorf("uncommented default was re-commented on write-back:\n%s", y1)
	}
	if y2 := writeBack(t, processed, y1); y1 != y2 {
		t.Errorf("write-back is not idempotent: %s", firstDiff(y1, y2))
	}
}

// An all_clusters answer survives write-back even when every cluster overrides it.
func TestPlanInputs_FleetAnswerKeptWhenEveryClusterOverrides(t *testing.T) {
	processed := demoProcessed(t)
	ep := BuildEnginePlan(processed, DeclaredInputs{}, stateArgName, fixedExampleClock)
	var b strings.Builder
	b.WriteString("all_clusters:\n  target_cloud: gcp\nclusters:\n")
	for _, cp := range ep.Clusters {
		b.WriteString("  " + cp.ClusterID + ":\n    target_cloud: azure\n")
	}
	y := writeBack(t, processed, b.String())
	head, _, _ := strings.Cut(y, "\nclusters:\n")
	if !strings.Contains(head, "\n  target_cloud: gcp\n") {
		t.Errorf("fleet-wide answer was erased from all_clusters:\n%s", head)
	}
}

// A repeated token in a declared list is collapsed.
func TestParseDeclaredInputs_DedupesListTokens(t *testing.T) {
	declared, _, err := ParseDeclaredInputs([]byte("all_clusters:\n  source_auth: [scram, scram]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := declared.Defaults.OvSourceAuth; len(got) != 1 {
		t.Errorf("source_auth = %v, want one entry", got)
	}
}

// An all_clusters answer survives write-back when every cluster is withheld and one
// cluster overrides it (so the clusters no longer agree on a shared value).
func TestPlanInputs_FleetAnswerKeptWhenEveryClusterWithheld(t *testing.T) {
	processed := report.ProcessedState{Sources: []report.ProcessedSource{{
		OSKData: &report.ProcessedOSKSource{Clusters: []report.ProcessedOSKCluster{{ID: "a"}, {ID: "b"}}},
	}}}
	declared, _, err := ParseDeclaredInputs([]byte("all_clusters:\n  use_case_breadth: shared-fabric\n  downtime_tolerance: zero\nclusters:\n  a:\n    downtime_tolerance: minutes\n"))
	if err != nil {
		t.Fatal(err)
	}
	ep := BuildEnginePlan(processed, declared, "", fixedExampleClock)
	for _, cp := range ep.Clusters {
		if !cp.Plan.Withheld {
			t.Fatalf("cluster %s should be withheld", cp.ClusterID)
		}
	}
	y := RenderPlanInputsYAML(ep)
	head, _, _ := strings.Cut(y, "\nclusters:\n")
	if !strings.Contains(head, "\n  downtime_tolerance: zero\n") {
		t.Errorf("fleet-wide answer was wiped from all_clusters:\n%s", head)
	}
}
