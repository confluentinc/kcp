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
// plan-inputs. Pass nil planInputs for defaults. planInputs may be JSON
// or YAML bytes — JSON is a YAML 1.2 subset, so goccy/go-yaml accepts
// either content type, which lets HTTP callers pass an incoming
// request body straight through without a YAML dependency on their side.
func GeneratePlan(stateJSON, planInputs []byte) (*PlanResult, error) {
	state, err := types.NewStateFromBytes(stateJSON)
	if err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	cfg, err := plan.LoadPlanConfig("")
	if err != nil {
		return nil, fmt.Errorf("load plan-config: %w", err)
	}
	var pi *types.PlanInputs
	if len(planInputs) > 0 {
		var parsed types.PlanInputs
		if err := yaml.Unmarshal(planInputs, &parsed); err != nil {
			return nil, fmt.Errorf("parse plan-inputs: %w", err)
		}
		pi = &parsed
	}
	resolved := plan.ResolvePlanInputs(pi, cfg)
	processed := report.NewReportService().ProcessState(*state)
	p, err := plan.NewPlanService(cfg, time.Now).Build(processed, resolved, "lib")
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
	piYAML, err := yaml.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("marshal resolved plan-inputs: %w", err)
	}
	return &PlanResult{JSON: js, Markdown: md, PlanInputs: piYAML}, nil
}
