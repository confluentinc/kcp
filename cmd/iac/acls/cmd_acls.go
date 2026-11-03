package acls

import (
	"github.com/confluentinc/kcp/cmd/iac/acls/iam_acls"
	"github.com/confluentinc/kcp/cmd/iac/acls/kafka_acls"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
)

func NewAclsCmd() *cobra.Command {
	aclsCmd := utils.NewGroupCmd(&cobra.Command{
		Use:   "acls",
		Short: "Migrate ACLs from MSK to Confluent Cloud",
		Long: `Migrate ACLs (Kafka and IAM) from MSK to executable Terraform assets for Confluent Cloud.

This command provides subcommands to convert both Kafka ACLs and IAM ACLs to Confluent Cloud compatible formats.`,
	})

	aclsCmd.AddCommand(
		utils.NewNounCmd("kafka", kafka_acls.NewGenerateCmd()),
		utils.NewNounCmd("iam", iam_acls.NewGenerateCmd()),
	)

	return aclsCmd
}
