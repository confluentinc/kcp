package msk_connectors

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/services/iampolicy"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile       string
	clusterId       string
	ccClusterId     string
	ccEnvironmentId string
	ccApiKey        string
	ccApiSecret     string
	outputDir       string
)

func NewMigrateMskConnectorsCmd() *cobra.Command {
	mskConnectorsCmd := &cobra.Command{
		Use:   "msk",
		Short: "Migrate MSK Connect connectors to Confluent Cloud",
		Long:  "Generate Terraform configuration that recreates MSK Connect connectors as Confluent Cloud fully-managed connectors. Uses the Confluent translate/config API to convert connector configs.",
		Example: `  kcp create-asset migrate-connectors msk \
      --state-file kcp-state.json \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --cc-environment-id env-a1bcde \
      --cc-cluster-id lkc-xyz123 \
      --cc-api-key ABCDEFGHIJKLMNOP \
      --cc-api-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx`,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: "```json\n" +
				`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "kafkaconnect:ListConnectors",
        "kafkaconnect:DescribeConnector",
        "kafkaconnect:DescribeWorkerConfiguration",
        "kafkaconnect:DescribeCustomPlugin"
      ],
      "Resource": "*"
    }
  ]
}` + "\n```\n",
		},
		SilenceErrors: true,
		PreRunE:       preRunMigrateMskConnectors,
		RunE:          runMigrateMskConnectors,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the cluster discovery reports have been written to.")
	requiredFlags.StringVar(&clusterId, "cluster-id", "", "The ARN of the MSK cluster.")
	requiredFlags.StringVar(&ccEnvironmentId, "cc-environment-id", "", "The ID of the Confluent Cloud environment to migrate connectors to.")
	requiredFlags.StringVar(&ccClusterId, "cc-cluster-id", "", "The ID of the Confluent Cloud cluster to migrate connectors to.")
	requiredFlags.StringVar(&ccApiKey, "cc-api-key", "", "The API key for the Confluent Cloud cluster to migrate connectors to.")
	requiredFlags.StringVar(&ccApiSecret, "cc-api-secret", "", "The API secret for the Confluent Cloud cluster to migrate connectors to.")
	mskConnectorsCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&outputDir, "output-dir", "", "The directory where the Confluent Cloud Terraform connector assets will be written to")
	mskConnectorsCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	mskConnectorsCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	_ = mskConnectorsCmd.MarkFlagRequired("state-file")
	_ = mskConnectorsCmd.MarkFlagRequired("cluster-id")
	_ = mskConnectorsCmd.MarkFlagRequired("cc-environment-id")
	_ = mskConnectorsCmd.MarkFlagRequired("cc-cluster-id")
	_ = mskConnectorsCmd.MarkFlagRequired("cc-api-key")
	_ = mskConnectorsCmd.MarkFlagRequired("cc-api-secret")

	return mskConnectorsCmd
}

func preRunMigrateMskConnectors(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrateMskConnectors(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrateMskConnectorsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migrate MSK Connect connectors opts: %v", err)
	}

	mskConnectorsMigrator := NewMskConnectorMigrator(*opts)
	if err := mskConnectorsMigrator.Run(); err != nil {
		return fmt.Errorf("failed to migrate MSK Connect connectors: %v", err)
	}

	return nil
}

func parseMigrateMskConnectorsOpts() (*MigrateMskConnectorOpts, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
	}

	if outputDir == "" {
		outputDir = fmt.Sprintf("%s-connectors", utils.ExtractClusterNameFromArn(clusterId))
	}

	var state types.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	cluster, err := state.GetClusterByArn(clusterId)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	var connectors []types.ConnectorSummary
	if cluster.AWSClientInformation.Connectors != nil {
		connectors = cluster.AWSClientInformation.Connectors
	}

	opts := MigrateMskConnectorOpts{
		EnvironmentId: ccEnvironmentId,
		ClusterId:     ccClusterId,
		CcApiKey:      ccApiKey,
		CcApiSecret:   ccApiSecret,
		Connectors:    connectors,
		OutputDir:     outputDir,
	}

	return &opts, nil
}
