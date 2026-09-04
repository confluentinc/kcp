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
// conventions and the destination stream, giving TBM output a single owner.
type reporter struct {
	out io.Writer
}

// newReporter returns a reporter that writes progress to stdout.
func newReporter() *reporter {
	return &reporter{out: os.Stdout}
}

func (r *reporter) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(r.out, format, a...)
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// mirror copies the plain (ANSI-stripped) narrative into kcp.log via the
// file-only sink, without doubling onto the console.
func (r *reporter) mirror(msg string) {
	logging.File().Info(ansiRE.ReplaceAllString(msg, ""))
}

// section prints a blank line then a cyan banner announcing a major step.
func (r *reporter) section(msg string) { //nolint:unused // used by orchestrator (Task 3)
	r.printf("\n%s\n", color.CyanString(msg))
	r.mirror(msg)
}

// success prints an indented green-✔ line.
func (r *reporter) success(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.printf("   %s %s\n", color.GreenString("✔"), msg)
	r.mirror(msg)
}

// stepDone prints the per-step completion marker.
func (r *reporter) stepDone() { //nolint:unused // used by orchestrator (Task 3)
	r.printf("%s\n", color.GreenString("✅ Done"))
}

// complete prints the final green completion banner (blank line first).
func (r *reporter) complete(msg string) { //nolint:unused // used by orchestrator (Task 3)
	r.printf("\n%s\n", color.GreenString(msg))
	r.mirror(msg)
}
