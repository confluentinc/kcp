package kafka_acls

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile                 string
	clusterArn                string
	sourceType                string
	oskClusterId              string
	targetClusterId           string
	targetClusterRestEndpoint string
	outputDir                 string
	skipAuditReport           bool
)

func NewConvertKafkaAclsCmd() *cobra.Command {
	aclsCmd := &cobra.Command{
		Use:           "kafka",
		Short:         "Convert Kafka ACLs to Confluent Cloud ACLs.",
		Long:          "Convert Kafka ACLs to Confluent Cloud ACLs as individual Terraform resources.",
		SilenceErrors: true,
		PreRunE:       preRunConvertKafkaAcls,
		RunE:          runConvertKafkaAcls,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the cluster discovery reports have been written to.")
	requiredFlags.StringVar(&targetClusterId, "target-cluster-id", "", "The Confluent Cloud cluster ID (e.g., lkc-xxxxxx).")
	requiredFlags.StringVar(&targetClusterRestEndpoint, "target-rest-endpoint", "", "The Confluent Cloud cluster REST endpoint (e.g., https://xxx.xxx.aws.confluent.cloud:443).")
	aclsCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	sourceFlags := pflag.NewFlagSet("source", pflag.ExitOnError)
	sourceFlags.SortFlags = false
	sourceFlags.StringVar(&sourceType, "source-type", "msk", "The source type (msk or osk).")
	sourceFlags.StringVar(&clusterArn, "cluster-arn", "", "The ARN of the MSK cluster to convert ACLs from (required when --source-type is msk).")
	sourceFlags.StringVar(&oskClusterId, "cluster-id", "", "The ID of the OSK cluster to convert ACLs from (required when --source-type is osk).")
	aclsCmd.Flags().AddFlagSet(sourceFlags)
	groups[sourceFlags] = "Source Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&outputDir, "output-dir", "", "The directory where the Confluent Cloud Terraform ACL assets will be written to")
	optionalFlags.BoolVar(&skipAuditReport, "skip-audit-report", false, "Skip generating an audit report of the converted ACLs")
	aclsCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	aclsCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, sourceFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Source Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	aclsCmd.MarkFlagRequired("state-file")
	aclsCmd.MarkFlagRequired("target-cluster-id")
	aclsCmd.MarkFlagRequired("target-cluster-rest-endpoint")

	return aclsCmd
}

func preRunConvertKafkaAcls(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runConvertKafkaAcls(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrateKafkaAclsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migrate Kafka ACLs opts: %v", err)
	}

	kafkaAclsGenerator := NewKafkaAclsGenerator(*opts)
	if err := kafkaAclsGenerator.Run(); err != nil {
		return fmt.Errorf("failed to migrate Kafka ACLs: %v", err)
	}

	return nil
}

func parseMigrateKafkaAclsOpts() (*MigrateKafkaAclsOpts, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
	}

	var state types.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	var kafkaAdminInfo *types.KafkaAdminClientInformation
	var clusterName string

	switch sourceType {
	case "msk":
		if clusterArn == "" {
			return nil, fmt.Errorf("--cluster-arn is required when --source-type is msk")
		}
		cluster, err := state.GetClusterByArn(clusterArn)
		if err != nil {
			return nil, fmt.Errorf("failed to get cluster: %w", err)
		}
		kafkaAdminInfo = &cluster.KafkaAdminClientInformation
		clusterName = cluster.Name
	case "osk":
		if oskClusterId == "" {
			return nil, fmt.Errorf("--cluster-id is required when --source-type is osk")
		}
		cluster, err := state.GetOSKClusterByID(oskClusterId)
		if err != nil {
			return nil, fmt.Errorf("failed to get OSK cluster: %w", err)
		}
		kafkaAdminInfo = &cluster.KafkaAdminClientInformation
		clusterName = cluster.ID
	default:
		return nil, fmt.Errorf("invalid --source-type: %s (must be 'msk' or 'osk')", sourceType)
	}

	if len(kafkaAdminInfo.Acls) == 0 {
		return nil, fmt.Errorf("cluster %s has no ACLs within the state file: %s", clusterName, stateFile)
	}

	opts := MigrateKafkaAclsOpts{
		ClusterName:               clusterName,
		KafkaAcls:                 kafkaAdminInfo.Acls,
		TargetClusterId:           targetClusterId,
		TargetClusterRestEndpoint: targetClusterRestEndpoint,
		OutputDir:                 outputDir,
		SkipAuditReport:           skipAuditReport,
	}

	return &opts, nil
}
