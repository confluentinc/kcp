package d2s

import (
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/confluentinc/kcp/internal/logging"
	"github.com/fatih/color"
)

// reporter owns the conversion's terminal output, with TBM's conventions (tbm/reporter.go): a cyan banner per
// step, indented ✔ and ↳ lines, ⚠️ cautions, and every line mirrored into kcp.log.
type reporter struct {
	out io.Writer
	err io.Writer
}

func newReporter() *reporter {
	return &reporter{out: os.Stdout, err: os.Stderr}
}

func (r *reporter) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(r.out, format, a...)
}

func (r *reporter) errf(format string, a ...any) {
	_, _ = fmt.Fprintf(r.err, format, a...)
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// mirror copies the plain (ANSI-stripped) narrative into kcp.log without doubling it onto the console.
func (r *reporter) mirror(msg string) {
	logging.File().Info(ansiRE.ReplaceAllString(msg, ""))
}

func (r *reporter) mirrorWarn(msg string) {
	logging.File().Warn(ansiRE.ReplaceAllString(msg, ""))
}

// section prints a blank line then a cyan banner announcing a step.
func (r *reporter) section(msg string) {
	r.printf("\n%s\n", color.CyanString(msg))
	r.mirror(msg)
}

// Success prints an indented green ✔ line. Exported so the reporter satisfies gateway.Reporter.
func (r *reporter) Success(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.printf("   %s %s\n", color.GreenString("✔"), msg)
	r.mirror(msg)
}

// Detail prints an indented ↳ progress line. Exported — see Success.
func (r *reporter) Detail(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.printf("   ↳ %s\n", msg)
	r.mirror(msg)
}

// warn prints an indented yellow ⚠️ line to stdout.
func (r *reporter) warn(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.printf("   %s %s\n", color.YellowString("⚠️"), msg)
	r.mirrorWarn(msg)
}

// Remediation prints a yellow ⚠️ note to stderr. Exported — see Success.
func (r *reporter) Remediation(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.errf("%s %s\n", color.YellowString("⚠️"), msg)
	r.mirrorWarn(msg)
}

// stepDone prints the per-step completion marker.
func (r *reporter) stepDone() {
	r.printf("%s\n", color.GreenString("✅ Done"))
}

// complete prints the final green banner.
func (r *reporter) complete(msg string) {
	r.printf("\n%s\n", color.GreenString(msg))
	r.mirror(msg)
}
