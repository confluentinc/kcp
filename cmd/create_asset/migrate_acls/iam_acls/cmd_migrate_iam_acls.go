package iam_acls

import (
	"fmt"
	"slices"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	roleArn         string
	userArn         string
	stateFile       string
	clusterArn      string
	outputDir       string
	skipAuditReport bool
	targetClusterId           string
	targetClusterRestEndpoint string
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
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file.")
	requiredFlags.StringVar(&clusterArn, "cluster-arn", "", "The ARN of the cluster to migrate ACLs from.")
	requiredFlags.StringVar(&targetClusterId, "target-cluster-id", "", "The Confluent Cloud cluster ID (e.g., lkc-xxxxxx).")
	requiredFlags.StringVar(&targetClusterRestEndpoint, "target-rest-endpoint", "", "The Confluent Cloud cluster REST endpoint (e.g., https://xxx.xxx.aws.confluent.cloud:443).")
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
		groupNames := []string{"Required Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
				if groupNames[i] == "Required Flags" {
					fmt.Printf("  (Provide either --role-arn OR --user-arn OR --state-file)\n")
					fmt.Printf("  (If --state-file is provided, --cluster-arn is also required)\n\n")
				}
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	aclsCmd.MarkFlagsOneRequired("role-arn", "user-arn", "state-file")
	aclsCmd.MarkFlagsMutuallyExclusive("role-arn", "user-arn", "state-file")
	aclsCmd.MarkFlagsRequiredTogether("state-file", "cluster-arn")
	aclsCmd.MarkFlagRequired("target-cluster-id")
	aclsCmd.MarkFlagRequired("target-cluster-rest-endpoint")

	return aclsCmd
}

func preRunMigrateIamAcls(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrateIamAcls(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrateIamAclsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migrate IAM ACLs opts: %v", err)
	}

	iamAclsGenerator := NewIamAclsGenerator(*opts)
	if err := iamAclsGenerator.Run(); err != nil {
		return fmt.Errorf("failed to migrate IAM ACLs: %v", err)
	}

	return nil
}

func parseMigrateIamAclsOpts() (*MigrateIamAclsOpts, error) {
	var principalArns []string

	switch {
	case roleArn != "":
		principalArns = []string{roleArn}
	case userArn != "":
		principalArns = []string{userArn}
	case stateFile != "":
		state, err := types.NewStateFromFile(stateFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load existing state file: %v", err)
		}
		principals, err := parseClientDiscoveryFile(clusterArn, state)
		if err != nil {
			return nil, fmt.Errorf("failed to parse client discovery file: %v", err)
		}

		if outputDir == "" {
			outputDir = "client-discovery-acls"
		}

		principalArns = principals
	}

	opts := MigrateIamAclsOpts{
		PrincipalArns:             principalArns,
		TargetClusterId:           targetClusterId,
		TargetClusterRestEndpoint: targetClusterRestEndpoint,
		OutputDir:                 outputDir,
		SkipAuditReport:           skipAuditReport,
	}

	return &opts, nil
}

func parseClientDiscoveryFile(clusterArn string, state *types.State) ([]string, error) {
	cluster, err := state.GetClusterByArn(clusterArn)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	var principals []string
	for _, client := range cluster.DiscoveredClients {
		if client.Auth == "IAM" {
			principals = append(principals, client.Principal)
		}
	}

	principalArns, err := evaluatePrincipal(principals)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate principal: %v", err)
	}

	return principalArns, nil
}

// arn:aws:sts::000123456789:assumed-role/kcp-iam-role/i-0ab123456cdef7890
// arn:aws:iam::000123456789:role/kcp-iam-role
func evaluatePrincipal(discoveredPrincipals []string) ([]string, error) {
	var principalArns []string

	for _, principal := range discoveredPrincipals {
		arn := strings.Replace(principal, "arn:aws:sts::", "arn:aws:iam::", 1)
		arn = strings.Replace(arn, ":assumed-role/", ":role/", 1)

		parts := strings.Split(arn, "/")
		if len(parts) > 2 {
			arn = strings.Join(parts[:2], "/")
		}

		if slices.Contains(principalArns, arn) {
			continue
		}

		principalArns = append(principalArns, arn)
	}

	return principalArns, nil
}
