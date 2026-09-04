package migplan

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/fatih/color"
)

// RenderReport writes the per-topic report to w. Colours auto-disable when the
// output is not a TTY (fatih/color), so piped/test output is plain text.
func RenderReport(w io.Writer, r reconcile.Report) {
	faint := color.New(color.Faint)
	for _, p := range r.Preconditions {
		if p.OK {
			_, _ = fmt.Fprintf(w, "  %s %s\n", color.GreenString("✓"), p.Name)
		} else {
			_, _ = fmt.Fprintf(w, "  %s %s\n", color.RedString("✗ %s — %s", p.Name, p.Detail), "")
		}
	}
	for _, tv := range r.Migratable {
		_, _ = fmt.Fprintln(w, color.GreenString("  + migratable  %s", tv.Topic))
	}
	for _, tv := range r.Unchanged {
		_, _ = fmt.Fprintln(w, faint.Sprintf("  = unchanged   %s", tv.Topic))
	}
	for _, tv := range r.FailFast {
		_, _ = fmt.Fprintln(w, color.RedString("  ✗ fail-fast   %s — %s", tv.Topic, tv.Reason))
	}
	for _, warn := range r.Warnings {
		_, _ = fmt.Fprintln(w, color.YellowString("  ⚠ %s", warn))
	}
}

// WriteArtifacts writes topics.json (a JSON array), fence-rules.yaml, and
// switchover-rules.yaml into dir.
func WriteArtifacts(dir string, a *reconcile.Artifacts) error {
	if a == nil {
		return fmt.Errorf("no artifacts to write")
	}
	topicsJSON, err := json.MarshalIndent(a.Topics, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling topics.json: %w", err)
	}
	writes := []struct {
		name string
		data []byte
	}{
		{"topics.json", topicsJSON},
		{"fence-rules.yaml", a.FenceRules},
		{"switchover-rules.yaml", a.SwitchoverRules},
	}
	for _, wr := range writes {
		if err := os.WriteFile(filepath.Join(dir, wr.name), wr.data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", wr.name, err)
		}
	}
	return nil
}
