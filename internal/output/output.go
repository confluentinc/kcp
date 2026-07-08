// Package output writes user-facing text to the terminal and mirrors a clean,
// ANSI-stripped copy into kcp.log via slog, so support can reconstruct what a
// command did from the shared log. It is the single seam every command uses in
// place of bare fmt.Print*/color calls.
//
// The mirror is strictly additive: the terminal write is unchanged (colour
// preserved), and a second, marker-bearing slog record carries the plain text
// to the file handler. The cmd console handler drops marker-bearing records, so
// mirrored lines never double on the console — in normal or --verbose mode.
package output

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// MirrorMarkerKey tags a slog record as a mirror of terminal output. The cmd
// console PrettyHandler drops any record carrying it (no console duplication);
// the file PrettyHandler writes the record but omits this key from the rendered
// line, keeping kcp.log clean.
const MirrorMarkerKey = "__mirror"

// ansiRE matches SGR colour codes (e.g. \x1b[36m, \x1b[0m) that fatih/color
// emits when stdout is a TTY. Stripping them keeps kcp.log plain-text.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// newlineRE matches runs of CR/LF. A mirrored terminal write may span multiple
// lines (e.g. glamour-rendered markdown, multi-line remediation notes); each
// such run is flattened to a single space so the mirror stays one clean kcp.log
// record and attacker-influenced content cannot forge extra log lines.
var newlineRE = regexp.MustCompile(`[\r\n]+`)

// ctrlRE matches C0 control characters (except tab, kept for table alignment)
// and DEL. Stripping them keeps kcp.log free of non-printable bytes.
var ctrlRE = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)

// Mirror emits a marker-bearing Info record into kcp.log. It writes to no
// terminal stream, so callers that already own the terminal write (e.g. the
// migration reporter, which writes to its own streams) can add a log leg
// without doubling their output. The line is sanitised first — ANSI SGR codes
// stripped, interior newlines flattened to spaces, and other control chars
// removed — so every mirror is a single clean record that cannot forge extra
// log lines (security review LOW-001). An empty result produces no record.
func Mirror(s string) {
	clean := ansiRE.ReplaceAllString(s, "")
	clean = newlineRE.ReplaceAllString(clean, " ")
	clean = ctrlRE.ReplaceAllString(clean, "")
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return
	}
	slog.Default().Info(clean, MirrorMarkerKey, true)
}

// Printf writes the formatted line to stdout (colour preserved) and mirrors it
// into kcp.log.
func Printf(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	_, _ = fmt.Fprint(os.Stdout, s)
	Mirror(s)
}

// Println writes the operands to stdout with spaces and a trailing newline and
// mirrors the line into kcp.log.
func Println(a ...any) {
	s := fmt.Sprintln(a...)
	_, _ = fmt.Fprint(os.Stdout, s)
	Mirror(s)
}

// Errf writes the formatted line to stderr and mirrors it into kcp.log.
func Errf(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	_, _ = fmt.Fprint(os.Stderr, s)
	Mirror(s)
}

// Errln writes the operands to stderr with spaces and a trailing newline and
// mirrors the line into kcp.log.
func Errln(a ...any) {
	s := fmt.Sprintln(a...)
	_, _ = fmt.Fprint(os.Stderr, s)
	Mirror(s)
}
