package plan

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// The `docs/assets/report-plan-examples/**` reference outputs are generated from a
// COMMITTED synthetic scan fixture (`demo-scan.json`) plus a committed answered
// inputs file (`filled-inputs.yaml`), and verified here so they can never silently
// go stale. TestReportPlanExamples_UpToDate regenerates the three artifacts for each
// of the three documented modes through the same library seam `kcp report plan`
// uses — report.ProcessState / ScanlessState → BuildEnginePlan → the Render* funcs —
// normalizes the only nondeterministic bits (the plan.md `Generated …` timestamp and
// the plan.json `generated_at`), and compares byte-for-byte to the committed files.
//
// Regenerate with `make examples` (which sets UPDATE_EXAMPLES=1) after any change
// that alters plan output; the plain `go test` run asserts and fails on drift.

// examplesRoot is the committed reference directory, relative to this package.
const examplesRoot = "../../../docs/assets/report-plan-examples"

// stateArgName is the display name threaded into BuildEnginePlan as the state-file
// path. The plan renders it by base name only (filepath.Base), so a bare, portable
// file name keeps the committed samples free of any absolute path.
const stateArgName = "demo-scan.json"

// fixedExampleClock pins BuildEnginePlan's clock. The value is irrelevant to the
// committed output (it is normalized away below), but pinning it keeps the
// pre-normalization artifacts deterministic too.
func fixedExampleClock() time.Time { return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) }

var (
	// mdGeneratedLine matches the `Generated <ts>` token in the plan.md header meta
	// line (e.g. "Generated 2026-09-10 08:00 UTC"). The scanned/kcp fields on the same
	// line are derived from the committed fixture and stay deterministic.
	mdGeneratedLine = regexp.MustCompile(`Generated \d{4}-\d{2}-\d{2} \d{2}:\d{2} [A-Za-z]+`)
	// jsonGeneratedAt matches the plan.json header.generated_at value.
	jsonGeneratedAt = regexp.MustCompile(`"generated_at": "[^"]*"`)
)

// normalizeMarkdown replaces the nondeterministic generated timestamp with a fixed
// placeholder so the committed plan.md is byte-stable across runs.
func normalizeMarkdown(md string) string {
	return mdGeneratedLine.ReplaceAllString(md, "Generated <example>")
}

// normalizeJSON replaces the nondeterministic header.generated_at with a fixed
// placeholder so the committed plan.json is byte-stable across runs.
func normalizeJSON(js string) string {
	return jsonGeneratedAt.ReplaceAllString(js, `"generated_at": "<example>"`)
}

func TestReportPlanExamples_UpToDate(t *testing.T) {
	update := os.Getenv("UPDATE_EXAMPLES") == "1"

	// Load the committed scan fixture once, exactly as the CLI does.
	scanBytes, err := os.ReadFile(filepath.Join(examplesRoot, "demo-scan.json"))
	if err != nil {
		t.Fatalf("read demo-scan.json fixture: %v", err)
	}
	state, err := types.NewStateFromBytes(scanBytes)
	if err != nil {
		t.Fatalf("load demo-scan.json fixture: %v", err)
	}
	scanned := report.NewReportService().ProcessState(*state)

	modes := []struct {
		name       string // subdirectory under examplesRoot
		scanless   bool   // no --state-file: pure questionnaire
		inputsPath string // committed answered inputs, or "" for a first run
	}{
		{name: "filled", inputsPath: filepath.Join(examplesRoot, "filled-inputs.yaml")},
		{name: "first-run"},
		{name: "no-scan", scanless: true},
	}

	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			// Mirror cmd/report/plan.runReportPlan's construction of the plan.
			processed := scanned
			stateArg := stateArgName
			if m.scanless {
				processed = ScanlessState()
				stateArg = ""
			}

			declared, inputErrs, err := LoadDeclaredInputs(m.inputsPath)
			if err != nil {
				t.Fatalf("%s: load declared inputs: %v", m.name, err)
			}
			if len(inputErrs) > 0 {
				t.Fatalf("%s: committed inputs have problems: %v", m.name, inputErrs)
			}

			ep := BuildEnginePlan(processed, declared, stateArg, fixedExampleClock)
			if m.scanless {
				ep.Header.Source = "Questionnaire (no scan file)"
			}

			js, err := RenderEnginePlanJSON(ep)
			if err != nil {
				t.Fatalf("%s: render plan.json: %v", m.name, err)
			}
			artifacts := map[string]string{
				"plan.md":          normalizeMarkdown(RenderEnginePlanMarkdown(ep)),
				"plan.json":        normalizeJSON(js),
				"plan-inputs.yaml": RenderPlanInputsYAML(ep),
			}

			for name, got := range artifacts {
				path := filepath.Join(examplesRoot, m.name, name)
				if update {
					if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
						t.Fatalf("%s: write %s: %v", m.name, name, err)
					}
					continue
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("%s: read committed %s: %v", m.name, name, err)
				}
				if got != string(want) {
					t.Errorf("%s/%s is stale: generated output differs from the committed reference.\n"+
						"Regenerate with:  make examples\n%s",
						m.name, name, firstDiff(string(want), got))
				}
			}
		})
	}
}

// firstDiff returns a short, human-readable description of the first line that
// differs between want and got, so a drift failure points at the exact change
// instead of dumping the whole file.
func firstDiff(want, got string) string {
	wl := splitLines(want)
	gl := splitLines(got)
	n := len(wl)
	if len(gl) < n {
		n = len(gl)
	}
	for i := 0; i < n; i++ {
		if wl[i] != gl[i] {
			return "first difference at line " + strconv.Itoa(i+1) + ":\n  committed: " + wl[i] + "\n  generated: " + gl[i]
		}
	}
	if len(wl) != len(gl) {
		return "files differ in length: committed has " + strconv.Itoa(len(wl)) + " lines, generated has " + strconv.Itoa(len(gl)) + " lines"
	}
	return "files differ but no line-level difference found (trailing bytes?)"
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
