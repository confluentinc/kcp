package lib

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/services/plan"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/goccy/go-yaml"
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
	cfg, err := plan.LoadPlanConfig("")
	if err != nil {
		return nil, fmt.Errorf("load plan-config: %w", err)
	}
	var pi *plan.PlanInputs
	if len(planInputsYAML) > 0 {
		var parsed plan.PlanInputs
		if err := yaml.Unmarshal(planInputsYAML, &parsed); err != nil {
			return nil, fmt.Errorf("parse plan-inputs: %w", err)
		}
		pi = &parsed
	}
	resolved := plan.ResolvePlanInputs(pi, cfg)
	processed := report.NewReportService().ProcessState(*state)
	// Empty state-file path: library callers passed bytes, not a file.
	// The renderer omits the "from <path>" header clause when this is
	// empty; JSON consumers get `"state_file_path": ""`.
	p, err := plan.NewPlanService(cfg, time.Now).Build(processed, resolved, "")
	if err != nil {
		return nil, fmt.Errorf("build plan: %w", err)
	}
	js, err := plan.RenderJSON(p)
	if err != nil {
		return nil, fmt.Errorf("render plan json: %w", err)
	}
	md, err := plan.RenderMarkdown(p, cfg)
	if err != nil {
		return nil, fmt.Errorf("render plan markdown: %w", err)
	}
	// Strip the Raw pointer before YAML marshalling so the echoed
	// plan-inputs match the flat plan-inputs.yaml shape — Raw is a
	// runtime helper that surfaces customer-set vs default fields and
	// has no place in user-facing YAML output.
	echo := resolved
	echo.Raw = nil
	piYAML, err := yaml.Marshal(echo)
	if err != nil {
		return nil, fmt.Errorf("marshal resolved plan-inputs: %w", err)
	}
	return &PlanResult{JSON: js, Markdown: md, PlanInputs: piYAML}, nil
}
