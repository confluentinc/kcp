package migplan

import (
	"fmt"
	"io"
	"strings"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/fatih/color"
)

// RenderView carries the display-only context RenderReport needs beyond the
// Report itself: the route/target for the header, whether to expand per-topic
// facts (--verbose), and the success-footer note (where artifacts went, or that
// this was a dry-run).
type RenderView struct {
	Route        string
	TargetDomain string
	Verbose      bool
	ArtifactNote string // shown in the success footer, e.g. "artifacts → ./out"
}

// RenderReport writes the topic-centric plan to w: a header, the route checks
// (always every one), then the requested topics — blocked first, then ready,
// then unchanged — and a Terraform-style counts footer. Colours match the
// direct-API migration renderer (green ready, red blocked, faint unchanged,
// yellow warnings). Colour is gated by fatih/color's process-global
// color.NoColor, which the library initialises from os.Stdout's TTY status and
// the NO_COLOR env var — it is NOT keyed off w. So a non-terminal stdout (piped
// output, `go test`) yields plain text, but redirecting w to a file while stdout
// stays a TTY would still emit ANSI; set color.NoColor if that matters.
func RenderReport(w io.Writer, r reconcile.Report, v RenderView) {
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)
	yellow := color.New(color.FgYellow)
	faint := color.New(color.Faint)
	bold := color.New(color.Bold)

	if v.Route != "" {
		_, _ = fmt.Fprintln(w, bold.Sprintf("Migration plan · route %q → %s", v.Route, v.TargetDomain))
		_, _ = fmt.Fprintln(w)
	}

	// Route checks — always every line, pass or fail.
	_, _ = fmt.Fprintln(w, "Route checks")
	failedGates := 0
	for _, p := range r.Preconditions {
		if p.OK {
			_, _ = fmt.Fprintf(w, "  %s %s\n", green.Sprint("✓"), p.Name)
		} else {
			failedGates++
			_, _ = fmt.Fprintf(w, "  %s %s\n", red.Sprint("✗"), red.Sprintf("%s — %s", p.Name, p.Detail))
		}
	}

	// A failed gate stops the run before any topic is evaluated.
	topicsEvaluated := len(r.Migratable)+len(r.Unchanged)+len(r.FailFast) > 0
	if failedGates > 0 && !topicsEvaluated {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, red.Sprintf("Refused at route checks — %d failed. No topics evaluated, no artifacts.", failedGates))
		return
	}

	// Topics — blocked first (what Omar must fix), then ready, then unchanged.
	total := len(r.FailFast) + len(r.Migratable) + len(r.Unchanged)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Topics · %d requested\n", total)

	width := 0
	for _, group := range [][]reconcile.TopicVerdict{r.FailFast, r.Migratable, r.Unchanged} {
		for _, tv := range group {
			if len(tv.Topic) > width {
				width = len(tv.Topic)
			}
		}
	}

	renderTopic := func(glyph string, col *color.Color, word string, tv reconcile.TopicVerdict, showReason bool) {
		// The topic name is padded plain (no ANSI) so columns align; glyph and
		// status word carry the colour.
		_, _ = fmt.Fprintf(w, "  %s %-*s  %s\n", col.Sprint(glyph), width, tv.Topic, col.Sprint(word))
		if showReason && tv.Reason != "" {
			_, _ = fmt.Fprintf(w, "      %s %s\n", faint.Sprint("↳"), tv.Reason)
		}
		if v.Verbose {
			_, _ = fmt.Fprintf(w, "      %s %s\n", faint.Sprint("↳"),
				faint.Sprintf("source %s · mirror %s · target %s · routes %s", tv.S, tv.M, tv.T, tv.R))
		}
		for _, wn := range warningsForTopic(tv.Topic, r.Warnings) {
			_, _ = fmt.Fprintf(w, "      %s %s\n", yellow.Sprint("⚠"), yellow.Sprint(wn))
		}
	}

	for _, tv := range r.FailFast {
		renderTopic("✗", red, "blocked", tv, true)
	}
	for _, tv := range r.Migratable {
		renderTopic("+", green, "ready", tv, false)
	}
	for _, tv := range r.Unchanged {
		renderTopic("=", faint, "unchanged", tv, false)
	}

	// Any warning that doesn't reference a listed topic (rare) → trailing note.
	for _, wn := range unattachedWarnings(r) {
		_, _ = fmt.Fprintf(w, "  %s %s\n", yellow.Sprint("⚠"), yellow.Sprint(wn))
	}

	// Footer — Terraform-style counts, blocked shown only when there are any.
	nMig, nBlk, nUnch := len(r.Migratable), len(r.FailFast), len(r.Unchanged)
	parts := []string{fmt.Sprintf("%d to migrate", nMig)}
	if nBlk > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", nBlk))
	}
	parts = append(parts, fmt.Sprintf("%d unchanged", nUnch))

	var outcome string
	switch {
	case r.Refused():
		outcome = red.Sprint("(refused — no artifacts)")
	case nMig == 0:
		outcome = faint.Sprint("(nothing to do)")
	default:
		note := v.ArtifactNote
		if note == "" {
			note = "artifacts written"
		}
		outcome = green.Sprintf("(%s)", note)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Plan: %s  %s\n", strings.Join(parts, ", "), outcome)
}

// warningsForTopic returns the warnings whose text references topic by its
// quoted name (how the reconcile core spells topics in warning messages).
func warningsForTopic(topic string, warnings []string) []string {
	needle := "\"" + topic + "\""
	var out []string
	for _, wn := range warnings {
		if strings.Contains(wn, needle) {
			out = append(out, wn)
		}
	}
	return out
}

// unattachedWarnings returns warnings that reference none of the listed topics,
// so nothing is silently dropped when a warning can't be attached to a line.
func unattachedWarnings(r reconcile.Report) []string {
	listed := make([]reconcile.TopicVerdict, 0, len(r.FailFast)+len(r.Migratable)+len(r.Unchanged))
	listed = append(listed, r.FailFast...)
	listed = append(listed, r.Migratable...)
	listed = append(listed, r.Unchanged...)
	var out []string
	for _, wn := range r.Warnings {
		attached := false
		for _, tv := range listed {
			if strings.Contains(wn, "\""+tv.Topic+"\"") {
				attached = true
				break
			}
		}
		if !attached {
			out = append(out, wn)
		}
	}
	return out
}
