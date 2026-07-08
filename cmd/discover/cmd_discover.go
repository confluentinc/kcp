package discover

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/internal/output"
	"github.com/confluentinc/kcp/internal/services/iampolicy"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	stateFileName          = "kcp-state.json"
	credentialsFileName    = "msk-credentials.yaml"
	reportCommandsFileName = "report-commands.txt"
)

func discoverIAMAnnotation() string {
	return iampolicy.RenderStatements(
		"The following policy covers a full run. If you pass `--skip-topics`, `--skip-costs`, or `--skip-metrics`, the corresponding statements can be omitted.",
		[]iampolicy.Statement{
			{
				Sid: "MSKScanPermissions",
				Actions: []string{
					"kafka:ListClustersV2",
					"kafka:ListReplicators",
					"kafka:ListVpcConnections",
					"kafka:GetCompatibleKafkaVersions",
					"kafka:GetBootstrapBrokers",
					"kafka:ListConfigurations",
					"kafka:DescribeClusterV2",
					"kafka:ListKafkaVersions",
					"kafka:ListNodes",
					"kafka:ListClusterOperationsV2",
					"kafka:ListScramSecrets",
					"kafka:ListClientVpcConnections",
					"kafka:GetClusterPolicy",
					"kafka:DescribeConfigurationRevision",
					"kafka:DescribeReplicator",
				},
			},
			{
				Sid: "MSKClusterConnect",
				Actions: []string{
					"kafka-cluster:Connect",
					"kafka-cluster:DescribeCluster",
				},
			},
			{
				Sid: "MSKTopicActions",
				Actions: []string{
					"kafka:ListTopics",
					"kafka:DescribeTopic",
					"kafka-cluster:DescribeTopic",
					"kafka-cluster:DescribeTopicDynamicConfiguration",
				},
			},
			{
				Sid: "CostMetricsScanPermissions",
				Actions: []string{
					"cloudwatch:GetMetricData",
					"ce:GetCostAndUsage",
					"cloudwatch:GetMetricStatistics",
					"cloudwatch:ListMetrics",
				},
			},
			{
				Sid:     "MSKNetworkingScanPermission",
				Actions: []string{"ec2:DescribeSubnets"},
			},
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

var (
	regions            []string
	skipCosts          bool
	skipMetrics        bool
	skipTopics         bool
	metricsGranularity string
	clusterArns        []string
)

func NewDiscoverCmd() *cobra.Command {
	discoverCmd := &cobra.Command{
		Use:   "discover",
		Short: "Multi-region, multi cluster discovery scan of AWS MSK",
		Long:  "Performs a full Discovery of all MSK clusters across multiple regions, and their associated resources, costs and metrics",
		Example: `  # Scan a single region
  kcp discover --region us-east-1

  # Scan multiple regions (repeated flag or comma-separated)
  kcp discover --region us-east-1 --region eu-west-3
  kcp discover --region us-east-1,eu-west-3

  # Skip topic/cost/metric discovery for faster runs or reduced IAM scope
  kcp discover --region us-east-1 --skip-topics --skip-costs --skip-metrics


  # Specify metrics granularity (mutually exclusive with --skip-metrics)
  kcp discover --region us-east-1 --metrics-granularity 60s
  kcp discover --region us-east-1 --metrics-granularity 5m
  kcp discover --region us-east-1 --metrics-granularity 1h
  kcp discover --region us-east-1 --metrics-granularity 1d

  The maximum time range for each granularity is:
  - 60s = 15 days
  - 5m = 63 days
  - 1h = 365 days
  - 1d = 365 days

  The finer the granularity, the more detailed the metrics data, but also more data is stored in the state-file, resulting in state-file growth. Coarser granularity is recommended for averaging workloads over longer time periods, but will smooth out spikes, while finer granularity is recommended for analyzing more bursty workloads and uncovering spikes over short time periods.

  # Discover a single cluster (region inferred from the ARN); create or replace it in state
  kcp discover --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/uuid

  # Re-discover one cluster at a finer metrics granularity without touching other clusters
  kcp discover --cluster-arn arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/uuid --metrics-granularity 60s
  `,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: discoverIAMAnnotation(),
		},
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunDiscover,
		RunE:          runDiscover,
	}

	groups := map[*pflag.FlagSet]string{}

	// Exactly one of --region / --cluster-arn must be provided (enforced below via
	// MarkFlagsMutuallyExclusive + MarkFlagsOneRequired), so they share a group whose
	// label reflects that either-or requirement rather than implying both are needed.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringSliceVar(&regions, "region", []string{}, "The AWS region(s) to scan (comma separated list or repeated flag). Mutually exclusive with --cluster-arn.")
	requiredFlags.StringSliceVar(&clusterArns, "cluster-arn", []string{}, "Discover only the specified MSK cluster ARN(s) (comma separated or repeated flag). Region is inferred from each ARN. Mutually exclusive with --region.")
	discoverCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags (provide exactly one)"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&skipTopics, "skip-topics", false, "Skips the topic discovery through the AWS MSK API")
	optionalFlags.BoolVar(&skipCosts, "skip-costs", false, "Skips the cost discovery through the AWS Cost Explorer API")
	optionalFlags.BoolVar(&skipMetrics, "skip-metrics", false, "Skips the metrics discovery through the AWS CloudWatch API")
	optionalFlags.StringVar(&metricsGranularity, "metrics-granularity", "1d", "The granularity for which to query for CloudWatch metrics. Valid values: 60s, 5m, 1h, 1d. The maximum time range for each granularity is: 60s = 15 days, 5m = 63 days, 1h = 365 days, 1d = 365 days.")
	discoverCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	discoverCmd.MarkFlagsMutuallyExclusive("skip-metrics", "metrics-granularity")
	discoverCmd.MarkFlagsMutuallyExclusive("region", "cluster-arn")
	discoverCmd.MarkFlagsOneRequired("region", "cluster-arn")

	discoverCmd.SetUsageFunc(func(c *cobra.Command) error {
		output.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags (provide exactly one)", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				output.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		output.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	return discoverCmd
}

func preRunDiscover(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	// Validate metrics granularity.
	switch metricsGranularity {
	case "60s", "5m", "1h", "1d":
	default:
		return fmt.Errorf("invalid metrics-granularity %q: must be one of: 60s, 5m, 1h, 1d", metricsGranularity)
	}

	// Validate cluster ARNs are well-formed (region is parsed from each ARN).
	if len(clusterArns) > 0 {
		if _, err := regionsFromClusterArns(clusterArns); err != nil {
			return err
		}
	}

	return nil
}

func runDiscover(cmd *cobra.Command, args []string) error {
	opts, err := parseDiscoverOpts()
	if err != nil {
		return fmt.Errorf("failed to parse discover opts: %v", err)
	}

	discoverer := NewDiscoverer(*opts)

	if err := discoverer.Run(); err != nil {
		return fmt.Errorf("failed to discover: %v", err)
	}

	return nil
}

func parseDiscoverOpts() (*DiscovererOpts, error) {
	var state *types.State
	var credentials *types.Credentials

	// Check if existing state file exists
	if _, err := os.Stat(stateFileName); os.IsNotExist(err) {
		// No state file found - start fresh
		slog.Debug("starting with fresh state")
	} else if err != nil {
		// Error checking file - return error
		return nil, fmt.Errorf("failed to check state file: %v", err)
	} else {
		// State file exists - load it
		slog.Info("Found existing state file, attempting to load it", "file", stateFileName)
		state, err = types.NewStateFromFile(stateFileName)
		if err != nil {
			return nil, fmt.Errorf("failed to load existing state file: %v", err)
		}
		slog.Debug("Loaded existing state file", "file", stateFileName)
	}

	// Check if existing credentials file exists
	if _, err := os.Stat(credentialsFileName); os.IsNotExist(err) {
		// No credentials file found - start fresh
		slog.Debug("starting with fresh credentials")
	} else if err != nil {
		// Error checking file - return error
		return nil, fmt.Errorf("failed to check credentials file: %v", err)
	} else {
		// Credentials file exists - load it
		var errs []error
		credentials, errs = types.NewCredentialsFromFile(credentialsFileName)
		if len(errs) > 0 {
			return nil, fmt.Errorf("failed to load existing credentials file: %v", errs)
		}
		slog.Debug("using existing credentials file", "file", credentialsFileName)
	}

	// In targeted mode regions are inferred from the cluster ARNs; otherwise use --region.
	effectiveRegions := regions
	if len(clusterArns) > 0 {
		derived, err := regionsFromClusterArns(clusterArns)
		if err != nil {
			return nil, err
		}
		effectiveRegions = derived
	}

	return &DiscovererOpts{
		Regions:            effectiveRegions,
		SkipCosts:          skipCosts,
		SkipMetrics:        skipMetrics,
		SkipTopics:         skipTopics,
		State:              state,
		Credentials:        credentials,
		MetricsGranularity: metricsGranularity,
		ClusterArns:        clusterArns,
	}, nil
}
