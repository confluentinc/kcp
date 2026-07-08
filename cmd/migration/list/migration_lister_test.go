package list

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/output"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStatusColor(t *testing.T) {
	ml := &MigrationLister{}

	// The new pause stage colors like the other fenced-family states.
	assert.Equal(t, color.New(color.FgYellow), ml.getStatusColor("offset_sync_paused"),
		"offset_sync_paused should color like fenced/fence_verified")

	// Unknown states keep the white default.
	assert.Equal(t, color.New(color.FgWhite), ml.getStatusColor("some_future_state"))
}

// TestGetStatusColor_EveryState pins the color of every known lifecycle state so
// a recoloring regression (e.g. moving fence_verified out of the fenced-family
// yellow, or dropping the bold on switched) can't slip through unnoticed.
func TestGetStatusColor_EveryState(t *testing.T) {
	ml := &MigrationLister{}

	tests := []struct {
		state string
		want  *color.Color
	}{
		{"uninitialized", color.New(color.FgYellow)},
		{"initialized", color.New(color.FgCyan)},
		{"lags_ok", color.New(color.FgCyan)},
		{"fenced", color.New(color.FgYellow)},
		{"offset_sync_paused", color.New(color.FgYellow)},
		{"fence_verified", color.New(color.FgYellow)},
		{"promoted", color.New(color.FgGreen)},
		{"switched", color.New(color.FgGreen, color.Bold)},
		{"some_future_state", color.New(color.FgWhite)},
		{"", color.New(color.FgWhite)},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			assert.Equal(t, tt.want, ml.getStatusColor(tt.state))
		})
	}
}

// --- mirror capture helpers (U6) ---

type mirrorCaptureHandler struct {
	records []slog.Record
}

func (h *mirrorCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *mirrorCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *mirrorCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *mirrorCaptureHandler) WithGroup(string) slog.Handler { return h }

// captureMirror swaps slog.Default() for a recording handler for the duration
// of fn and returns the records the mirror emitted.
func captureMirror(t *testing.T, fn func()) []slog.Record {
	t.Helper()
	h := &mirrorCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)
	fn()
	return h.records
}

// captureStdout returns the exact bytes written to os.Stdout during fn.
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

func hasMirrorMarker(r slog.Record) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == output.MirrorMarkerKey {
			found = true
		}
		return true
	})
	return found
}

// TestRun_MirrorsPlainRowsWhileStdoutKeepsColour verifies U6's additive mirror:
// with colour forced on, the rendered table row on stdout keeps its ANSI SGR
// codes, while the record mirrored into kcp.log is clean plain text (no escape
// bytes). The index/ID row carries three color.* spans, so it proves the strip
// works across multiple spans on one line. Covers R3/R4.
func TestRun_MirrorsPlainRowsWhileStdoutKeepsColour(t *testing.T) {
	// Force colour codes even in a non-TTY test env; restore after.
	prevNoColor := color.NoColor
	color.NoColor = false
	defer func() { color.NoColor = prevNoColor }()

	ml := NewMigrationLister(MigrationListerOpts{
		MigrationStateFile: "does-not-exist.json",
		MigrationState: migration.MigrationState{
			Migrations: []migration.MigrationConfig{
				{
					MigrationId:     "mig-test-001",
					CurrentState:    "promoted",
					K8sNamespace:    "confluent",
					InitialCrName:   "gw-1",
					ClusterLinkName: "link-1",
					Topics:          []string{"orders", "payments"},
				},
			},
		},
	})

	var stdout string
	records := captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			require.NoError(t, ml.Run())
		})
	})

	// The rendered table on stdout is unchanged: colour codes are preserved.
	require.Contains(t, stdout, "\x1b[", "forced-colour stdout must keep SGR codes")

	// The index/ID row (three color.* spans) is mirrored as clean plain text,
	// and every mirrored record is escape-free and marker-bearing.
	const wantRow = "[1] Migration ID: mig-test-001"
	found := false
	for _, r := range records {
		require.NotContainsf(t, r.Message, "\x1b", "no escape bytes may reach kcp.log: %q", r.Message)
		require.True(t, hasMirrorMarker(r), "mirror records must carry the marker attr")
		if r.Message == wantRow {
			found = true
		}
	}
	require.Truef(t, found, "expected a mirrored record with the plain index/ID row %q, got %d records", wantRow, len(records))
}
