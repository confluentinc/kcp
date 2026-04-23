package discover

import (
	"fmt"
	"log/slog"
	"os"

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

const discoverIAMPermissions = "The following policy covers a full run. If you pass `--skip-topics`, `--skip-costs`, or `--skip-metrics`, the corresponding statements can be omitted.\n\n" +
	"```json\n" +
	`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "MSKScanPermissions",
      "Effect": "Allow",
      "Action": [
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
        "kafkaconnect:ListConnectors",
        "kafkaconnect:DescribeConnector"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MSKClusterConnect",
      "Effect": "Allow",
      "Action": ["kafka-cluster:Connect", "kafka-cluster:DescribeCluster"],
      "Resource": "*"
    },
    {
      "Sid": "MSKTopicActions",
      "Effect": "Allow",
      "Action": [
        "kafka:ListTopics",
        "kafka:DescribeTopic",
        "kafka-cluster:DescribeTopic",
        "kafka-cluster:DescribeTopicDynamicConfiguration"
      ],
      "Resource": "*"
    },
    {
      "Sid": "CostMetricsScanPermissions",
      "Effect": "Allow",
      "Action": [
        "cloudwatch:GetMetricData",
        "ce:GetCostAndUsage",
        "cloudwatch:GetMetricStatistics",
        "cloudwatch:ListMetrics"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MSKNetworkingScanPermission",
      "Effect": "Allow",
      "Action": ["ec2:DescribeSubnets"],
      "Resource": "*"
    }
  ]
}` + "\n```\n"

var (
	regions     []string
	skipCosts   bool
	skipMetrics bool
	skipTopics  bool
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
  kcp discover --region us-east-1 --skip-topics --skip-costs --skip-metrics`,
		Annotations: map[string]string{
			"aws_iam_permissions": discoverIAMPermissions,
		},
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunDiscover,
		RunE:          runDiscover,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringSliceVar(&regions, "region", []string{}, "The AWS region(s) to scan (comma separated list or repeated flag)")
	discoverCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.BoolVar(&skipTopics, "skip-topics", false, "Skips the topic discovery through the AWS MSK API")
	optionalFlags.BoolVar(&skipCosts, "skip-costs", false, "Skips the cost discovery through the AWS Cost Explorer API")
	optionalFlags.BoolVar(&skipMetrics, "skip-metrics", false, "Skips the metrics discovery through the AWS CloudWatch API")
	discoverCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	discoverCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	_ = discoverCmd.MarkFlagRequired("region")

	return discoverCmd
}

func preRunDiscover(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
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
		state, err = types.NewStateFromFile(stateFileName)
		if err != nil {
			return nil, fmt.Errorf("failed to load existing state file: %v", err)
		}
		slog.Debug("using existing state file", "file", stateFileName)
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

	return &DiscovererOpts{
		Regions:     regions,
		SkipCosts:   skipCosts,
		SkipMetrics: skipMetrics,
		SkipTopics:  skipTopics,
		State:       state,
		Credentials: credentials,
	}, nil
}
