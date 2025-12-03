package connectorutility

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
	stateFile  string
	clusterArn string
	outputDir  string
)

func NewConnectorUtilityCmd() *cobra.Command {
	connectorUtilityCmd := &cobra.Command{
		Use:           "connector-utility",
		Short:         "Utility to migrate connectors to Confluent Cloud",
		Long:          "Utility to migrate connectors to Confluent Cloud",
		SilenceErrors: true,
		PreRunE:       preRunConnectorUtility,
		RunE:          runConnectorUtility,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the cluster discovery reports have been written to.")
	connectorUtilityCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&clusterArn, "cluster-arn", "", "The ARN of the MSK cluster to generate the connector configs JSON from.")
	optionalFlags.StringVar(&outputDir, "output-dir", "", "The directory where the connector configs JSON will be written to")
	connectorUtilityCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	connectorUtilityCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	connectorUtilityCmd.MarkFlagRequired("state-file")

	return connectorUtilityCmd
}

func preRunConnectorUtility(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runConnectorUtility(cmd *cobra.Command, args []string) error {
	opts, err := parseConnectorUtilityOpts()
	if err != nil {
		return fmt.Errorf("failed to parse connector utility opts: %v", err)
	}

	connectorUtility := NewConnectorUtility(*opts)
	if err := connectorUtility.Run(); err != nil {
		return fmt.Errorf("failed to run connector utility: %v", err)
	}

	return nil
}

func parseConnectorUtilityOpts() (*ConnectorUtilityOpts, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read statefile %s: %w", stateFile, err)
	}

	var state types.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	clustersByArn := make(map[string]*types.DiscoveredCluster)

	if clusterArn != "" {
		cluster, err := utils.GetClusterByArn(&state, clusterArn)
		if err != nil {
			return nil, fmt.Errorf("failed to get cluster: %w", err)
		}

		hasConnectors := len(cluster.AWSClientInformation.Connectors) > 0 ||
			(cluster.KafkaAdminClientInformation.SelfManagedConnectors != nil &&
				len(cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors) > 0)

		if !hasConnectors {
			return nil, fmt.Errorf("no connectors found for cluster %s in %s. The cluster exists but has no MSK Connect or self-managed connectors", cluster.Name, stateFile)
		}
		clustersByArn[clusterArn] = cluster
	} else {
		for _, region := range state.Regions {
			for i := range region.Clusters {
				cluster := &region.Clusters[i]
				// Include cluster if it has any connectors (MSK Connect or self-managed)
				hasConnectors := len(cluster.AWSClientInformation.Connectors) > 0 ||
					(cluster.KafkaAdminClientInformation.SelfManagedConnectors != nil &&
						len(cluster.KafkaAdminClientInformation.SelfManagedConnectors.Connectors) > 0)
				if hasConnectors {
					clustersByArn[cluster.Arn] = cluster
				}
			}
		}
	}

	if outputDir == "" {
		outputDir = "discovered-connector-configs"
	}

	return &ConnectorUtilityOpts{
		ClustersByArn: clustersByArn,
		OutputDir:     outputDir,
	}, nil
}
