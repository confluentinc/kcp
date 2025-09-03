package clusters

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/confluentinc/kcp/internal/generators/scan/clusters"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	discoverDir     string
	credentialsYaml string
)

func NewScanClustersCmd() *cobra.Command {
	clustersCmd := &cobra.Command{
		Use:           "clusters",
		Short:         "Scan multiple clusters using the generated `kcp discover` output",
		Long:          "Scan multiple clusters for information using the Kafka Admin API such as topics, ACLs and cluster ID",
		SilenceErrors: true,
		PreRunE:       preRunScanClusters,
		RunE:          runScanClusters,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&discoverDir, "discover-dir", "", "The path to the directory where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&credentialsYaml, "credentials-yaml", "", "The credentials YAML file used for authenticating to the MSK cluster(s).")
	clustersCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	clustersCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags}
		groupNames := []string{"Required Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	return clustersCmd
}

func preRunScanClusters(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanClusters(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(credentialsYaml)
	if err != nil {
		return fmt.Errorf("failed to read creds.yaml file: %w", err)
	}

	var credsFile types.CredsYaml
	if err := yaml.Unmarshal(data, &credsFile); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	if err := validateYaml(credsFile); err != nil {
		return err
	}

	opts := &clusters.ClustersScannerOpts{
		DiscoverDir:     discoverDir,
		CredentialsFile: credentialsYaml,
	}

	clustersScanner := clusters.NewClustersScanner(opts)
	if err := clustersScanner.Run(); err != nil {
		return fmt.Errorf("❌ failed to scan clusters: %v", err)
	}

	return nil
}

func validateYaml(credsFile types.CredsYaml) error {
	var clustersWithMultipleAuth []string

	for region, clusters := range credsFile.Regions {
		for arn, cluster := range clusters.Clusters {
			clusterName, err := getClusterNameFromArn(arn)
			if err != nil {
				return fmt.Errorf("❌ failed to get cluster name: %v", err)
			}

			enabledMethods := utils.GetAuthMethods(cluster)
			if len(enabledMethods) > 1 {
				clustersWithMultipleAuth = append(clustersWithMultipleAuth, fmt.Sprintf("%s (region: %s)", clusterName, region))
			}
		}
	}

	if len(clustersWithMultipleAuth) > 0 {
		return fmt.Errorf("❌ The following cluster(s) have more than one authentication method enabled, please configure only one auth method per cluster: %s",
			strings.Join(clustersWithMultipleAuth, ", "))
	}

	return nil
}

func getClusterNameFromArn(arn string) (string, error) {
	arnParts := strings.Split(arn, "/")
	if len(arnParts) < 2 {
		return "", fmt.Errorf("invalid cluster ARN format: %s", arn)
	}

	clusterName := arnParts[1]
	if clusterName == "" {
		return "", fmt.Errorf("cluster name not found in cluster ARN: %s", arn)
	}

	return clusterName, nil
}
