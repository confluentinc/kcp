package msk

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
	stateFile       string
	clusterArn      string
	ccClusterId     string
	ccEnvironmentId string
	ccApiKey        string
	ccApiSecret     string
	outputDir       string
)

func NewMigrateMskConnectorsCmd() *cobra.Command {
	mskConnectorsCmd := &cobra.Command{
		Use:           "msk",
		Short:         "Migrate connectors from MSK to Confluent Cloud",
		Long:          "Migrate connectors from MSK to Confluent Cloud",
		SilenceErrors: true,
		PreRunE:       preRunMigrateMskConnectors,
		RunE:          runMigrateMskConnectors,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&clusterArn, "cluster-arn", "", "The ARN of the MSK cluster to migrate connectors from.")
	requiredFlags.StringVar(&ccClusterId, "cc-cluster-id", "", "The ID of the Confluent Cloud cluster to migrate connectors to.")
	requiredFlags.StringVar(&ccEnvironmentId, "cc-environment-id", "", "The ID of the Confluent Cloud environment to migrate connectors to.")
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

	mskConnectorsCmd.MarkFlagRequired("state-file")
	mskConnectorsCmd.MarkFlagRequired("cc-cluster-id")
	mskConnectorsCmd.MarkFlagRequired("cc-environment-id")
	mskConnectorsCmd.MarkFlagRequired("cc-api-key")
	mskConnectorsCmd.MarkFlagRequired("cc-api-secret")

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
		return fmt.Errorf("failed to parse migrate MSK connectors opts: %v", err)
	}

	mskConnectorsMigrator := NewMskConnectorsMigrator(*opts)
	if err := mskConnectorsMigrator.Run(); err != nil {
		return fmt.Errorf("failed to migrate MSK connectors: %v", err)
	}

	return nil
}

func parseMigrateMskConnectorsOpts() (*MigrateMskConnectorsOpts, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
	}

	var state types.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	cluster, err := utils.GetClusterByArn(&state, clusterArn)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	opts := MigrateMskConnectorsOpts{
		connectors:  cluster.AWSClientInformation.Connectors,
		OutputDir:   outputDir,
	}
}
