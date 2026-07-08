package migration

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/confluentinc/kcp/internal/output"
	"github.com/fatih/color"
	"github.com/stretchr/testify/require"
)

// mirrorCaptureHandler records the slog records the reporter's mirror leg emits.
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

func captureReporterMirror(t *testing.T, fn func()) []slog.Record {
	t.Helper()
	h := &mirrorCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)
	fn()
	return h.records
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

func mirrorMessages(records []slog.Record) []string {
	msgs := make([]string, 0, len(records))
	for _, r := range records {
		msgs = append(msgs, r.Message)
	}
	return msgs
}

// R1/R4: every reporter helper writes to its stream AND mirrors the plain text
// into kcp.log with the console-suppression marker.
func TestReporter_MirrorsNarrativeToLog(t *testing.T) {
	old := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = old }()

	var out, errb bytes.Buffer
	r := &reporter{out: &out, err: &errb}

	records := captureReporterMirror(t, func() {
		r.section("⏸ Pausing consumer.offset.sync")
		r.success("%s set to false", "consumer.offset.sync.enable")
		r.detail("waiting for %d topics", 3)
		r.warn("lag still %d", 5)
		r.remediation("manual step required")
	})

	require.Contains(t, out.String(), "⏸ Pausing consumer.offset.sync")
	require.Contains(t, out.String(), "consumer.offset.sync.enable set to false")
	require.Contains(t, errb.String(), "manual step required")

	msgs := mirrorMessages(records)
	require.Contains(t, msgs, "⏸ Pausing consumer.offset.sync")
	require.Contains(t, msgs, "✔ consumer.offset.sync.enable set to false")
	require.Contains(t, msgs, "↳ waiting for 3 topics")
	require.Contains(t, msgs, "⚠️ lag still 5")
	require.Contains(t, msgs, "⚠️ manual step required")
	for _, rec := range records {
		require.True(t, hasMirrorMarker(rec), "every reporter mirror record must carry the marker")
	}
}

// R3: a colourful success line mirrors a record with no escape bytes, while the
// terminal keeps the colour codes.
func TestReporter_MirrorStripsANSI(t *testing.T) {
	old := color.NoColor
	color.NoColor = false
	defer func() { color.NoColor = old }()

	var out bytes.Buffer
	r := &reporter{out: &out, err: &out}

	records := captureReporterMirror(t, func() {
		r.success("done")
	})

	require.Len(t, records, 1)
	require.Equal(t, "✔ done", records[0].Message)
	require.NotContains(t, records[0].Message, "\x1b", "kcp.log must not contain escape bytes")
	require.Contains(t, out.String(), "\x1b[", "terminal keeps colour codes")
}

// A blank line reaches the terminal but is not mirrored (empty guard).
func TestReporter_BlankEmitsNoRecord(t *testing.T) {
	var out bytes.Buffer
	r := &reporter{out: &out, err: &out}

	records := captureReporterMirror(t, func() {
		r.blank()
	})

	require.Equal(t, "\n", out.String())
	require.Empty(t, records)
}

// R4 regression: the exact bytes each helper writes to its stream must be
// unchanged by the additive mirror — the e2e stdout pins and the
// orchestrator/offset-sync stdout captures depend on byte-identical output.
func TestReporter_StreamBytesUnchanged(t *testing.T) {
	old := color.NoColor
	color.NoColor = true
	defer func() { color.NoColor = old }()

	var out, errb bytes.Buffer
	r := &reporter{out: &out, err: &errb}

	_ = captureReporterMirror(t, func() {
		r.section("🔍 Init")
		r.success("ok")
		r.detail("step")
		r.warn("careful")
		r.stepDone()
		r.complete("✅ Migration complete")
		r.blank()
		r.line("row")
		r.remediation("fix this")
	})

	wantOut := "\n🔍 Init\n" +
		"   ✔ ok\n" +
		"   ↳ step\n" +
		"   ⚠️ careful\n" +
		"✅ Done\n" +
		"\n✅ Migration complete\n" +
		"\n" +
		"row\n"
	require.Equal(t, wantOut, out.String())
	require.Equal(t, "⚠️ fix this\n", errb.String())
}
