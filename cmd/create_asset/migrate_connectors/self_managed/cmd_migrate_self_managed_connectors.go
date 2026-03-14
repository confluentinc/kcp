package self_managed_connectors

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
	clusterId       string
	sourceType      string
	ccClusterId     string
	ccEnvironmentId string
	ccApiKey        string
	ccApiSecret     string
	outputDir       string
)

func NewMigrateSelfManagedConnectorsCmd() *cobra.Command {
	selfManagedConnectorsCmd := &cobra.Command{
		Use:           "self-managed",
		Short:         "Migrate self-managed connectors to Confluent Cloud",
		Long:          "Migrate self-managed connectors to Confluent Cloud",
		SilenceErrors: true,
		PreRunE:       preRunMigrateSelfManagedConnectors,
		RunE:          runMigrateSelfManagedConnectors,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the cluster discovery reports have been written to.")
	requiredFlags.StringVar(&ccEnvironmentId, "cc-environment-id", "", "The ID of the Confluent Cloud environment to migrate connectors to.")
	requiredFlags.StringVar(&ccClusterId, "cc-cluster-id", "", "The ID of the Confluent Cloud cluster to migrate connectors to.")
	requiredFlags.StringVar(&ccApiKey, "cc-api-key", "", "The API key for the Confluent Cloud cluster to migrate connectors to.")
	requiredFlags.StringVar(&ccApiSecret, "cc-api-secret", "", "The API secret for the Confluent Cloud cluster to migrate connectors to.")
	selfManagedConnectorsCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	sourceFlags := pflag.NewFlagSet("source", pflag.ExitOnError)
	sourceFlags.SortFlags = false
	sourceFlags.StringVar(&sourceType, "source-type", "msk", "The source type (msk or osk).")
	sourceFlags.StringVar(&clusterId, "cluster-id", "", "The cluster identifier (ARN for MSK, cluster ID from credentials file for OSK).")
	selfManagedConnectorsCmd.Flags().AddFlagSet(sourceFlags)
	groups[sourceFlags] = "Source Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&outputDir, "output-dir", "", "The directory where the Confluent Cloud Terraform connector assets will be written to")
	selfManagedConnectorsCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	selfManagedConnectorsCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	selfManagedConnectorsCmd.MarkFlagRequired("state-file")
	selfManagedConnectorsCmd.MarkFlagRequired("cluster-id")
	selfManagedConnectorsCmd.MarkFlagRequired("cc-environment-id")
	selfManagedConnectorsCmd.MarkFlagRequired("cc-cluster-id")
	selfManagedConnectorsCmd.MarkFlagRequired("cc-api-key")
	selfManagedConnectorsCmd.MarkFlagRequired("cc-api-secret")

	return selfManagedConnectorsCmd
}

func preRunMigrateSelfManagedConnectors(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrateSelfManagedConnectors(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrateSelfManagedConnectorsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migrate self-managed connectors opts: %v", err)
	}

	selfManagedConnectorsMigrator := NewSelfManagedConnectorMigrator(*opts)
	if err := selfManagedConnectorsMigrator.Run(); err != nil {
		return fmt.Errorf("failed to migrate self-managed connectors: %v", err)
	}

	return nil
}

func parseMigrateSelfManagedConnectorsOpts() (*MigrateSelfManagedConnectorOpts, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
	}

	var state types.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	var connectors []types.SelfManagedConnector

	switch sourceType {
	case "msk":
		cluster, err := state.GetClusterByArn(clusterId)
		if err != nil {
			return nil, fmt.Errorf("failed to get cluster: %w", err)
		}
		if cluster.KafkaAdminClientInformation.SelfManagedConnectors != nil {
			connectors = cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors
		}
		if outputDir == "" {
			outputDir = fmt.Sprintf("%s-connectors", cluster.Name)
		}
	case "osk":
		cluster, err := state.GetOSKClusterByID(clusterId)
		if err != nil {
			return nil, fmt.Errorf("failed to get OSK cluster: %w", err)
		}
		if cluster.KafkaAdminClientInformation.SelfManagedConnectors != nil {
			connectors = cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors
		}
		if outputDir == "" {
			outputDir = fmt.Sprintf("%s-connectors", cluster.ID)
		}
	default:
		return nil, fmt.Errorf("invalid --source-type: %s (must be 'msk' or 'osk')", sourceType)
	}

	opts := MigrateSelfManagedConnectorOpts{
		EnvironmentId: ccEnvironmentId,
		ClusterId:     ccClusterId,
		CcApiKey:      ccApiKey,
		CcApiSecret:   ccApiSecret,
		Connectors:    connectors,
		OutputDir:     outputDir,
	}

	return &opts, nil
}
