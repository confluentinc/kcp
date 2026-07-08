package execute

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

func captureStdoutAndMirror(t *testing.T, fn func()) (string, []slog.Record) {
	t.Helper()
	h := &mirrorRecorder{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)

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
	return <-done, h.records
}

func hasMirroredLine(records []slog.Record, msg string) bool {
	for _, r := range records {
		if r.Message != msg {
			continue
		}
		found := false
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == output.MirrorMarkerKey {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// The migration-execute completion line must still reach stdout byte-for-byte
// (protecting any stdout consumer) AND appear plain in a mirrored kcp.log
// record, so a shared support log records that the run finished.
func TestPrintMigrationComplete_MirrorsToLog(t *testing.T) {
	stdout, records := captureStdoutAndMirror(t, func() {
		printMigrationComplete("mig-abc123")
	})

	require.Equal(t, "✅ Migration completed: mig-abc123\n", stdout)
	require.True(t, hasMirroredLine(records, "✅ Migration completed: mig-abc123"),
		"completion line must be mirrored into kcp.log")
}
