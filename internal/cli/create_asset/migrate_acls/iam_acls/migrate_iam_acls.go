package iam_acls

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/generators/create_asset/migrate_acls/iam_acls"
	"github.com/confluentinc/kcp/internal/utils"
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
		Long: `Convert IAM ACLs to Confluent Cloud IAM ACLs as individual Terraform resources.

All flags can be provided via environment variables (uppercase, with underscores):

FLAG                     | ENV_VAR
-------------------------|---------------------------
--role-arn               | ROLE_ARN=arn:aws:iam::123456789012:role/my-role
--output-dir             | OUTPUT_DIR=path/to/output
`,
		SilenceErrors: true,
		PreRunE:       preRunMigrateIamAcls,
		RunE:          runMigrateIamAcls,
	}

	aclsCmd.Flags().StringVar(&roleArn, "role-arn", "", "IAM Role ARN to convert ACLs from")
	aclsCmd.Flags().StringVar(&outputDir, "output-dir", "", "The directory to write the ACL files to")

	aclsCmd.MarkFlagRequired("role-arn")
	aclsCmd.MarkFlagRequired("output-dir")

	return aclsCmd
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func preRunMigrateIamAcls(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrateIamAcls(cmd *cobra.Command, args []string) error {
	if err := iam_acls.RunConvertIamAcls(roleArn, outputDir); err != nil {
		return fmt.Errorf("failed to convert IAM ACLs: %v", err)
	}

	return nil
}
