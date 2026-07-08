package version

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

func mirroredContains(records []slog.Record, sub string) bool {
	for _, r := range records {
		if strings.Contains(r.Message, sub) {
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
	}
	return false
}

// The version command's narrative must still reach stdout and now also appear
// in kcp.log via the mirror. Covers R2/R4.
func TestVersionCmd_NarrativeMirrored(t *testing.T) {
	var stdout string
	records := captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			cmd := NewVersionCmd()
			cmd.Run(cmd, nil)
		})
	})

	require.Contains(t, stdout, "Version: ")
	require.Contains(t, stdout, "Commit:  ")
	require.True(t, mirroredContains(records, "Version: "), "version narrative must be mirrored to kcp.log")
	require.True(t, mirroredContains(records, "Commit:  "), "version narrative must be mirrored to kcp.log")
}
