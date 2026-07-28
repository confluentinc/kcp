package cmd

import (
	"bytes"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/fatih/color"
)

// TestPrettyHandlerConsoleRender demonstrates the two rendering legs:
//   - console: INFO is clean narrative (no time/level prefix); WARN/ERROR
//     colour the level; nothing carries a timestamp.
//   - file: every record stays fully structured (time LEVEL message attrs).
func TestPrettyHandlerConsoleRender(t *testing.T) {
	// Force colour on so the WARN/ERROR level styling is exercised even though
	// `go test` writes to a pipe (fatih/color auto-disables otherwise).
	prev := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = prev })

	render := func(console bool, emit func(*slog.Logger)) string {
		buf := &bytes.Buffer{}
		log := slog.New(NewPrettyHandler(buf, PrettyHandlerOptions{
			SlogOpts: slog.HandlerOptions{Level: slog.LevelDebug},
			Console:  console,
		}))
		emit(log)
		return buf.String()
	}

	tsRE := regexp.MustCompile(`\d{4}/\d{2}/\d{2}`)

	// --- console leg ---
	console := render(true, func(l *slog.Logger) {
		l.Info("🚀 Starting discover")
		l.Info("✅ found clusters", "count", 3)
		l.Warn("could not fetch costs", "region", "us-east-1")
		l.Error("describe cluster failed", "arn", "arn:aws:kafka:us-east-1:...")
	})
	t.Logf("console leg:\n%s", console)

	if tsRE.MatchString(console) {
		t.Errorf("console leg should carry no timestamps:\n%s", console)
	}
	if !strings.Contains(console, "✅ found clusters count=3") {
		t.Errorf("console INFO should be clean narrative with attrs:\n%s", console)
	}
	if !strings.Contains(console, color.YellowString("WARN")) {
		t.Errorf("console WARN level should be yellow:\n%s", console)
	}
	if !strings.Contains(console, color.RedString("ERROR")) {
		t.Errorf("console ERROR level should be red:\n%s", console)
	}

	// --- file leg ---
	file := render(false, func(l *slog.Logger) {
		l.Info("✅ found clusters", "count", 3)
	})
	t.Logf("file leg:\n%s", file)

	if !tsRE.MatchString(file) {
		t.Errorf("file leg should carry a timestamp:\n%s", file)
	}
	if !strings.Contains(file, "INFO ✅ found clusters count=3") {
		t.Errorf("file leg should stay fully structured:\n%s", file)
	}
}

// TestPrettyHandlerConsoleDebugRender locks in how the console leg renders DEBUG
// under --verbose. Unlike INFO (clean, prefix-free narrative), DEBUG keeps a
// "DEBUG" level prefix so diagnostics stay distinct from narrative — but the
// prefix is uncoloured (only WARN/ERROR colour the level) and, like every console
// record, it carries no timestamp.
func TestPrettyHandlerConsoleDebugRender(t *testing.T) {
	prev := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = prev })

	buf := &bytes.Buffer{}
	log := slog.New(NewPrettyHandler(buf, PrettyHandlerOptions{
		SlogOpts: slog.HandlerOptions{Level: slog.LevelDebug},
		Console:  true,
	}))
	log.Debug("🔍 confluent cloud request", "method", "POST", "ms", 210)
	out := buf.String()
	t.Logf("console debug leg:\n%s", out)

	tsRE := regexp.MustCompile(`\d{4}/\d{2}/\d{2}`)
	if tsRE.MatchString(out) {
		t.Errorf("console leg should carry no timestamps:\n%s", out)
	}
	// Exact prefix+message+attrs: the DEBUG token sits immediately before the
	// message with no ANSI escape between them, proving it is uncoloured.
	if !strings.Contains(out, "DEBUG 🔍 confluent cloud request method=POST ms=210") {
		t.Errorf("console DEBUG should keep an uncoloured level prefix + message + attrs:\n%s", out)
	}
	if strings.Contains(out, color.YellowString("DEBUG")) || strings.Contains(out, color.RedString("DEBUG")) {
		t.Errorf("console DEBUG level should not be coloured (only WARN/ERROR are):\n%s", out)
	}
}

// TestColorizeLevel pins the level-colouring policy: WARN/ERROR are coloured
// (carry ANSI escapes), INFO/DEBUG are returned verbatim so they read as plain
// narrative/diagnostics.
func TestColorizeLevel(t *testing.T) {
	prev := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = prev })

	if got := colorizeLevel(slog.LevelDebug); got != "DEBUG" {
		t.Errorf("DEBUG should be uncoloured, got %q", got)
	}
	if got := colorizeLevel(slog.LevelInfo); got != "INFO" {
		t.Errorf("INFO should be uncoloured, got %q", got)
	}
	if got := colorizeLevel(slog.LevelWarn); !strings.Contains(got, "\x1b[") {
		t.Errorf("WARN should be coloured, got %q", got)
	}
	if got := colorizeLevel(slog.LevelError); !strings.Contains(got, "\x1b[") {
		t.Errorf("ERROR should be coloured, got %q", got)
	}
}
