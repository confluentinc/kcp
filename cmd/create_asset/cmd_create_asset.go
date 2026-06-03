package create_asset

import (
	"github.com/confluentinc/kcp/cmd/create_asset/registry"
	"github.com/spf13/cobra"
)

func NewCreateAssetCmd() *cobra.Command {
	createAssetCmd := &cobra.Command{
		Use:   "create-asset",
		Short: "Generate infrastructure and migration assets",
		Long:  "Generate various infrastructure and migration assets including bastion host configurations, data migration tools, and target environment setups.",
	}

	// Subcommands self-register via their package init()s (see register_base.go
	// and the edition-gated register_full.go). The set present here is therefore
	// determined by which subcommand packages are compiled into this edition.
	createAssetCmd.AddCommand(registry.Commands()...)

	return createAssetCmd
}
