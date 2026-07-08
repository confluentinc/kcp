package cmd

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

type bannerRecorder struct{ records []slog.Record }

func (h *bannerRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *bannerRecorder) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *bannerRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *bannerRecorder) WithGroup(string) slog.Handler      { return h }

// The build-provenance banner printed on every command must reach stdout AND be
// mirrored (plain, ANSI-stripped) into kcp.log, so a shared support log records
// which build produced it.
func TestEmitBuildBanner_MirrorsToLog(t *testing.T) {
	h := &bannerRecorder{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)

	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()

	emitBuildBanner()
	require.NoError(t, w.Close())
	os.Stdout = orig
	stdout := <-done

	require.Contains(t, stdout, "Executing kcp with build version=")

	var mirrored bool
	for _, rec := range h.records {
		if !strings.Contains(rec.Message, "Executing kcp with build version=") {
			continue
		}
		require.NotContains(t, rec.Message, "\x1b", "banner mirror must be ANSI-stripped")
		found := false
		rec.Attrs(func(a slog.Attr) bool {
			if a.Key == output.MirrorMarkerKey {
				found = true
			}
			return true
		})
		require.True(t, found, "banner mirror must carry the suppression marker")
		mirrored = true
	}
	require.True(t, mirrored, "build banner must be mirrored into kcp.log")
}
