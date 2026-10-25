package client_inventory

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/iampolicy"
	"github.com/confluentinc/kcp/internal/services/s3"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	s3Uri        string
	stateFile    string
	logGroupName string
	region       string
	clusterName  string
	startTime    string
	endTime      string
)

func clientInventoryIAMAnnotation() string {
	return iampolicy.RenderStatements(
		"Grant the statement matching your chosen broker-log source (`--s3-uri` or `--log-group-name`).",
		[]iampolicy.Statement{
			{
				Sid:     "BrokerLogsFromS3",
				Actions: []string{"s3:GetObject", "s3:ListBucket"},
				Resources: []string{
					"arn:aws:s3:::<BROKER_LOGS_BUCKET>",
					"arn:aws:s3:::<BROKER_LOGS_BUCKET>/*",
				},
			},
			{
				Sid:       "BrokerLogsFromCloudWatch",
				Actions:   []string{"logs:FilterLogEvents"},
				Resources: []string{"arn:aws:logs:<AWS REGION>:<AWS ACCOUNT ID>:log-group:<LOG GROUP NAME>:*"},
			},
		},
	)
}

func NewScanClientInventoryCmd() *cobra.Command {
	clientInventoryCmd := &cobra.Command{
		Use:   "client-inventory",
		Short: "Scan the broker logs for client activity",
		Long: `Scan MSK broker logs to identify clients using the cluster, from either Amazon S3 or Amazon CloudWatch Logs.

Choose exactly one source:
  --s3-uri          read broker log files from an S3 prefix (region and cluster name are derived from the URI).
  --log-group-name  read broker logs from a CloudWatch Logs log group (requires --region and --cluster-name).

Prerequisites:

- The source MSK cluster must be configured with trace logging (kafka.server.KafkaApis=TRACE) on each broker.
- Broker logs must be delivered to the chosen destination (S3 or CloudWatch Logs).

Note on CloudWatch: AWS recommends S3 (or Firehose) for DEBUG/TRACE logging and cautions that CloudWatch "may continuously deliver a sample of logs." Client discovery relies on TRACE, so over CloudWatch completeness is best-effort — prefer S3 for complete or historical discovery, and use CloudWatch for quick, recent windows (default: the last 24h; override with --start-time/--end-time in RFC3339).

Each unique client is keyed by ` + "`clientID|topic|role|auth|principal`" + ` — e.g. ` + "`stock-levels-producer|stock-levels|Producer|IAM|arn:aws:sts::...`" + ` — so re-runs only append clients that don't already match an existing key.`,
		Example: `  # From S3
  kcp scan client-inventory \
      --s3-uri s3://my-cluster-logs/AWSLogs/000123456789/KafkaBrokerLogs/us-east-1/msk-cluster-xxxx-5/2025-08-13-14/ \
      --state-file kcp-state.json

  # From CloudWatch Logs (defaults to the last 24h)
  kcp scan client-inventory \
      --log-group-name /aws/msk/my-cluster/broker-logs \
      --region us-east-1 \
      --cluster-name msk-cluster \
      --state-file kcp-state.json`,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: clientInventoryIAMAnnotation(),
		},
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanClientInventory,
		RunE:          runScanClientInventory,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the client inventory reports will be written to.")

	s3SourceFlags := pflag.NewFlagSet("s3-source", pflag.ExitOnError)
	s3SourceFlags.SortFlags = false
	s3SourceFlags.StringVar(&s3Uri, "s3-uri", "", "Read broker logs from this S3 URI (e.g., s3://my-bucket/AWSLogs/<acct>/KafkaBrokerLogs/<region>/<cluster>/<date>/). Region and cluster name are derived from the URI.")

	cloudWatchSourceFlags := pflag.NewFlagSet("cloudwatch-source", pflag.ExitOnError)
	cloudWatchSourceFlags.SortFlags = false
	cloudWatchSourceFlags.StringVar(&logGroupName, "log-group-name", "", "Read broker logs from this CloudWatch Logs log group (requires --region and --cluster-name).")
	cloudWatchSourceFlags.StringVar(&region, "region", "", "AWS region of the CloudWatch Logs log group (CloudWatch source only).")
	cloudWatchSourceFlags.StringVar(&clusterName, "cluster-name", "", "MSK cluster name to attribute discovered clients to (CloudWatch source only).")
	cloudWatchSourceFlags.StringVar(&startTime, "start-time", "", "Window start as RFC3339 (CloudWatch source only; default: 24h before --end-time).")
	cloudWatchSourceFlags.StringVar(&endTime, "end-time", "", "Window end as RFC3339 (CloudWatch source only; default: now).")

	clientInventoryCmd.Flags().AddFlagSet(requiredFlags)
	clientInventoryCmd.Flags().AddFlagSet(s3SourceFlags)
	clientInventoryCmd.Flags().AddFlagSet(cloudWatchSourceFlags)

	clientInventoryCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, s3SourceFlags, cloudWatchSourceFlags}
		groupNames := []string{"Required Flags", "S3 Source Flags", "CloudWatch Source Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("Provide exactly one of --s3-uri or --log-group-name.")
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	_ = clientInventoryCmd.MarkFlagRequired("state-file")
	clientInventoryCmd.MarkFlagsOneRequired("s3-uri", "log-group-name")
	clientInventoryCmd.MarkFlagsMutuallyExclusive("s3-uri", "log-group-name")

	return clientInventoryCmd
}

func preRunScanClientInventory(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runScanClientInventory(cmd *cobra.Command, args []string) error {
	opts, useCloudWatch, err := parseClientInventoryFlags(rawFlags{
		s3Uri:        s3Uri,
		logGroupName: logGroupName,
		region:       region,
		clusterName:  clusterName,
		startTime:    startTime,
		endTime:      endTime,
		stateFile:    stateFile,
	})
	if err != nil {
		return err
	}

	state, err := types.NewStateFromFile(opts.StateFile)
	if err != nil {
		return fmt.Errorf("failed to load existing state file: %v", err)
	}

	var source LogSource
	if useCloudWatch {
		slog.Warn("⚠️ CloudWatch delivers only a sample of broker logs at TRACE; use the S3 source (--s3-uri) for complete or historical client discovery")

		cwClient, cwErr := client.NewCloudWatchLogsClient(opts.Region)
		if cwErr != nil {
			return fmt.Errorf("failed to create CloudWatch Logs client: %w", cwErr)
		}
		source = NewCloudWatchLogSource(cwClient, opts.LogGroupName, opts.StartTime, opts.EndTime)
	} else {
		s3Client, s3Err := client.NewS3Client(opts.Region)
		if s3Err != nil {
			return fmt.Errorf("failed to create S3 client: %w", s3Err)
		}
		s3Service := s3.NewS3Service(s3Client)
		source, err = NewS3LogSource(s3Service, opts.S3Uri)
		if err != nil {
			return fmt.Errorf("failed to create S3 log source: %w", err)
		}
	}

	clientInventoryScanner, err := NewClientInventoryScanner(source, *state, *opts)
	if err != nil {
		return fmt.Errorf("failed to create client inventory scanner: %v", err)
	}

	if err := clientInventoryScanner.Run(); err != nil {
		return err
	}

	return nil
}

// rawFlags holds the raw string flag values so parsing/validation can be unit
// tested without constructing a Cobra command.
type rawFlags struct {
	s3Uri        string
	logGroupName string
	region       string
	clusterName  string
	startTime    string
	endTime      string
	stateFile    string
}

// parseClientInventoryFlags validates the flags and builds the scanner options,
// returning whether the CloudWatch source was selected. Cobra enforces that
// exactly one of --s3-uri / --log-group-name is set; this function derives the
// per-mode values and validates CloudWatch's region/cluster/time inputs.
func parseClientInventoryFlags(in rawFlags) (*ClientInventoryScannerOpts, bool, error) {
	if in.logGroupName != "" {
		if in.region == "" {
			return nil, false, fmt.Errorf("--region is required when using --log-group-name")
		}
		if in.clusterName == "" {
			return nil, false, fmt.Errorf("--cluster-name is required when using --log-group-name")
		}

		end := time.Now()
		if in.endTime != "" {
			t, err := time.Parse(time.RFC3339, in.endTime)
			if err != nil {
				return nil, false, fmt.Errorf("invalid --end-time (want RFC3339, e.g. 2025-08-13T14:00:00Z): %w", err)
			}
			end = t
		}

		start := end.Add(-24 * time.Hour)
		if in.startTime != "" {
			t, err := time.Parse(time.RFC3339, in.startTime)
			if err != nil {
				return nil, false, fmt.Errorf("invalid --start-time (want RFC3339, e.g. 2025-08-13T14:00:00Z): %w", err)
			}
			start = t
		}

		if !start.Before(end) {
			return nil, false, fmt.Errorf("--start-time (%s) must be before --end-time (%s)", start.Format(time.RFC3339), end.Format(time.RFC3339))
		}

		opts := &ClientInventoryScannerOpts{
			Region:       in.region,
			ClusterName:  in.clusterName,
			StateFile:    in.stateFile,
			LogGroupName: in.logGroupName,
			StartTime:    start,
			EndTime:      end,
		}
		return opts, true, nil
	}

	// S3 mode: derive region and cluster name from the URI, as before.
	if in.s3Uri == "" {
		return nil, false, fmt.Errorf("exactly one of --s3-uri or --log-group-name is required")
	}

	regionFromUri, err := utils.ExtractRegionFromS3Uri(in.s3Uri)
	if err != nil {
		return nil, false, fmt.Errorf("failed to extract region from S3 URI: %w", err)
	}

	clusterNameFromUri, err := utils.ExtractClusterNameFromS3Uri(in.s3Uri)
	if err != nil {
		return nil, false, fmt.Errorf("failed to extract cluster name from S3 URI: %w", err)
	}

	opts := &ClientInventoryScannerOpts{
		S3Uri:       in.s3Uri,
		Region:      regionFromUri,
		ClusterName: clusterNameFromUri,
		StateFile:   in.stateFile,
	}
	return opts, false, nil
}
