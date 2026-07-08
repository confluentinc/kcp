package metrics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/output"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/mock"
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

// TestMetricReporter_NarrativeMirrored is the report-group spot-check: the
// representative "Processing clusters" narrative must (a) still reach stdout
// byte-for-byte and (b) appear plain in a mirrored kcp.log record. The mock
// errors on the first filter so Run returns after the narrative print without
// writing a report file.
func TestMetricReporter_NarrativeMirrored(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	svc := &MockReportService{}
	svc.On("ProcessState", mock.Anything).Return(report.ProcessedState{})
	svc.On("FilterClusterMetrics", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("stop after narrative"))

	reporter := NewMetricReporter(svc, MetricReporterOpts{
		ClusterIds: []string{"c1"},
		State:      &types.State{},
		StartDate:  &start,
		EndDate:    &end,
	})

	stdout, records := captureStdoutAndMirror(t, func() {
		_ = reporter.Run()
	})

	want := "🔍 Processing clusters: [c1] (from 2025-01-01 to 2025-01-31)"
	require.Contains(t, stdout, want+"\n", "stdout narrative must be unchanged")
	require.True(t, hasMirroredLine(records, want),
		"narrative must also appear (plain) in a mirrored record")
}
