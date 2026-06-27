package upgrade

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/spf13/cobra"
)

func NewStateUpgradeCmd() *cobra.Command {
	var in, out string
	cmd := &cobra.Command{
		Use:           "upgrade",
		Short:         "Migrate a kcp-state.json file to the current schema",
		Long:          "Reads a state file produced by any prior KCP version, migrates it to the current schema, and writes the result. Writes in place if --out is omitted.",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			state, err := types.NewStateFromFile(in)
			if err != nil {
				return err
			}
			dst := out
			if dst == "" {
				dst = in
			}
			if err := state.WriteToFile(dst); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "upgraded %s -> %s (schema_version stamped)\n", in, dst)
			return nil
		},
	}
	cmd.Flags().StringVar(&in, "in", "", "Path to the state file to upgrade (required)")
	cmd.Flags().StringVar(&out, "out", "", "Where to write the upgraded file (default: overwrite --in)")
	_ = cmd.MarkFlagRequired("in")
	return cmd
}
