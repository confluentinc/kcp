package utils

import "github.com/spf13/cobra"

// NewNounCmd nests verb under a grouping command named use, giving the
// `kcp <noun>... <verb>` shape. The group borrows verb's Short so its line in
// the parent's help still says what it does, and verb's Hidden so a hidden verb
// doesn't leave an empty, visible noun behind.
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
