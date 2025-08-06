package migrate_acls

import (
	"github.com/confluentinc/kcp/internal/cli/create_asset/migrate_acls/iam_acls"
	"github.com/confluentinc/kcp/internal/cli/create_asset/migrate_acls/kafka_acls"

	"github.com/spf13/cobra"
)

func NewMigrateAclsCmd() *cobra.Command {
	migrateAclsCmd := &cobra.Command{
		Use:   "migrate-acls",
		Short: "Migrate ACLs from MSK to Confluent Cloud",
		Long:  "Migrate ACLs (Kafka and IAM) from MSK to executable Terraform assets for Confluent Cloud.",
	}

	migrateAclsCmd.AddCommand(
		kafka_acls.NewConvertKafkaAclsCmd(),
		iam_acls.NewMigrateIamAclsCmd(),
	)

	return migrateAclsCmd
}
