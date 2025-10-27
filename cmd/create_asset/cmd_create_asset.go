package create_asset

import (
	"github.com/confluentinc/kcp/cmd/create_asset/bastion_host"
	"github.com/confluentinc/kcp/cmd/create_asset/migrate_acls"
	"github.com/confluentinc/kcp/cmd/create_asset/migrate_connectors"
	"github.com/confluentinc/kcp/cmd/create_asset/migrate_schemas"
	"github.com/confluentinc/kcp/cmd/create_asset/migrate_topics"
	"github.com/confluentinc/kcp/cmd/create_asset/migration_infra"
	"github.com/confluentinc/kcp/cmd/create_asset/reverse_proxy"
	"github.com/spf13/cobra"
)

func NewCreateAssetCmd() *cobra.Command {
	createAssetCmd := &cobra.Command{
		Use:   "create-asset",
		Short: "Generate infrastructure and migration assets",
		Long:  "Generate various infrastructure and migration assets including bastion host configurations, data migration tools, and target environment setups.",
	}

	// Add subcommands
	createAssetCmd.AddCommand(
		bastion_host.NewBastionHostCmd(),
		migrate_acls.NewMigrateAclsCmd(),
		migrate_connectors.NewMigrateConnectorsCmd(),
		migrate_topics.NewMigrateTopicsCmd(),
		migrate_schemas.NewMigrateSchemasCmd(),
		migration_infra.NewMigrationInfraCmd(),
		reverse_proxy.NewReverseProxyCmd(),
	)

	return createAssetCmd
}
