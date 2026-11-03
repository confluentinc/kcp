package upgrade

import (
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/spf13/cobra"
)

func NewStateUpgradeCmd() *cobra.Command {
	var stateFile string
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Migrate a kcp-state.json file to the current schema",
		Long:  "Reads a state file produced by any prior KCP version, migrates it to the current schema, and overwrites it in place. Before overwriting, the original is backed up alongside it as <state-file>.<UTC-timestamp>.bak (a file already at the current schema is left unchanged, with no backup).",
		Example: `  # Migrate a state file to the current schema, overwriting it in place
  # (the original is preserved as kcp-state.json.<UTC-timestamp>.bak)
  kcp state upgrade --state-file kcp-state.json`,
		SilenceErrors: true,
		SilenceUsage:  true, // a load/runtime error is not a usage error — don't dump the flags
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			slog.Info("upgrading state file to current schema", "path", stateFile)
			state, err := types.NewStateFromFile(stateFile)
			if err != nil {
				return err
			}
			if err := state.WriteToFile(stateFile); err != nil {
				return err
			}
			slog.Info("upgraded state file", "path", stateFile)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "upgraded %s (schema_version stamped)\n", stateFile)
			return nil
		},
	}
	cmd.Flags().StringVar(&stateFile, "state-file", "", "Path to the state file to upgrade in place (required)")
	_ = cmd.MarkFlagRequired("state-file")
	return cmd
}
