package reconcile

import (
	"context"
	"fmt"
	"io"
)

// Engine runs reconcilers in order, rendering progress to out.
type Engine struct {
	out io.Writer
}

// NewEngine creates an Engine that writes progress output to out.
func NewEngine(out io.Writer) *Engine {
	return &Engine{out: out}
}

// Run executes the §8.4 loop for each reconciler. With dryRun=true it stops
// after Plan (previewing the delta) and never calls Apply.
func (e *Engine) Run(ctx context.Context, rs []Reconciler, dryRun bool) (Report, error) {
	report := Report{DryRun: dryRun, Outcomes: map[string]Outcome{}}

	for _, r := range rs {
		if err := r.CheckPreconditions(ctx); err != nil {
			return report, fmt.Errorf("%s: precondition failed: %w", r.Name(), err)
		}

		plan, err := r.Plan(ctx)
		if err != nil {
			return report, fmt.Errorf("%s: planning failed: %w", r.Name(), err)
		}

		e.renderPlan(r.Name(), plan, dryRun)

		if dryRun {
			continue
		}

		out, err := r.Apply(ctx, plan)
		report.Outcomes[r.Name()] = out
		e.renderOutcome(r.Name(), out)
		if err != nil {
			return report, fmt.Errorf("%s: apply failed: %w", r.Name(), err)
		}
	}

	return report, nil
}

func (e *Engine) renderPlan(name string, p Plan, dryRun bool) {
	verb := "Applying"
	if dryRun {
		verb = "Planned"
	}
	_, _ = fmt.Fprintf(e.out, "== %s (%s) ==\n", name, verb)
	changes := p.Changes()
	if len(changes) == 0 {
		_, _ = fmt.Fprintln(e.out, "  (nothing to do)")
		return
	}
	for _, c := range changes {
		symbol := map[Action]string{ActionCreate: "+", ActionPresent: "✓", ActionDrift: "⚠"}[c.Action]
		line := fmt.Sprintf("  %s %s", symbol, c.Summary)
		if c.Detail != "" {
			line += " — " + c.Detail
		}
		_, _ = fmt.Fprintln(e.out, line)
	}
}

func (e *Engine) renderOutcome(name string, o Outcome) {
	for _, c := range o.Failed {
		line := fmt.Sprintf("  ✖ %s", c.Summary)
		if c.Detail != "" {
			line += " — " + c.Detail
		}
		_, _ = fmt.Fprintln(e.out, line)
	}
	_, _ = fmt.Fprintf(e.out, "  %s: %d created, %d already present, %d drift, %d failed\n",
		name, len(o.Created), len(o.Present), len(o.Drift), len(o.Failed))
}
