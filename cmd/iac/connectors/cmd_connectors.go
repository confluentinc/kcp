package connectors

import (
	connector_utility "github.com/confluentinc/kcp/cmd/iac/connectors/connector_utility"
	msk_connectors "github.com/confluentinc/kcp/cmd/iac/connectors/msk"
	self_managed_connectors "github.com/confluentinc/kcp/cmd/iac/connectors/self_managed"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

func NewConnectorsCmd() *cobra.Command {
	connectorsCmd := &cobra.Command{
		Use:   "connectors",
		Short: "Migrate connectors to Confluent Cloud",
		Long: `Migrate connectors to Confluent Cloud.

This command translates MSK Connect and self-managed Kafka Connect connector configurations into Confluent Cloud fully-managed connector resources, using Confluent Cloud's translation API.

**Prerequisites:**

- A provisioned Confluent Cloud environment and target cluster.
- A Cloud API key/secret with the correct cluster scope. The translation step calls the ` + "`.../translate/config`" + ` Confluent Cloud API endpoint to convert each self-managed connector config to its fully-managed equivalent.`,
		SilenceErrors: true,
	}

	connectorsCmd.AddCommand(
		utils.NewNounCmd("self-managed", self_managed_connectors.NewGenerateCmd()),
		utils.NewNounCmd("msk", msk_connectors.NewGenerateCmd()),
		utils.NewNounCmd("connector-utility", connector_utility.NewGenerateCmd()),
	)

	return connectorsCmd
}
