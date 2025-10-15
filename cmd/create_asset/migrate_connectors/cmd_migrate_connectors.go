package migrate_connectors

import (
	self_managed_connectors "github.com/confluentinc/kcp/cmd/create_asset/migrate_connectors/self_managed"

	"github.com/spf13/cobra"
)

func NewMigrateConnectorsCmd() *cobra.Command {
	migrateConnectorsCmd := &cobra.Command{
		Use:   "migrate-connectors",
		Short: "Migrate connectors to Confluent Cloud",
		Long: `Migrate connectors to Confluent Cloud.

This command translates self-managed connector configurations to Confluent Cloud compatible formats using the Confluent Cloud API.`,
		SilenceErrors: true,
	}

	migrateConnectorsCmd.AddCommand(
		self_managed_connectors.NewMigrateSelfManagedConnectorsCmd(),
	)

	return migrateConnectorsCmd
}
