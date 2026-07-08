package markdown

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/output"
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

func mirroredContainsMarked(records []slog.Record, sub string) bool {
	for _, r := range records {
		if !strings.Contains(r.Message, sub) {
			continue
		}
		marked := false
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == output.MirrorMarkerKey {
				marked = true
			}
			return true
		})
		if marked {
			return true
		}
	}
	return false
}

// WriteToTerminal writes raw markdown to stdout; the mirror must also carry it
// into kcp.log without altering the terminal bytes. The fmt.Fprintf(&m.content,
// ...) builder writes go to a buffer and are intentionally NOT mirrored — only
// the os.Stdout terminal write is. Covers R2/R4.
func TestWriteToTerminal_MirrorsNarrative(t *testing.T) {
	const probe = "MirrorProbeParagraph"
	md := New()
	md.AddHeading("Mirror Test", 1)
	md.AddParagraph(probe)

	var stdout string
	records := captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			_, err := md.WriteToTerminal()
			require.NoError(t, err)
		})
	})

	require.Contains(t, stdout, probe, "terminal output must be unchanged")
	require.Contains(t, stdout, "# Mirror Test", "raw markdown must reach the terminal verbatim")
	require.True(t, mirroredContainsMarked(records, probe), "narrative must be mirrored to kcp.log")
}

// WriteToTerminalWithGlamour renders (possibly ANSI-styled) output to stdout;
// the mirror must carry the text into kcp.log with no escape bytes. Covers
// R2/R3/R4.
func TestWriteToTerminalWithGlamour_MirrorsCleanNarrative(t *testing.T) {
	const probe = "GlamourProbeParagraph"
	md := New()
	md.AddHeading("Glamour Mirror Test", 1)
	md.AddParagraph(probe)

	var stdout string
	records := captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			_, err := md.WriteToTerminalWithGlamour()
			require.NoError(t, err)
		})
	})

	require.Contains(t, stdout, probe, "rendered terminal output must contain the paragraph")
	require.True(t, mirroredContainsMarked(records, probe), "narrative must be mirrored to kcp.log")
	for _, r := range records {
		require.NotContains(t, r.Message, "\x1b", "no ANSI escape bytes may reach kcp.log")
	}
}
