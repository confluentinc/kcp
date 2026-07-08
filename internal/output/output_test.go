package output

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// captureHandler records the slog records it is handed so tests can assert on
// the mirror leg (message, level, marker attr) without a real logger.
type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

// captureMirror swaps slog.Default() for a recording handler for the duration
// of fn and returns the records the mirror emitted.
func captureMirror(t *testing.T, fn func()) []slog.Record {
	t.Helper()
	h := &captureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)
	fn()
	return h.records
}

// captureStdout mirrors the migration package's helper so output tests assert
// on the exact bytes written to the terminal stream.
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

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	defer func() { os.Stderr = orig }()

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

func hasMarker(r slog.Record) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == MirrorMarkerKey {
			found = true
		}
		return true
	})
	return found
}

// Mirror is the seam U2 (the migration reporter) builds on: it mirrors a
// pre-composed line into kcp.log without touching stdout/stderr, because the
// caller already owns the terminal write. Writing to a stream here would double
// the reporter's output.
func TestMirror_EmitsMarkedRecordWithoutWritingToStreams(t *testing.T) {
	var stdout, stderr string
	records := captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			stderr = captureStderr(t, func() {
				Mirror("\x1b[32m✔\x1b[0m migration complete")
			})
		})
	})

	require.Empty(t, stdout, "Mirror must not write to stdout")
	require.Empty(t, stderr, "Mirror must not write to stderr")
	require.Len(t, records, 1)
	require.Equal(t, "✔ migration complete", records[0].Message)
	require.True(t, hasMarker(records[0]))
}

func TestMirror_EmptyProducesNoRecord(t *testing.T) {
	records := captureMirror(t, func() {
		Mirror("   \n  ")
	})
	require.Empty(t, records)
}

// Log-forging defense (security review LOW-001): a mirrored line must stay a
// single clean kcp.log record. Interior newlines/CRs must not split it into
// multiple (forgeable) lines, and stray C0 control chars must not survive.
func TestMirror_NeutralizesInteriorNewlinesAndControlChars(t *testing.T) {
	records := captureMirror(t, func() {
		Mirror("real line\n2026/07/08 12:00:00 INFO ✅ forged\x00\x07 tail")
	})

	require.Len(t, records, 1, "interior newlines must not split into multiple records")
	msg := records[0].Message
	require.NotContains(t, msg, "\n", "no interior newline may survive into a log record")
	require.NotContains(t, msg, "\r")
	require.NotContains(t, msg, "\x00", "NUL must be stripped")
	require.NotContains(t, msg, "\x07", "control chars must be stripped")
	// content is preserved, just flattened onto one clean line
	require.Contains(t, msg, "real line")
	require.Contains(t, msg, "forged")
	require.Contains(t, msg, "tail")
}

func TestPrintf_WritesStdoutAndMirrorsMarkedRecord(t *testing.T) {
	var stdout string
	records := captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			Printf("hello %s", "world")
		})
	})

	require.Equal(t, "hello world", stdout, "stdout must receive the composed line verbatim")
	require.Len(t, records, 1)
	require.Equal(t, "hello world", records[0].Message)
	require.Equal(t, slog.LevelInfo, records[0].Level)
	require.True(t, hasMarker(records[0]), "mirror record must carry the marker attr")
}

func TestPrintln_WritesStdoutAndMirrors(t *testing.T) {
	var stdout string
	records := captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			Println("done", 42)
		})
	})

	require.Equal(t, "done 42\n", stdout)
	require.Len(t, records, 1)
	require.Equal(t, "done 42", records[0].Message)
	require.True(t, hasMarker(records[0]))
}

func TestPrintf_MirrorStripsANSI(t *testing.T) {
	// Literal SGR codes (not color.CyanString, which no-ops when NoColor is set
	// in a non-TTY test env) so the strip is genuinely exercised. Covers R3.
	records := captureMirror(t, func() {
		_ = captureStdout(t, func() {
			Printf("%s", "\x1b[36mx\x1b[0m")
		})
	})

	require.Len(t, records, 1)
	require.Equal(t, "x", records[0].Message)
	require.NotContains(t, records[0].Message, "\x1b", "no escape bytes may reach kcp.log")
}

func TestPrintf_StdoutKeepsANSIVerbatim(t *testing.T) {
	// The mirror strips ANSI, but the terminal write must keep colour codes.
	var stdout string
	_ = captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			Printf("%s", "\x1b[36mx\x1b[0m")
		})
	})
	require.Equal(t, "\x1b[36mx\x1b[0m", stdout)
}

func TestPrintln_EmptyProducesNoRecord(t *testing.T) {
	var stdout string
	records := captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			Println("")
		})
	})

	require.Equal(t, "\n", stdout, "empty line still reaches the terminal")
	require.Empty(t, records, "an empty line must not be mirrored")
}

func TestPrintf_TrimsMirrorButWritesStdoutVerbatim(t *testing.T) {
	var stdout string
	records := captureMirror(t, func() {
		stdout = captureStdout(t, func() {
			Printf("  \n  msg \n")
		})
	})

	require.Equal(t, "  \n  msg \n", stdout, "stdout write is verbatim")
	require.Len(t, records, 1)
	require.Equal(t, "msg", records[0].Message, "mirror message is trimmed")
}

func TestErrf_WritesStderrAndMirrors(t *testing.T) {
	var stderr string
	records := captureMirror(t, func() {
		stderr = captureStderr(t, func() {
			Errf("problem: %s", "bad")
		})
	})

	require.Equal(t, "problem: bad", stderr)
	require.Len(t, records, 1)
	require.Equal(t, "problem: bad", records[0].Message)
	require.True(t, hasMarker(records[0]))
}

func TestErrln_WritesStderrAndMirrors(t *testing.T) {
	var stderr string
	records := captureMirror(t, func() {
		stderr = captureStderr(t, func() {
			Errln("oops")
		})
	})

	require.Equal(t, "oops\n", stderr)
	require.Len(t, records, 1)
	require.Equal(t, "oops", records[0].Message)
	require.True(t, hasMarker(records[0]))
}
