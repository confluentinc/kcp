package cmd

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/output"
	"github.com/stretchr/testify/require"
)

func markerRecord(msg string) slog.Record {
	r := slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
	r.Add(output.MirrorMarkerKey, true)
	return r
}

// A marker-bearing record must be dropped by the console handler in the default
// (Warn+) configuration so the mirror never doubles the terminal write. Covers
// R4.
func TestPrettyHandler_ConsoleDropsMirrorRecord_Warn(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, PrettyHandlerOptions{
		SlogOpts:     slog.HandlerOptions{Level: slog.LevelWarn},
		DropMirrored: true,
	})

	require.NoError(t, h.Handle(context.Background(), markerRecord("mirrored line")))
	require.Empty(t, buf.String(), "console handler must drop marker-bearing records")
}

// ...and also under --verbose (Debug+), where the level gate no longer hides an
// Info mirror record — the marker drop is what prevents the duplicate. Covers R4.
func TestPrettyHandler_ConsoleDropsMirrorRecord_Verbose(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, PrettyHandlerOptions{
		SlogOpts:     slog.HandlerOptions{Level: slog.LevelDebug},
		DropMirrored: true,
	})

	require.NoError(t, h.Handle(context.Background(), markerRecord("mirrored line")))
	require.Empty(t, buf.String(), "console handler must drop marker records even under --verbose")
}

// The file handler writes the mirror record, but the marker key must not appear
// in the rendered kcp.log line. Covers R3.
func TestPrettyHandler_FileWritesMirrorRecordWithoutMarkerKey(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, PrettyHandlerOptions{
		SlogOpts: slog.HandlerOptions{Level: slog.LevelDebug},
		// DropMirrored defaults false: this is the file handler.
	})

	require.NoError(t, h.Handle(context.Background(), markerRecord("mirrored line")))
	out := buf.String()
	require.Contains(t, out, "mirrored line")
	require.NotContains(t, out, output.MirrorMarkerKey, "marker key must not leak into kcp.log")
}

// Normal (non-marker) records must still render with their attrs, including the
// e2e-relied-upon key=value tail (e.g. topics=3). Guards against the marker
// change breaking ordinary logging.
func TestPrettyHandler_RendersNormalRecordWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, PrettyHandlerOptions{
		SlogOpts: slog.HandlerOptions{Level: slog.LevelDebug},
	})

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "promoting mirror topics", 0)
	r.Add("topics", 3)
	require.NoError(t, h.Handle(context.Background(), r))

	out := buf.String()
	require.Contains(t, out, "promoting mirror topics")
	require.Contains(t, out, "topics=3")
}

// Even the console handler must render normal (non-marker) records at/above its
// level — DropMirrored only drops marker-bearing records.
func TestPrettyHandler_ConsoleRendersNormalRecord(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, PrettyHandlerOptions{
		SlogOpts:     slog.HandlerOptions{Level: slog.LevelWarn},
		DropMirrored: true,
	})

	r := slog.NewRecord(time.Now(), slog.LevelWarn, "a real warning", 0)
	require.NoError(t, h.Handle(context.Background(), r))
	require.Contains(t, buf.String(), "a real warning")
}
