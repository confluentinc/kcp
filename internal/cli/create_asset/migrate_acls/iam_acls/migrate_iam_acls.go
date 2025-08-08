package iam_acls

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/generators/create_asset/migrate_acls/iam_acls"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	roleArn   string
	outputDir string
)

func NewMigrateIamAclsCmd() *cobra.Command {
	aclsCmd := &cobra.Command{
		Use:           "iam",
		Short:         "Convert IAM ACLs to Confluent Cloud IAM ACLs.",
		Long:          "Convert IAM ACLs to Confluent Cloud IAM ACLs as individual Terraform resources.",
		SilenceErrors: true,
		PreRunE:       preRunMigrateIamAcls,
		RunE:          runMigrateIamAcls,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&roleArn, "role-arn", "", "IAM Role ARN to convert ACLs from")
	aclsCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&outputDir, "output-dir", "", "The directory where the Confluent Cloud Terraform ACL assets will be written to")
	aclsCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	aclsCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	aclsCmd.MarkFlagRequired("role-arn")

	return aclsCmd
}

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
