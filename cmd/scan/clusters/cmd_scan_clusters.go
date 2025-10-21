package clusters

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile       string
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
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&credentialsYaml, "credentials-file", "", "The credentials YAML file used for authenticating to the MSK cluster(s).")
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

	clustersCmd.MarkFlagRequired("state-file")
	clustersCmd.MarkFlagRequired("credentials-file")

	return clustersCmd
}

func preRunScanClusters(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanClusters(cmd *cobra.Command, args []string) error {
	opts, err := parseScanClustersOpts()
	if err != nil {
		return fmt.Errorf("❌ failed to parse scan clusters opts: %v", err)
	}

	clustersScanner := NewClustersScanner(*opts)
	if err := clustersScanner.Run(); err != nil {
		return fmt.Errorf("❌ failed to scan clusters: %v", err)
	}

	return nil
}

func parseScanClustersOpts() (*ClustersScannerOpts, error) {
	credentials, errs := types.NewCredentialsFromFile(credentialsYaml)
	if len(errs) > 0 {
		errMsg := "failed to parse credentials file:"
		for _, e := range errs {
			errMsg += "\n\t- " + e.Error()
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	opts := ClustersScannerOpts{
		StateFile:   stateFile,
		State:       *state,
		Credentials: *credentials,
	}

	return &opts, nil
}
