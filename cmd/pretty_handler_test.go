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
