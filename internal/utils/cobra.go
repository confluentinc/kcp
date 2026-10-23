package utils

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewNounCmd wraps verb in a parent command named use, so that
// `kcp iac topics generate` has a `topics` level between `iac` and `generate`.
// Cobra needs a command for every word in the path, but only the verb does any
// work; the parent just routes to it.
//
// The parent is runnable only so that a missing or mistyped verb fails: cobra
// answers a non-runnable command with its help and exit status 0, which would
// leave `kcp iac topics genrate` looking successful while writing nothing. Both
// failures are raised from Args because cobra validates args before the root's
// PersistentPreRun, so a usage error prints no banner and writes no kcp.log;
// RunE is never reached.
//
// The parent copies verb's Short so it still has a description in the grandparent's
// help, and verb's Hidden because cobra lists a runnable parent even when its only
// child is hidden.
func NewNounCmd(use string, verb *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:           use,
		Short:         verb.Short,
		Hidden:        verb.Hidden,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("missing sub-command for %q, did you mean %q?", cmd.CommandPath(), verb.CommandPath())
			}
			return cobra.NoArgs(cmd, args)
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	cmd.AddCommand(verb)
	return cmd
}
