package discover

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

// mirrorRecorder captures the slog records the output mirror emits so this
// spot-check can assert the narrative also reaches kcp.log.
type mirrorRecorder struct{ records []slog.Record }

func (h *mirrorRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *mirrorRecorder) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *mirrorRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *mirrorRecorder) WithGroup(string) slog.Handler      { return h }

// captureStdoutAndMirror runs fn with os.Stdout redirected to a pipe and
// slog.Default() swapped for a recorder, returning the exact stdout bytes and
// the mirror records fn produced. Both are restored on return.
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

// hasMirroredLine reports whether a marker-bearing record with exactly msg was
// emitted (the shape output.Mirror produces).
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

// TestRegionDiscoverer_NarrativeMirrored is the discover-group spot-check: the
// representative "Discovering region" narrative must (a) still reach stdout
// byte-for-byte and (b) appear plain in a mirrored kcp.log record.
func TestRegionDiscoverer_NarrativeMirrored(t *testing.T) {
	rd := NewRegionDiscoverer(&stubRegionMSKService{}, &stubCostService{})

	var err error
	stdout, records := captureStdoutAndMirror(t, func() {
		_, err = rd.Discover(context.Background(), "us-east-1", true /* skipCosts */)
	})
	require.NoError(t, err)

	require.Contains(t, stdout, "🔍 Discovering region us-east-1\n",
		"stdout narrative must be unchanged")
	require.True(t, hasMirroredLine(records, "🔍 Discovering region us-east-1"),
		"narrative must also appear (plain) in a mirrored record")
}
