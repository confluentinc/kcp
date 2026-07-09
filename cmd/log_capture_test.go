package cmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

// runPump feeds write into a pipe, runs pump to completion, and returns what
// reached the terminal leg (verbatim) and the log leg (sanitised).
func runPump(t *testing.T, write func(w *os.File)) (term, logged string) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	var termBuf, logBuf bytes.Buffer
	c := &terminalCapture{}
	c.wg.Add(1)
	go c.pump(r, &termBuf, &logBuf)

	write(w)
	_ = w.Close()
	c.wg.Wait() // happens-before: safe to read the buffers after this

	return termBuf.String(), logBuf.String()
}

func TestPumpForwardsRawAndStripsForLog(t *testing.T) {
	term, logged := runPump(t, func(w *os.File) {
		// cyan "hello" + reset, then a plain line
		_, _ = fmt.Fprint(w, "\x1b[36mhello\x1b[0m\nworld\n")
	})

	if !strings.Contains(term, "\x1b[36mhello\x1b[0m") {
		t.Errorf("terminal leg lost colour: %q", term)
	}
	if term != "\x1b[36mhello\x1b[0m\nworld\n" {
		t.Errorf("terminal leg not verbatim: %q", term)
	}
	if logged != "hello\nworld\n" {
		t.Errorf("log leg not stripped/line-preserved: %q", logged)
	}
}

func TestPumpFlushesTrailingPartialLine(t *testing.T) {
	// An interactive prompt has no trailing newline; it must still reach the
	// log when the stream closes, and reach the terminal verbatim beforehand.
	term, logged := runPump(t, func(w *os.File) {
		_, _ = fmt.Fprint(w, "Continue? (y/N): ")
	})

	if term != "Continue? (y/N): " {
		t.Errorf("terminal leg not verbatim: %q", term)
	}
	if logged != "Continue? (y/N): \n" {
		t.Errorf("trailing partial not flushed: %q", logged)
	}
}

func TestPumpSkipsBlankLines(t *testing.T) {
	_, logged := runPump(t, func(w *os.File) {
		_, _ = fmt.Fprint(w, "\n\n   \nreal line\n\x1b[0m\n")
	})

	if logged != "real line\n" {
		t.Errorf("blank/whitespace/colour-only lines not skipped: %q", logged)
	}
}

func TestPumpStripsControlBytesKeepsTabs(t *testing.T) {
	_, logged := runPump(t, func(w *os.File) {
		_, _ = fmt.Fprint(w, "col1\tcol2\x07\x00\r\n")
	})

	// Tab preserved (table alignment); bell/nul/CR removed.
	if logged != "col1\tcol2\n" {
		t.Errorf("control-byte handling wrong: %q", logged)
	}
}

func TestFlushCaptureNoopWhenInactive(t *testing.T) {
	prev := activeCapture
	activeCapture = nil
	defer func() { activeCapture = prev }()

	FlushCapture() // must not panic when no capture is installed
}
