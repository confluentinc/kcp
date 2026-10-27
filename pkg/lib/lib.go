package lib

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/services/plan"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// PlanResult is the output of GeneratePlan. JSON and Markdown are two
// renderings of the same Plan from a single Build pass. PlanInputs is
// the resolved input set (caller-supplied fields merged with kcp
// defaults) serialised as YAML — same shape as a plan-inputs.yaml file,
// so a UI can show it as an editable text block (with room for future
// commented-out optional knobs that a JSON echo would strip).
type PlanResult struct {
	JSON       []byte // same schema as `kcp report plan --output json`
	Markdown   []byte // same rendering as `kcp report plan --output md`
	PlanInputs []byte // resolved plan-inputs (request merged with kcp defaults), as YAML
}

// ScanSummary parses a kcp-state.json byte slice and returns the
// ProcessedState as JSON bytes — the flattened, aggregated view the
// kcp UI serves at GET /state. Stateless; safe for concurrent use.
func ScanSummary(stateJSON []byte) ([]byte, error) {
	state, err := types.NewStateFromBytes(stateJSON)
	if err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	processed := report.NewReportService().ProcessState(*state)
	out, err := json.Marshal(processed)
	if err != nil {
		return nil, fmt.Errorf("marshal processed state: %w", err)
	}
	return out, nil
}

// GeneratePlan builds a migration plan from a state file and optional
// plan-inputs YAML. Pass nil planInputsYAML for defaults. planInputsYAML
// must follow the plan-inputs.yaml shape; see docs/assets/plan-inputs.example.yaml.
func GeneratePlan(stateJSON, planInputsYAML []byte) (*PlanResult, error) {
	state, err := types.NewStateFromBytes(stateJSON)
	if err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	declared, inputWarnings, err := plan.ParseDeclaredInputs(planInputsYAML)
	if err != nil {
		return nil, fmt.Errorf("parse plan-inputs: %w", err)
	}
	processed := report.NewReportService().ProcessState(*state)
	// Empty state-file path: library callers passed bytes, not a file. The
	// renderer omits the "from <path>" header clause when this is empty.
	ep := plan.BuildEnginePlan(processed, declared, "", time.Now)
	// The CLI fails hard on plan-inputs mistakes (unknown keys, invalid values);
	// a library caller instead gets a plan plus these warnings, so mis-typed
	// inputs stay detectable rather than being silently dropped. Surface them
	// first — they precede any advisory warnings the engine added.
	ep.Warnings = append(inputWarnings, ep.Warnings...)
	js, err := plan.RenderEnginePlanJSON(ep)
	if err != nil {
		return nil, fmt.Errorf("render plan json: %w", err)
	}
	md := plan.RenderEnginePlanMarkdown(ep)
	piYAML := plan.RenderPlanInputsYAML(ep)
	return &PlanResult{JSON: []byte(js), Markdown: []byte(md), PlanInputs: []byte(piYAML)}, nil
}
