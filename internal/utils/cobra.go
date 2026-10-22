package utils

import "github.com/spf13/cobra"

// NewNounCmd wraps verb in an empty parent command named use, so that
// `kcp iac topics generate` has a `topics` level between `iac` and `generate`.
// Cobra needs a command for every word in the path, but only the verb does any
// work; the parent just routes to it.
//
// The parent copies verb's Short so it still has a description in the grandparent's
// help, and verb's Hidden so a hidden verb doesn't leave a visible, empty group.
func NewNounCmd(use string, verb *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:           use,
		Short:         verb.Short,
		Hidden:        verb.Hidden,
		SilenceErrors: true,
	}
	cmd.AddCommand(verb)
	return cmd
}
