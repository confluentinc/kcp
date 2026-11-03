package plan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// Re-running `kcp report plan` on the plan-inputs.yaml it just wrote, with no edits,
// must reproduce plan.md, plan.json, and plan-inputs.yaml byte-for-byte (apart from
// timestamps): nothing the file records may be re-read as a different answer.
func TestReportPlan_RerunWithoutEditsIsIdempotent(t *testing.T) {
	cases := []struct {
		name     string
		scanless bool
		scanFile string
		inputs   string // committed starting inputs, or "" for a first run
	}{
		{name: "first-run", scanFile: stateArgName},
		{name: "filled", scanFile: stateArgName, inputs: filepath.Join(examplesRoot, "filled-inputs.yaml")},
		{name: "osk-scan", scanFile: "demo-osk-scan.json"},
		{name: "scanless", scanless: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var processed report.ProcessedState
			stateArg := tc.scanFile
			if tc.scanless {
				processed = ScanlessState()
				stateArg = ""
			} else {
				data, err := os.ReadFile(filepath.Join(examplesRoot, tc.scanFile))
				if err != nil {
					t.Fatal(err)
				}
				state, err := types.NewStateFromBytes(data)
				if err != nil {
					t.Fatal(err)
				}
				processed = report.NewReportService().ProcessState(*state)
			}

			run := func(inputs []byte) (md, js, yml string) {
				t.Helper()
				declared, _, err := ParseDeclaredInputs(inputs)
				if err != nil {
					t.Fatalf("parse inputs: %v", err)
				}
				ep := BuildEnginePlan(processed, declared, stateArg, fixedExampleClock)
				j, err := RenderEnginePlanJSON(ep)
				if err != nil {
					t.Fatal(err)
				}
				return normalizeMarkdown(RenderEnginePlanMarkdown(ep)), normalizeJSON(j), RenderPlanInputsYAML(ep)
			}

			var start []byte
			if tc.inputs != "" {
				var err error
				if start, err = os.ReadFile(tc.inputs); err != nil {
					t.Fatal(err)
				}
			}
			md1, js1, y1 := run(start)
			md2, js2, y2 := run([]byte(y1))
			if md1 != md2 {
				t.Errorf("plan.md changed on re-run: %s", firstDiff(md1, md2))
			}
			if js1 != js2 {
				t.Errorf("plan.json changed on re-run: %s", firstDiff(js1, js2))
			}
			if y1 != y2 {
				t.Errorf("plan-inputs.yaml changed on re-run: %s", firstDiff(y1, y2))
			}
		})
	}
}
