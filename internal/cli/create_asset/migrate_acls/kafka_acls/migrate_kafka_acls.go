package kafka_acls

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/internal/generators/create_asset/migrate_acls/kafka_acls"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile       string
	clusterArn      string
	outputDir       string
	skipAuditReport bool
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
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&clusterArn, "cluster-arn", "", "The ARN of the MSK cluster to convert ACLs from.")
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
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	aclsCmd.MarkFlagRequired("cluster-file")

	return aclsCmd
}

func preRunConvertKafkaAcls(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runConvertKafkaAcls(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
	}

	var state types.State
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	cluster, err := utils.GetClusterByArn(&state, clusterArn)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}
	clusterName := cluster.Name

	if len(cluster.KafkaAdminClientInformation.Acls) == 0 {
		slog.Warn(fmt.Sprintf("⚠️ Cluster %s has no ACLs within the state file: %s", cluster.Name, stateFile))
		return nil
	}
	kafkaAcls := cluster.KafkaAdminClientInformation.Acls

	if err := kafka_acls.RunConvertKafkaAcls(clusterName, kafkaAcls, outputDir, skipAuditReport); err != nil {
		return fmt.Errorf("failed to convert Kafka ACLs: %v", err)
	}

	return nil
}
