package managed_connectors

import (
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/services/iampolicy"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile   string
	regions     []string
	clusterArns []string
)

func managedConnectorsIAMAnnotation() string {
	return iampolicy.RenderStatements(
		"The following policy covers scanning MSK-managed (MSK Connect) connectors.",
		[]iampolicy.Statement{
			{
				Sid: "MSKConnectScanPermissions",
				Actions: []string{
					"kafkaconnect:ListConnectors",
					"kafkaconnect:DescribeConnector",
				},
			},
		},
	)
}

func NewScanManagedConnectorsCmd() *cobra.Command {
	managedConnectorsCmd := &cobra.Command{
		Use:   "managed-connectors",
		Short: "Scan MSK-managed (MSK Connect) connectors for clusters in the state file",
		Long:  "Scan Amazon MSK Connect connectors and match them to MSK clusters already present in the state file. Sensitive config values are redacted before being written to state.",
		Example: `  # Scan managed connectors for all discovered clusters in a region
  kcp scan managed-connectors --state-file kcp-state.json --region us-east-1

  # Scan multiple regions (repeated flag or comma-separated)
  kcp scan managed-connectors --state-file kcp-state.json --region us-east-1 --region eu-west-3

  # Scan a single cluster (region inferred from the ARN)
  kcp scan managed-connectors --state-file kcp-state.json --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/uuid`,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: managedConnectorsIAMAnnotation(),
		},
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanManagedConnectors,
		RunE:          runScanManagedConnectors,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file to update with connector information.")
	managedConnectorsCmd.Flags().AddFlagSet(requiredFlags)

	// Exactly one of --region / --cluster-arn (mirrors kcp discover).
	scopeFlags := pflag.NewFlagSet("scope", pflag.ExitOnError)
	scopeFlags.SortFlags = false
	scopeFlags.StringSliceVar(&regions, "region", []string{}, "The AWS region(s) to scan (comma separated list or repeated flag). Scans all discovered clusters in each region. Mutually exclusive with --cluster-arn.")
	scopeFlags.StringSliceVar(&clusterArns, "cluster-arn", []string{}, "Scan only the specified MSK cluster ARN(s) (comma separated or repeated flag). Region is inferred from each ARN. Mutually exclusive with --region.")
	managedConnectorsCmd.Flags().AddFlagSet(scopeFlags)

	managedConnectorsCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		if c.Example != "" {
			fmt.Printf("Examples:\n%s\n\n", c.Example)
		}
		for _, fs := range []*pflag.FlagSet{requiredFlags, scopeFlags} {
			if usage := fs.FlagUsages(); usage != "" {
				fmt.Printf("%s\n", usage)
			}
		}
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	_ = managedConnectorsCmd.MarkFlagRequired("state-file")
	managedConnectorsCmd.MarkFlagsMutuallyExclusive("region", "cluster-arn")
	managedConnectorsCmd.MarkFlagsOneRequired("region", "cluster-arn")

	return managedConnectorsCmd
}

func preRunScanManagedConnectors(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}
	if len(clusterArns) > 0 {
		if _, err := regionsFromClusterArns(clusterArns); err != nil {
			return err
		}
	}
	return nil
}

func runScanManagedConnectors(cmd *cobra.Command, args []string) error {
	opts, err := parseScanManagedConnectorsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan managed connectors opts: %v", err)
	}
	scanner := NewManagedConnectorsScanner(*opts)
	if err := scanner.Run(); err != nil {
		return fmt.Errorf("failed to scan managed connectors: %v", err)
	}
	return nil
}

// regionsFromClusterArns returns the distinct AWS regions parsed from the given
// MSK cluster ARNs, preserving first-seen order. Returns an error if any ARN is
// malformed. (Mirrors the helper in cmd/discover.)
func regionsFromClusterArns(arns []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, arn := range arns {
		region, err := utils.ExtractRegionFromArn(arn)
		if err != nil {
			return nil, fmt.Errorf("invalid cluster ARN %q: %w", arn, err)
		}
		if !seen[region] {
			seen[region] = true
			out = append(out, region)
		}
	}
	return out, nil
}

func parseScanManagedConnectorsOpts() (*ManagedConnectorsScannerOpts, error) {
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("state file does not exist: %s", stateFile)
	}

	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	effectiveRegions := regions
	if len(clusterArns) > 0 {
		derived, err := regionsFromClusterArns(clusterArns)
		if err != nil {
			return nil, err
		}
		effectiveRegions = derived

		// Each requested cluster must already exist in state.
		for _, arn := range clusterArns {
			if _, err := state.GetClusterByArn(arn); err != nil {
				return nil, fmt.Errorf("cluster not found in state file: %v", err)
			}
		}
	}

	return &ManagedConnectorsScannerOpts{
		StateFile:   stateFile,
		State:       state,
		Regions:     effectiveRegions,
		ClusterArns: clusterArns,
	}, nil
}
