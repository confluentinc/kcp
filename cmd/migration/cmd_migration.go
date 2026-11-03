package migration

import (
	"github.com/confluentinc/kcp/cmd/migration/cutover"
	"github.com/confluentinc/kcp/cmd/migration/resources"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
)

func NewMigrationCmd() *cobra.Command {
	migrationCmd := utils.NewGroupCmd(&cobra.Command{
		Use:   "migration",
		Short: "Commands for migrating clients using the Confluent Gateway and Kafka resources directly.",
		Long: `Migrate Kafka workloads to Confluent Cloud.

- ` + "`kcp migration cutover`" + ` — cut clients over through the Confluent Gateway, promoting cluster-linked mirror topics.
- ` + "`kcp migration resources`" + ` — create Kafka resources (cluster links, topics, service accounts, ACLs) directly against Confluent Cloud or Confluent Platform.`,
	})

	migrationCmd.AddCommand(
		cutover.NewMigrationCutoverCmd(),
		resources.NewMigrationResourcesCmd(),
	)

	return migrationCmd
}
