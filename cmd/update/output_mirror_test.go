package update

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type mirrorRecorder struct{ records []slog.Record }

func (h *mirrorRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *mirrorRecorder) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *mirrorRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *mirrorRecorder) WithGroup(string) slog.Handler      { return h }

func captureMirror(t *testing.T, fn func()) []slog.Record {
	t.Helper()
	h := &mirrorRecorder{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)
	fn()
	return h.records
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	require.NoError(t, w.Close())
	return <-done
}

// Abuse case (R5): the interactive confirmation prompt solicits stdin; its text
// must never be mirrored into kcp.log. This guards the conversion sweep against
// accidentally routing the prompt through output.* — the prompt stays a bare
// fmt.Print. The prompt is still shown on the terminal.
func TestUpdater_askForConfirmation_PromptNeverMirrored(t *testing.T) {
	prompt := "This will overwrite the current kcp binary. Continue? [y/N]: "

	origStdin := os.Stdin
	rIn, wIn, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin = rIn
	defer func() { os.Stdin = origStdin }()
	_, _ = io.WriteString(wIn, "n\n")
	_ = wIn.Close()

	u := &Updater{}

	var stdout string
	records := captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			_ = u.askForConfirmation(prompt)
		})
	})

	require.Contains(t, stdout, prompt, "prompt must still be shown on the terminal")
	require.Empty(t, records, "an interactive stdin prompt must never be mirrored to kcp.log")
}
