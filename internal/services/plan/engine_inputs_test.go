package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDeclaredInputs_RejectsUnknownTopLevelKeys(t *testing.T) {
	for _, top := range []string{"clusterz", "all_clusterz", "foo"} {
		_, _, err := ParseDeclaredInputs([]byte(top + ":\n  x:\n    source_platform: msk\n"))
		if err == nil || !strings.Contains(err.Error(), top) || !strings.Contains(err.Error(), "all_clusters") {
			t.Errorf("%s: want error naming key and valid keys, got %v", top, err)
		}
	}
	if _, _, err := ParseDeclaredInputs([]byte("all_clusters:\n  source_platform: msk\nclusters:\n  a: {}\n")); err != nil {
		t.Errorf("valid keys rejected: %v", err)
	}
}

// A question that is required for one cluster but optional for the others must
// still show an active `# not set` line in that cluster's own block, so the
// "N required still open" count can be found in the file.
func TestPlanInputs_ClusterSpecificRequiredQuestionIsListed(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(examplesRoot, "first-run", "plan-inputs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	block := string(data)
	i := strings.Index(block, "\n  logs-ingest:")
	if i < 0 {
		t.Fatal("no logs-ingest block")
	}
	if !strings.Contains(block[i:], "\n    cc_egress_required:   # not set") {
		t.Errorf("logs-ingest block is missing the required cc_egress_required line:\n%s", block[i:])
	}
}

// A scanless or non-MSK cluster has no region, so its header comment omits the region
// rather than printing an empty "region:  ·".
func TestPlanInputs_HeaderOmitsEmptyRegion(t *testing.T) {
	declared, _, err := ParseDeclaredInputs([]byte(stepsInputs(nil)))
	if err != nil {
		t.Fatal(err)
	}
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	out := RenderPlanInputsYAML(BuildEnginePlan(ScanlessState(), declared, "", fixed))
	if strings.Contains(out, "region:") {
		t.Errorf("empty region should be omitted from the header:\n%s", out)
	}
	if !strings.Contains(out, "  your-cluster:   # ") || !strings.Contains(out, "open required") {
		t.Errorf("header comment missing:\n%s", out)
	}
}
