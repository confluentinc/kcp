package iam_acls

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewConvertIamAclsCmd() *cobra.Command {
	aclsCmd := &cobra.Command{
		Use:   "iam-acls",
		Short: "Convert IAM ACLs to Confluent Cloud IAM ACLs.",
		Long: `Convert IAM ACLs to Confluent Cloud IAM ACLs as individual Terraform resources.

		TODO: ...

`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("test")

			return runConvertIamAcls()
		},
	}

	return aclsCmd
}

func runConvertIamAcls() error {
	return nil
}