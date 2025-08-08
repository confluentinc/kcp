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
	userArn   string
	outputDir string
	skipAuditReport bool
)

func NewMigrateIamAclsCmd() *cobra.Command {
	aclsCmd := &cobra.Command{
		Use:           "iam",
		Short:         "Convert IAM ACLs to Confluent Cloud IAM ACLs.",
		Long:          "Convert IAM ACLs from IAM roles or users to Confluent Cloud IAM ACLs as individual Terraform resources.",
		SilenceErrors: true,
		PreRunE:       preRunMigrateIamAcls,
		RunE:          runMigrateIamAcls,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&roleArn, "role-arn", "", "IAM Role ARN to convert ACLs from")
	requiredFlags.StringVar(&userArn, "user-arn", "", "IAM User ARN to convert ACLs from")
	aclsCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&outputDir, "output-dir", "", "The directory where the Confluent Cloud Terraform ACL assets will be written to")
	optionalFlags.BoolVar(&skipAuditReport, "skip-audit-report", false, "Skip generating an audit report of the converted ACLs")
	aclsCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	aclsCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags (choose one)", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	aclsCmd.MarkFlagsOneRequired("role-arn", "user-arn")
	aclsCmd.MarkFlagsMutuallyExclusive("role-arn", "user-arn")

	return aclsCmd
}

func preRunMigrateIamAcls(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrateIamAcls(cmd *cobra.Command, args []string) error {
	var principalArn string
	if roleArn != "" {
		principalArn = roleArn
	} else {
		principalArn = userArn
	}

	if err := iam_acls.RunConvertIamAcls(principalArn, outputDir, skipAuditReport); err != nil {
		return fmt.Errorf("failed to convert IAM ACLs: %v", err)
	}

	return nil
}
