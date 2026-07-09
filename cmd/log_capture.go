package cmd

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"sync"
)

// terminalCapture tees everything written to os.Stdout/os.Stderr once
// installCapture has run: bytes are forwarded verbatim to the original streams
// (colour intact) and, line by line, an ANSI/control-stripped copy is written
// to kcp.log. This makes the user-facing narrative land in the shared support
// log by default — with no per-call-site wrapper — while usage/help text
// (emitted before PersistentPreRun installs the capture) is naturally excluded.
//
// This is the "capture by default" alternative to an opt-in output.* package:
// commands keep using plain fmt.Print*/color and their output is mirrored for
// them. slog stays independent for diagnostics.
type terminalCapture struct {
	realOut, realErr *os.File
	outW, errW       *os.File
	wg               sync.WaitGroup
	closeOnce        sync.Once
}

// activeCapture holds the installed capture so FlushCapture (deferred in main)
// can tear it down on any exit path. It is nil when no capture is active — for
// example the --help path, where cobra prints usage without ever running the
// root PersistentPreRun.
var activeCapture *terminalCapture

var (
	// captureANSIRE matches SGR colour codes (e.g. \x1b[36m, \x1b[0m) that
	// fatih/color emits on a TTY. Stripping them keeps the kcp.log copy plain.
	captureANSIRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

	// captureCtrlRE matches C0 control characters except tab (\x09) and newline
	// (\x0a, handled by line splitting) plus DEL. Stripping them keeps the log
	// free of non-printable bytes and stops mirrored content forging log lines.
	captureCtrlRE = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f]`)
)

// installCapture redirects os.Stdout and os.Stderr through pipes, forwarding
// bytes to the original streams and mirroring a clean, line-based copy into
// logW.
//
// It must be called *after* slog's console handler has been bound to the
// original os.Stdout; otherwise slog console records would loop back through
// the mirror and be double-written to kcp.log (the file handler already logs
// them). It must also run *after* the cwd write check, since a mirror target
// in an unwritable directory is pointless.
func installCapture(logW io.Writer) {
	c := &terminalCapture{realOut: os.Stdout, realErr: os.Stderr}

	outR, outW, err := os.Pipe()
	if err != nil {
		return // leave the streams untouched; narrative simply isn't mirrored
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return
	}

	c.outW, c.errW = outW, errW
	os.Stdout = outW
	os.Stderr = errW

	c.wg.Add(2)
	go c.pump(outR, c.realOut, logW)
	go c.pump(errR, c.realErr, logW)

	activeCapture = c
}

// FlushCapture restores the original streams, closes the pipe writers, and
// waits for the pump goroutines to drain any buffered output into kcp.log. It
// is safe to call when no capture is active. Defer it from main so it runs on
// success, error, and panic-unwind — before the process exits — because the
// mirror is drained asynchronously and os.Exit would otherwise abandon it.
func FlushCapture() {
	c := activeCapture
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		os.Stdout = c.realOut
		os.Stderr = c.realErr
		_ = c.outW.Close()
		_ = c.errW.Close()
		c.wg.Wait()
	})
}

// pump forwards raw bytes from r to term as they arrive — so half-line
// interactive prompts appear on the terminal immediately — while accumulating
// a line buffer. Each completed line is sanitised and written to logW; any
// trailing partial line (e.g. a prompt with no newline) is flushed when the
// pipe closes.
func (c *terminalCapture) pump(r io.ReadCloser, term io.Writer, logW io.Writer) {
	defer c.wg.Done()
	defer func() { _ = r.Close() }()

	buf := make([]byte, 4096)
	var line []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			_, _ = term.Write(chunk) // verbatim, immediate: colour + prompts intact

			line = append(line, chunk...)
			for {
				i := bytes.IndexByte(line, '\n')
				if i < 0 {
					break
				}
				mirrorLine(logW, line[:i])
				line = line[i+1:]
			}
		}
		if err != nil {
			mirrorLine(logW, line) // flush any trailing partial line
			return
		}
	}
}

// mirrorLine strips colour and control bytes from a single terminal line and
// writes the clean result (with a trailing newline) to logW. Blank or
// whitespace-only lines produce no record, so kcp.log isn't padded with empties.
func mirrorLine(logW io.Writer, raw []byte) {
	clean := captureANSIRE.ReplaceAll(raw, nil)
	clean = captureCtrlRE.ReplaceAll(clean, nil)
	if len(bytes.TrimSpace(clean)) == 0 {
		return
	}
	_, _ = logW.Write(append(clean, '\n'))
}
