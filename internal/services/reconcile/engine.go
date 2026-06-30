package reconcile

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/fatih/color"
)

// Engine runs reconcilers in order, rendering progress to out.
type Engine struct {
	out io.Writer
}

// NewEngine creates an Engine that writes progress output to out.
func NewEngine(out io.Writer) *Engine {
	return &Engine{out: out}
}

// Action colours. fatih/color auto-disables when stdout isn't a TTY or NO_COLOR
// is set — so piped/tested output (the integration suite execs kcp and captures
// its pipe) is plain text, while an interactive terminal gets colour.
var (
	cCreate    = color.New(color.FgGreen)
	cUnchanged = color.New(color.Faint)
	cDrift     = color.New(color.FgYellow)
	cFailed    = color.New(color.FgRed)
	cReason    = color.New(color.Faint)
)

// Run executes the §8.4 loop for each reconciler. With dryRun=true it previews
// the plan (per-reconciler) and never calls Apply; otherwise it applies and
// renders the outcome.
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

		if dryRun {
			e.renderPlan(r.Name(), plan)
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

// renderPlan previews a plan (dry-run): future-tense actions, nothing applied.
func (e *Engine) renderPlan(name string, p Plan) {
	_, _ = fmt.Fprintf(e.out, "== %s (Planned) ==\n", name)
	changes := p.Changes()
	if len(changes) == 0 {
		_, _ = fmt.Fprintln(e.out, "  (nothing to do)")
		return
	}
	for _, c := range changes {
		switch c.Action {
		case ActionCreate:
			e.renderItem("+", "create", cCreate, c, false)
		case ActionPresent:
			e.renderItem("=", "no change", cUnchanged, c, false)
		case ActionDrift:
			e.renderItem("⚠", "drift", cDrift, c, true)
		}
	}
}

// renderOutcome reports what Apply did: past-tense actions, with drift and
// failure reasons on indented sub-lines, then a one-line summary. Items are
// merged and sorted by summary so the listing reads in name order.
func (e *Engine) renderOutcome(name string, o Outcome) {
	_, _ = fmt.Fprintf(e.out, "== %s (Applying) ==\n", name)

	type item struct {
		c     Change
		glyph string
		word  string
		col   *color.Color
		sub   bool // reason(s) on indented sub-lines
	}
	var items []item
	for _, c := range o.Created {
		items = append(items, item{c, "+", "created", cCreate, false})
	}
	for _, c := range o.Present {
		items = append(items, item{c, "=", "unchanged", cUnchanged, false})
	}
	for _, c := range o.Drift {
		items = append(items, item{c, "⚠", "drift", cDrift, true})
	}
	for _, c := range o.Failed {
		items = append(items, item{c, "✖", "failed", cFailed, true})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].c.Summary < items[j].c.Summary })

	for _, it := range items {
		e.renderItem(it.glyph, it.word, it.col, it.c, it.sub)
	}

	_, _ = fmt.Fprintf(e.out, "  %s: %d created, %d unchanged, %d drift, %d failed\n",
		name, len(o.Created), len(o.Present), len(o.Drift), len(o.Failed))
}

// renderItem prints one action line: "  <glyph> <word>  <summary>". A create or
// no-op detail (short, e.g. "source s1", "3 partitions") stays inline after an
// em dash; a drift or failure reason goes on indented sub-line(s) — one per
// "; "-separated part — because those can be long or multi-part.
func (e *Engine) renderItem(glyph, word string, col *color.Color, c Change, detailOnSubline bool) {
	label := col.Sprintf("%s %-9s", glyph, word)
	line := "  " + label + " " + c.Summary
	if c.Detail != "" && !detailOnSubline {
		line += " — " + c.Detail
	}
	_, _ = fmt.Fprintln(e.out, line)
	if c.Detail != "" && detailOnSubline {
		for _, reason := range splitDetail(c.Detail) {
			_, _ = fmt.Fprintf(e.out, "      %s %s\n", cReason.Sprint("↳"), reason)
		}
	}
}

// splitDetail breaks a "; "-joined detail (e.g. config drift lists several keys)
// into one reason per line, dropping empties.
func splitDetail(d string) []string {
	var out []string
	for _, p := range strings.Split(d, "; ") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
