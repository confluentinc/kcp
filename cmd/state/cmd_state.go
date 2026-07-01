package state

import (
	"github.com/confluentinc/kcp/cmd/state/upgrade"
	"github.com/confluentinc/kcp/cmd/state/version"
	"github.com/spf13/cobra"
)

func NewStateCmd() *cobra.Command {
	stateCmd := &cobra.Command{
		Use:           "state",
		Short:         "Operate on kcp-state.json files",
		Long:          "Commands for inspecting and migrating KCP state files.",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}
	stateCmd.AddCommand(
		upgrade.NewStateUpgradeCmd(),
		version.NewStateVersionCmd(),
	)
	return stateCmd
}
