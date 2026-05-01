package migrate_connectors

import (
	connector_utility "github.com/confluentinc/kcp/cmd/create_asset/migrate_connectors/connector_utility"
	msk_connectors "github.com/confluentinc/kcp/cmd/create_asset/migrate_connectors/msk"
	self_managed_connectors "github.com/confluentinc/kcp/cmd/create_asset/migrate_connectors/self_managed"

	"github.com/spf13/cobra"
)

func NewMigrateConnectorsCmd() *cobra.Command {
	migrateConnectorsCmd := &cobra.Command{
		Use:   "migrate-connectors",
		Short: "Migrate connectors to Confluent Cloud",
		Long: `Migrate connectors to Confluent Cloud.

This command translates MSK Connect and self-managed Kafka Connect connector configurations into Confluent Cloud fully-managed connector resources, using Confluent Cloud's translation API.

**Prerequisites:**

- A provisioned Confluent Cloud environment and target cluster.
- A Cloud API key/secret with the ` + "`Cloud Resource Management`" + ` scope. The translation step calls the ` + "`.../translate/config`" + ` Confluent Cloud API endpoint to convert each self-managed connector config to its fully-managed equivalent.`,
		SilenceErrors: true,
	}

	migrateConnectorsCmd.AddCommand(
		self_managed_connectors.NewMigrateSelfManagedConnectorsCmd(),
		msk_connectors.NewMigrateMskConnectorsCmd(),
		connector_utility.NewConnectorUtilityCmd(),
	)

	return migrateConnectorsCmd
}
