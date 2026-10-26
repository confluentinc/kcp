package utils

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
//
// Unknown flags are tolerated on the parent so that `kcp iac topics --state-file x`
// (a pre-rename invocation with the verb dropped) reaches the missing-verb hint
// instead of failing on the flag, which cobra parses before it validates Args.
func NewNounCmd(use string, verb *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:                use,
		Short:              verb.Short,
		Hidden:             verb.Hidden,
		SilenceErrors:      true,
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
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

// NewGroupCmd makes cmd, a command that only routes to sub-commands, fail on a
// mistyped one. Cobra answers an unknown argument to a non-runnable command with
// its help and exit status 0, so `kcp iac acls iamm` would look successful while
// doing nothing. A bare invocation still prints help and exits 0: Args returns
// pflag.ErrHelp, which cobra turns into help before the root's PersistentPreRun.
func NewGroupCmd(cmd *cobra.Command) *cobra.Command {
	cmd.SilenceErrors = true
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return pflag.ErrHelp
		}
		return cobra.NoArgs(cmd, args)
	}
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	return cmd
}
