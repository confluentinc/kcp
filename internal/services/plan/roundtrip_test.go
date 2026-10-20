package plan

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// scanlessBase is a fully answered scanless questionnaire that every
// roundtrip fixture layers a few extra lines on.
const scanlessBase = `clusters:
  your-cluster:
    use_case_breadth: few-teams
    move_existing_data: true
    private_networking_required: true
    schema_registry: none
    schema_strategy: schemaless
    downtime_tolerance: minutes
    kafka_version: 3.0-plus
    partition_band: under-2500
    tiered_storage: false
    topics_have_custom_settings: false
    self_managed_connectors: false
    cc_egress_required: false
    connects_today: privatelink
`

// Re-running with no edits must not change the plan: render the plan, write
// plan-inputs.yaml, read it back, and plan again. The recommendations, steps and
// commands (plan.md) and the verdicts (plan.json, less the per-question
// bookkeeping that legitimately moves from "default" to "answered") must be
// identical between the two runs.
func TestPlanInputsRoundTrip_Idempotent(t *testing.T) {
	loadScan := func(t *testing.T, file string) report.ProcessedState {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(examplesRoot, file))
		if err != nil {
			t.Fatal(err)
		}
		state, err := types.NewStateFromBytes(b)
		if err != nil {
			t.Fatal(err)
		}
		return report.NewReportService().ProcessState(*state)
	}
	cases := []struct {
		name     string
		scanFile string // "" = scanless
		inputs   string
	}{
		{name: "msk-scan-first-run", scanFile: "demo-scan.json"},
		{name: "osk-scan-first-run", scanFile: "demo-osk-scan.json"},
		{name: "ak-azure-scanless", inputs: scanlessBase + "    source_platform: apache-kafka\n    source_cloud: azure\n    source_auth: [scram]\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			processed, stateArg := ScanlessState(), ""
			if c.scanFile != "" {
				processed, stateArg = loadScan(t, c.scanFile), c.scanFile
			}
			declared, _, err := ParseDeclaredInputs([]byte(c.inputs))
			if err != nil {
				t.Fatal(err)
			}
			run1 := BuildEnginePlan(processed, declared, stateArg, fixedExampleClock)
			yaml1 := RenderPlanInputsYAML(run1)

			declared2, warns, err := ParseDeclaredInputs([]byte(yaml1))
			if err != nil || len(warns) > 0 {
				t.Fatalf("rewritten plan-inputs.yaml does not parse cleanly: %v %v", err, warns)
			}
			run2 := BuildEnginePlan(processed, declared2, stateArg, fixedExampleClock)

			if a, b := planBody(t, run1), planBody(t, run2); a != b {
				t.Errorf("plan changed between run 1 and run 2 with no edits:\n%s", firstDiff(a, b))
			}
			// The rewrite settles after one pass: rewriting run 2 and planning again
			// changes nothing.
			yaml2 := RenderPlanInputsYAML(run2)
			declared3, _, _ := ParseDeclaredInputs([]byte(yaml2))
			run3 := BuildEnginePlan(processed, declared3, stateArg, fixedExampleClock)
			if yaml3 := RenderPlanInputsYAML(run3); yaml3 != yaml2 {
				t.Errorf("plan-inputs.yaml is not stable after a rewrite:\n%s", firstDiff(yaml2, yaml3))
			}
		})
	}
}

var optionalCountRE = regexp.MustCompile(`\d+ optional`)

// planBody renders plan.md plus the plan.json verdict content (questions and the
// summary roll-up are bookkeeping) for comparing two runs.
func planBody(t *testing.T, ep *EnginePlan) string {
	t.Helper()
	md := normalizeMarkdown(RenderEnginePlanMarkdown(ep))
	// A flat plan-inputs.yaml writes pre-selected optionals live, so on the next run
	// they read as answered rather than open: the optional count moves, the plan
	// doesn't.
	md = optionalCountRE.ReplaceAllString(md, "N optional")
	// The "Answers by cluster" tables list that same bookkeeping (which optionals
	// are pre-selected vs answered), so they are left out of the comparison.
	if i, j := strings.Index(md, "## Answers by cluster"), strings.Index(md, "## Talk to a person"); i >= 0 && j > i {
		md = md[:i] + md[j:]
	}
	cp := *ep
	cp.Summary = PlanSummary{}
	cp.Clusters = append([]ClusterPlan(nil), ep.Clusters...)
	for i := range cp.Clusters {
		cp.Clusters[i].Questions = nil
	}
	js, err := RenderEnginePlanJSON(&cp)
	if err != nil {
		t.Fatal(err)
	}
	return md + "\n---\n" + normalizeJSON(js)
}
