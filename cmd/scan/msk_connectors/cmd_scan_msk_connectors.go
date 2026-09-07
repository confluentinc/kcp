package msk_connectors

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
	stateFile          string
	regions            []string
	clusterArns        []string
	metricsGranularity string
)

func mskConnectorsIAMAnnotation() string {
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
			{
				Sid: "MSKConnectMetricsPermissions",
				Actions: []string{
					"cloudwatch:GetMetricData",
					"cloudwatch:ListMetrics",
				},
			},
		},
	)
}

func NewScanMSKConnectorsCmd() *cobra.Command {
	mskConnectorsCmd := &cobra.Command{
		Use:   "msk-connectors",
		Short: "Scan MSK-managed (MSK Connect) connectors for clusters in the state file",
		Long:  "Scan Amazon MSK Connect connectors and match them to MSK clusters already present in the state file. Sensitive config values are redacted before being written to state.",
		Example: `  # Scan managed connectors for all discovered clusters in a region
  kcp scan msk-connectors --state-file kcp-state.json --region us-east-1

  # Scan multiple regions (repeated flag or comma-separated)
  kcp scan msk-connectors --state-file kcp-state.json --region us-east-1 --region eu-west-3

  # Scan a single cluster (region inferred from the ARN)
  kcp scan msk-connectors --state-file kcp-state.json --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/uuid

  # Also collect CloudWatch (AWS/KafkaConnect) metrics (pass --metrics-granularity)
  kcp scan msk-connectors --state-file kcp-state.json --region us-east-1 --metrics-granularity 1d`,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: mskConnectorsIAMAnnotation(),
		},
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanMSKConnectors,
		RunE:          runScanMSKConnectors,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file to update with connector information.")
	mskConnectorsCmd.Flags().AddFlagSet(requiredFlags)

	// Exactly one of --region / --cluster-arn (mirrors kcp discover).
	scopeFlags := pflag.NewFlagSet("scope", pflag.ExitOnError)
	scopeFlags.SortFlags = false
	scopeFlags.StringSliceVar(&regions, "region", []string{}, "The AWS region(s) to scan (comma separated list or repeated flag). Scans all discovered clusters in each region. Mutually exclusive with --cluster-arn.")
	scopeFlags.StringSliceVar(&clusterArns, "cluster-arn", []string{}, "Scan only the specified MSK cluster ARN(s) (comma separated or repeated flag). Region is inferred from each ARN. Mutually exclusive with --region.")
	mskConnectorsCmd.Flags().AddFlagSet(scopeFlags)

	metricsFlags := pflag.NewFlagSet("metrics", pflag.ExitOnError)
	metricsFlags.SortFlags = false
	metricsFlags.StringVar(&metricsGranularity, "metrics-granularity", "", "Collect CloudWatch (AWS/KafkaConnect) metrics at this granularity: 60s, 5m, 1h, or 1d. Omit to skip metrics collection. Max time range per granularity: 60s = 15 days, 5m = 63 days, 1h = 365 days, 1d = 365 days.")
	mskConnectorsCmd.Flags().AddFlagSet(metricsFlags)

	mskConnectorsCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		if c.Example != "" {
			fmt.Printf("Examples:\n%s\n\n", c.Example)
		}
		for _, fs := range []*pflag.FlagSet{requiredFlags, scopeFlags, metricsFlags} {
			if usage := fs.FlagUsages(); usage != "" {
				fmt.Printf("%s\n", usage)
			}
		}
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	_ = mskConnectorsCmd.MarkFlagRequired("state-file")
	mskConnectorsCmd.MarkFlagsMutuallyExclusive("region", "cluster-arn")
	mskConnectorsCmd.MarkFlagsOneRequired("region", "cluster-arn")

	return mskConnectorsCmd
}

func preRunScanMSKConnectors(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}
	if len(clusterArns) > 0 {
		if _, err := utils.RegionsFromClusterArns(clusterArns); err != nil {
			return err
		}
	}
	if metricsGranularity != "" {
		switch metricsGranularity {
		case "60s", "5m", "1h", "1d":
		default:
			return fmt.Errorf("invalid --metrics-granularity '%s': must be one of 60s, 5m, 1h, 1d", metricsGranularity)
		}
	}
	return nil
}

func runScanMSKConnectors(cmd *cobra.Command, args []string) error {
	opts, err := parseScanMSKConnectorsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse scan managed connectors opts: %v", err)
	}
	scanner := NewMSKConnectorsScanner(*opts)
	if err := scanner.Run(); err != nil {
		return fmt.Errorf("failed to scan managed connectors: %v", err)
	}
	return nil
}

func parseScanMSKConnectorsOpts() (*MSKConnectorsScannerOpts, error) {
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("state file does not exist: %s", stateFile)
	}

	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	effectiveRegions := regions
	if len(clusterArns) > 0 {
		derived, err := utils.RegionsFromClusterArns(clusterArns)
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
	} else {
		// Each requested region must already exist in state; otherwise the scan would
		// silently succeed with 0 clusters (there is nothing in state to match against).
		for _, region := range regions {
			if _, err := state.GetRegion(region); err != nil {
				return nil, err
			}
		}
	}

	return &MSKConnectorsScannerOpts{
		StateFile:          stateFile,
		State:              state,
		Regions:            effectiveRegions,
		ClusterArns:        clusterArns,
		MetricsGranularity: metricsGranularity,
	}, nil
}
