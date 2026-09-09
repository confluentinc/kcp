package tbm

import (
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/confluentinc/kcp/internal/logging"
	"github.com/fatih/color"
)

// reporter owns all user-facing terminal output for the TBM flow, mirroring
// migration.reporter: it centralises the emoji, indentation and colour
// conventions and the destination streams (stdout for progress, stderr for
// soft-fail remediation notes), giving TBM output a single owner.
type reporter struct {
	out io.Writer
	err io.Writer
}

// newReporter returns a reporter that writes progress to stdout and
// remediation notes to stderr.
func newReporter() *reporter {
	return &reporter{out: os.Stdout, err: os.Stderr}
}

func (r *reporter) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(r.out, format, a...)
}

// errf writes to the remediation stream, discarding the unactionable error.
func (r *reporter) errf(format string, a ...any) {
	_, _ = fmt.Fprintf(r.err, format, a...)
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// mirror copies the plain (ANSI-stripped) narrative into kcp.log via the
// file-only sink, without doubling onto the console.
func (r *reporter) mirror(msg string) {
	logging.File().Info(ansiRE.ReplaceAllString(msg, ""))
}

// mirrorWarn is mirror at Warn level, for the in-flow caution helpers.
func (r *reporter) mirrorWarn(msg string) {
	logging.File().Warn(ansiRE.ReplaceAllString(msg, ""))
}

// section prints a blank line then a cyan banner announcing a major step.
func (r *reporter) section(msg string) {
	r.printf("\n%s\n", color.CyanString(msg))
	r.mirror(msg)
}

// success prints an indented green-✔ line.
func (r *reporter) success(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.printf("   %s %s\n", color.GreenString("✔"), msg)
	r.mirror(msg)
}

// detail prints an indented ↳ progress line.
func (r *reporter) detail(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.printf("   ↳ %s\n", msg)
	r.mirror(msg)
}

// warn prints an indented yellow-⚠️ line to stdout (in-flow caution).
func (r *reporter) warn(format string, a ...any) { //nolint:unused // reserved for a future in-flow caution (verify_fence); not yet called
	msg := fmt.Sprintf(format, a...)
	r.printf("   %s %s\n", color.YellowString("⚠️"), msg)
	r.mirrorWarn(msg)
}

// remediation prints a yellow-⚠️ soft-fail note to stderr. The body may
// contain newlines for indented continuation lines; it is not indented on the
// first line.
func (r *reporter) remediation(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.errf("%s %s\n", color.YellowString("⚠️"), msg)
	r.mirrorWarn(msg)
}

// stepDone prints the per-step completion marker.
func (r *reporter) stepDone() {
	r.printf("%s\n", color.GreenString("✅ Done"))
}

// complete prints the final green completion banner (blank line first).
func (r *reporter) complete(msg string) {
	r.printf("\n%s\n", color.GreenString(msg))
	r.mirror(msg)
}

// blank writes a single blank line.
func (r *reporter) blank() {
	r.printf("\n")
}

// line writes a pre-composed line (plus newline) through the reporter's
// stdout. Used by the few rich multi-colour rows (lag) that don't fit a
// semantic helper but should still route through the single output owner.
func (r *reporter) line(s string) {
	r.printf("%s\n", s)
	r.mirror(s)
}
