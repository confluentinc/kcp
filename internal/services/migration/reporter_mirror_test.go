package migration

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/logging"
	"github.com/fatih/color"
)

// TestReporterMirrorsToFileOnly proves option A: the reporter's terminal output
// is unchanged (rich, coloured), and a clean ANSI-stripped copy lands in the
// file-only sink — never echoed to the reporter's stdout/stderr (no doubling).
func TestReporterMirrorsToFileOnly(t *testing.T) {
	// Force colour on so the terminal leg carries ANSI we can assert the mirror strips.
	prevColor := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = prevColor })

	var fileBuf bytes.Buffer
	logging.SetFile(slog.New(slog.NewTextHandler(&fileBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { logging.SetFile(slog.New(slog.DiscardHandler)) })

	var out, errOut bytes.Buffer
	r := &reporter{out: &out, err: &errOut}

	r.section("Initializing migration...")
	r.success("migrated topic %s", "orders")
	r.detail("waiting for STOPPED")
	r.warn("offset lag high")
	r.remediation("run recover to restore")
	r.complete("Migration complete")
	r.line(color.GreenString("promotion table row"))

	term := out.String()
	// Terminal keeps rich formatting: ANSI colour + the ↳ decoration.
	if !strings.Contains(term, "\x1b[") {
		t.Errorf("terminal output should retain ANSI colour:\n%q", term)
	}
	if !strings.Contains(term, "↳ waiting for STOPPED") {
		t.Errorf("terminal detail should keep the ↳ decoration:\n%q", term)
	}

	file := fileBuf.String()
	// File mirror: ANSI-stripped, structured, no terminal-only decoration glyphs.
	if strings.Contains(file, "\x1b[") {
		t.Errorf("file mirror must strip ANSI:\n%s", file)
	}
	if strings.Contains(file, "↳") || strings.Contains(file, "[OK]") {
		t.Errorf("terminal-only decorations (↳/[OK]) must not leak into the log:\n%s", file)
	}
	for _, want := range []string{
		"Initializing migration...",
		"migrated topic orders",
		"waiting for STOPPED",
		"Migration complete",
		"promotion table row",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("file mirror missing %q in:\n%s", want, file)
		}
	}
	// warn/remediation mirror at WARN; narrative at INFO.
	if !strings.Contains(file, "level=WARN") || !strings.Contains(file, "level=INFO") {
		t.Errorf("expected both INFO narrative and WARN cautions in mirror:\n%s", file)
	}
}
