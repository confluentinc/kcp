package migration

import (
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/confluentinc/kcp/internal/logging"
	"github.com/fatih/color"
)

// reporter owns all user-facing terminal output for the migration flow so the
// orchestrator, workflow and offset-sync bookends don't scatter raw
// fmt.Printf/Fprintf/color calls. It centralises the indentation and
// colour conventions and the destination streams (stdout for progress, stderr
// for soft-fail remediation notes), giving migration output a single owner and
// a single point to redirect or silence.
type reporter struct {
	out io.Writer
	err io.Writer
}

// newReporter returns a reporter that writes progress to stdout and remediation
// notes to stderr.
func newReporter() *reporter {
	return &reporter{out: os.Stdout, err: os.Stderr}
}

// printf writes to the progress stream. Write errors to a terminal are not
// actionable, so they are intentionally discarded here (once) rather than at
// every call site.
func (r *reporter) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(r.out, format, a...)
}

// errf writes to the remediation stream, discarding the unactionable error.
func (r *reporter) errf(format string, a ...any) {
	_, _ = fmt.Fprintf(r.err, format, a...)
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// mirror copies the plain (ANSI-stripped) narrative into kcp.log via the
// file-only sink. It never reaches the console, so it cannot double the fmt
// write it accompanies. Terminal formatting stays with the fmt/color call;
// the log gets a clean, greppable, timestamped copy.
func (r *reporter) mirror(msg string) {
	logging.File().Info(ansiRE.ReplaceAllString(msg, ""))
}

// mirrorWarn is mirror at Warn level, for the in-flow caution helpers.
func (r *reporter) mirrorWarn(msg string) {
	logging.File().Warn(ansiRE.ReplaceAllString(msg, ""))
}

// section prints a blank line then a cyan banner announcing a major step. The
// caller supplies the section text in msg (e.g. "Initializing migration...").
func (r *reporter) section(msg string) {
	r.printf("\n%s\n", color.CyanString(msg))
	r.mirror(msg)
}

// success prints an indented green [OK] line. The text may embed its own colour.
func (r *reporter) success(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.printf("   %s %s\n", color.GreenString("[OK]"), msg)
	r.mirror(msg)
}

// detail prints an indented ↳ progress line.
func (r *reporter) detail(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.printf("   ↳ %s\n", msg)
	r.mirror(msg)
}

// warn prints an indented yellow [WARN] line to stdout (in-flow caution).
func (r *reporter) warn(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.printf("   %s %s\n", color.YellowString("[WARN]"), msg)
	r.mirrorWarn(msg)
}

// remediation prints a yellow [WARN] soft-fail note to stderr. The body may contain
// newlines for indented continuation lines; it is not indented on the first
// line. Used by the offset-sync bookends, whose failures are surfaced as
// operator guidance without aborting a successful migration.
func (r *reporter) remediation(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.errf("%s %s\n", color.YellowString("[WARN]"), msg)
	r.mirrorWarn(msg)
}

// stepDone prints the per-step completion marker.
func (r *reporter) stepDone() {
	r.printf("%s\n", color.GreenString("Done"))
}

// complete prints the final green migration-complete banner (blank line first).
func (r *reporter) complete(msg string) {
	r.printf("\n%s\n", color.GreenString(msg))
	r.mirror(msg)
}

// blank writes a single blank line.
func (r *reporter) blank() {
	r.printf("\n")
}

// line writes a pre-composed line (plus newline) through the reporter's stdout.
// Used by the few rich multi-colour rows (lag / promotion tables) that don't fit
// a semantic helper but should still route through the single output owner.
func (r *reporter) line(s string) {
	r.printf("%s\n", s)
	r.mirror(s)
}
