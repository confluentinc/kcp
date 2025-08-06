package iam_acls

import (
	"github.com/confluentinc/kcp/internal/generators/create_asset/migrate_acls/iam_acls"

	"github.com/spf13/cobra"
)

var (
	roleArn   string
	outputDir string
)

func NewMigrateIamAclsCmd() *cobra.Command {
	aclsCmd := &cobra.Command{
		Use:   "iam-acls",
		Short: "Convert IAM ACLs to Confluent Cloud IAM ACLs.",
		Long:  "Convert IAM ACLs to Confluent Cloud IAM ACLs as individual Terraform resources.",

		RunE: func(cmd *cobra.Command, args []string) error {
			roleArn, _ = cmd.Flags().GetString("role-arn")

			return iam_acls.RunConvertIamAcls(roleArn, outputDir)
		},
	}

	aclsCmd.Flags().StringVar(&roleArn, "role-arn", "", "IAM Role ARN to convert ACLs from (required)")
	aclsCmd.Flags().StringVar(&outputDir, "output-dir", "", "The directory to write the ACL files to")

	aclsCmd.MarkFlagRequired("role-arn")
	aclsCmd.MarkFlagRequired("output-dir")

	return aclsCmd
}
