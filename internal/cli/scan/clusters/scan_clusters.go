package clusters

import (
	"fmt"
	"strings"

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

	credsFile, err := types.NewCredentials(credentialsYaml)
	if err != nil {
		return fmt.Errorf("failed to create credentials file: %v", err)
	}

	clustersScanner := clusters.NewClustersScanner(discoverDir, *credsFile)
	if err := clustersScanner.Run(); err != nil {
		return fmt.Errorf("❌ failed to scan clusters: %v", err)
	}

	return nil
}
