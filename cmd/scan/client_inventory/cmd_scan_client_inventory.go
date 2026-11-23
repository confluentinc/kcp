package client_inventory

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/iampolicy"
	"github.com/confluentinc/kcp/internal/services/s3"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	sourceS3         = "s3"
	sourceCloudWatch = "cloudwatch"
)

var (
	logs         string
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
		"Grant the statement matching your chosen broker-log source (`--logs s3` or `--logs cloudwatch`).",
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
				Actions:   []string{"logs:StartQuery"},
				Resources: []string{"arn:aws:logs:<AWS REGION>:<AWS ACCOUNT ID>:log-group:<LOG GROUP NAME>:*"},
			},
			{
				// GetQueryResults is authorised against the query, not a log group ARN.
				Sid:       "BrokerLogsFromCloudWatchResults",
				Actions:   []string{"logs:GetQueryResults"},
				Resources: []string{"*"},
			},
		},
	)
}

func NewScanClientInventoryCmd() *cobra.Command {
	clientInventoryCmd := &cobra.Command{
		Use:   "client-inventory",
		Short: "Scan the broker logs for client activity",
		Long: `Scan MSK broker logs to identify clients using the cluster, from either Amazon S3 or Amazon CloudWatch Logs.

Choose the source with the required --logs flag (s3 or cloudwatch). Each source accepts only its own flags, and kcp rejects flags that belong to the other source:
  --logs s3          requires --s3-uri (region and cluster name are derived from the URI).
  --logs cloudwatch  requires --log-group-name, --region and --cluster-name, and accepts --start-time and --end-time.

Prerequisites:

- The source MSK cluster must be configured with trace logging (kafka.server.KafkaApis=TRACE) on each broker.
- Broker logs must be delivered to the chosen destination (S3 or CloudWatch Logs).

Note on CloudWatch: AWS recommends S3 (or Firehose) for DEBUG and TRACE logging and cautions that CloudWatch "may continuously deliver a sample of logs." Client discovery relies on TRACE, so discovery through CloudWatch is best effort. Use S3 for complete or historical discovery, and use CloudWatch for quick, recent windows. The default window is the last 24 hours. Override it with --start-time and --end-time in RFC3339.

The CloudWatch source runs a CloudWatch Logs Insights query that filters and removes duplicates on the server, so kcp downloads only one line for each distinct client. The query needs the logs:StartQuery and logs:GetQueryResults permissions and is billed per GB of log data scanned, so use a narrow window. A query returns at most 10,000 distinct rows. If a window exceeds that, the scan fails and asks you to narrow it. If you can't grant the query permissions, use the S3 source.

kcp keys each client by ` + "`clientID|topic|role|auth|principal`" + `, for example ` + "`stock-levels-producer|stock-levels|Producer|IAM|arn:aws:sts::...`" + `. A later run appends only the clients that don't already match an existing key.`,
		Example: `  # From S3
  kcp scan client-inventory \
      --logs s3 \
      --s3-uri s3://my-cluster-logs/AWSLogs/000123456789/KafkaBrokerLogs/us-east-1/msk-cluster-xxxx-5/2025-08-13-14/ \
      --state-file kcp-state.json

  # From CloudWatch Logs (defaults to the last 24h)
  kcp scan client-inventory \
      --logs cloudwatch \
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
	requiredFlags.StringVar(&logs, "logs", "", "Where to read broker logs from: s3 or cloudwatch. Each source accepts only its own flags and rejects the other source's flags.")

	s3SourceFlags := pflag.NewFlagSet("s3-source", pflag.ExitOnError)
	s3SourceFlags.SortFlags = false
	s3SourceFlags.StringVar(&s3Uri, "s3-uri", "", "Read broker logs from this S3 URI (for example, s3://my-bucket/AWSLogs/<acct>/KafkaBrokerLogs/<region>/<cluster>/<date>/). Region and cluster name are derived from the URI.")

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
		groupNames := []string{"Required Flags", "S3 Source Flags (--logs s3)", "CloudWatch Source Flags (--logs cloudwatch)"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("Choose --logs s3 or --logs cloudwatch. Each source accepts only its own flags.")
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	_ = clientInventoryCmd.MarkFlagRequired("state-file")
	_ = clientInventoryCmd.MarkFlagRequired("logs")
	// Per-source flag rules live in parseClientInventoryFlags, so the error messages stay
	// specific and the rules are unit tested in one place.

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
		logs:         logs,
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
		slog.Warn("CloudWatch delivers only a sample of broker logs at TRACE; use the S3 source (--s3-uri) for complete or historical client discovery")

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
	logs         string
	s3Uri        string
	logGroupName string
	region       string
	clusterName  string
	startTime    string
	endTime      string
	stateFile    string
}

// cloudWatchOnlyFlag is a flag that only the CloudWatch source accepts.
type cloudWatchOnlyFlag struct {
	name  string
	value string
}

// resolveSource validates --logs and rejects every flag that belongs to the other source.
func resolveSource(in rawFlags) (string, error) {
	chosen := strings.ToLower(strings.TrimSpace(in.logs))
	switch chosen {
	case "":
		return "", fmt.Errorf("--logs is required: use --logs s3 with --s3-uri, or --logs cloudwatch with --log-group-name")
	case sourceS3, sourceCloudWatch:
	default:
		return "", fmt.Errorf("invalid --logs %q (want %q or %q; if you did not pass --logs, unset the LOGS environment variable)", in.logs, sourceS3, sourceCloudWatch)
	}

	switch chosen {
	case sourceS3:
		if in.logGroupName != "" {
			return "", fmt.Errorf("--log-group-name is only valid with --logs cloudwatch")
		}
		for _, f := range []cloudWatchOnlyFlag{
			{"--region", in.region},
			{"--cluster-name", in.clusterName},
			{"--start-time", in.startTime},
			{"--end-time", in.endTime},
		} {
			if f.value != "" {
				// The env var name is in the message because kcp reads flags from the environment too.
				envName := strings.ToUpper(strings.ReplaceAll(strings.TrimPrefix(f.name, "--"), "-", "_"))
				return "", fmt.Errorf("%s is only valid with --logs cloudwatch; the S3 source derives it from --s3-uri (remove the flag, or unset the %s environment variable if it is set)", f.name, envName)
			}
		}
		if in.s3Uri == "" {
			return "", fmt.Errorf("--s3-uri is required when --logs is s3")
		}
	case sourceCloudWatch:
		if in.s3Uri != "" {
			return "", fmt.Errorf("--s3-uri is only valid with --logs s3")
		}
		if in.logGroupName == "" {
			return "", fmt.Errorf("--log-group-name is required when --logs is cloudwatch")
		}
	}
	return chosen, nil
}

// parseClientInventoryFlags validates the flags and builds the scanner options,
// returning whether the CloudWatch source was selected. resolveSource validates
// --logs and rejects the other source's flags; this function then derives the
// per-source values and validates CloudWatch's region/cluster/time inputs.
func parseClientInventoryFlags(in rawFlags) (*ClientInventoryScannerOpts, bool, error) {
	chosen, err := resolveSource(in)
	if err != nil {
		return nil, false, err
	}

	if chosen == sourceCloudWatch {
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
