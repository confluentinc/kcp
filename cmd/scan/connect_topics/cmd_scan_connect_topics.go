package connect_topics

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/confluentinc/kcp/internal/services/iampolicy"
	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/msk"
	"github.com/confluentinc/kcp/internal/sources/osk"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile       string
	credentialsFile string
	clusterID       string
	topics          []string
)

func scanConnectTopicsIAMAnnotation() string {
	return iampolicy.RenderStatements(
		"Only required for MSK clusters (those whose --cluster-id is an AWS ARN). OSK scans authenticate via the credentials file, not AWS IAM. `<TOPIC NAME>` placeholders should be repeated per `--topics` value.",
		[]iampolicy.Statement{
			{
				Sid: "MSKConnectStatusTopicAccess",
				Actions: []string{
					"kafka-cluster:Connect",
					"kafka-cluster:DescribeTopic",
					"kafka-cluster:ReadData",
				},
				Resources: []string{
					"arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:cluster/<MSK CLUSTER NAME>/<MSK CLUSTER ID>",
					"arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/<TOPIC NAME>",
				},
			},
		},
	)
}

func NewScanConnectTopicsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connect-topics",
		Short: "Discover Kafka Connect cluster addresses by parsing the connect-status topic",
		Long: `Read one or more Kafka topics that play the role of Kafka Connect's default ` + "`connect-status`" + ` storage topic, parse the messages for worker_id values, and print the unique Kafka Connect worker addresses to stdout.

The goal is to surface Kafka Connect clusters that may be running against a Kafka cluster without the operator's knowledge — orthogonal to ` + "`kcp scan self-managed-connectors`" + `, which inventories a Connect cluster the operator already knows the REST URL of.`,
		Example: `  # Discover Connect worker addresses for one OSK cluster
  kcp scan connect-topics \
      --credentials-file osk-credentials.yaml \
      --state-file kcp-state.json \
      --cluster-id production-kafka \
      --topics connect-status

  # MSK cluster (source type resolved automatically from state)
  kcp scan connect-topics \
      --credentials-file msk-credentials.yaml \
      --state-file kcp-state.json \
      --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc-123 \
      --topics connect-status

  # Multiple status topics (e.g. several Connect clusters sharing one Kafka cluster)
  kcp scan connect-topics \
      --credentials-file osk-credentials.yaml \
      --state-file kcp-state.json \
      --cluster-id production-kafka \
      --topics connect-status-A,connect-status-B`,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: scanConnectTopicsIAMAnnotation(),
		},
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanConnectTopics,
		RunE:          runScanConnectTopics,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "Path to the KCP state file (used to resolve --cluster-id to MSK or OSK)")
	requiredFlags.StringVar(&credentialsFile, "credentials-file", "", "Path to credentials file (msk-credentials.yaml or osk-credentials.yaml)")
	requiredFlags.StringVar(&clusterID, "cluster-id", "", "Cluster identifier from the credentials file. Accepts both MSK ARNs (arn:aws:kafka:...) and OSK cluster IDs.")
	requiredFlags.StringSliceVar(&topics, "topics", nil, "Comma-separated list (or repeated flag) of Kafka topic names that play the role of 'connect-status'")
	cmd.Flags().AddFlagSet(requiredFlags)

	cmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		if c.Example != "" {
			fmt.Printf("Examples:\n%s\n\n", c.Example)
		}
		fmt.Printf("Required Flags:\n%s\n", requiredFlags.FlagUsages())
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	_ = cmd.MarkFlagRequired("state-file")
	_ = cmd.MarkFlagRequired("credentials-file")
	_ = cmd.MarkFlagRequired("cluster-id")
	_ = cmd.MarkFlagRequired("topics")

	return cmd
}

func preRunScanConnectTopics(cmd *cobra.Command, _ []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	// Normalise --topics: strip whitespace and drop empty entries.
	var cleaned []string
	for _, t := range topics {
		t = strings.TrimSpace(t)
		if t != "" {
			cleaned = append(cleaned, t)
		}
	}
	if len(cleaned) == 0 {
		return fmt.Errorf("--topics requires at least one non-empty topic name")
	}
	topics = cleaned

	// Validate the state file loads. Per R8 we do not mutate it. Skip when
	// stateFile is empty so Cobra's MarkFlagRequired (which runs after PreRunE)
	// can surface the canonical "required flag" error.
	if stateFile != "" {
		if _, err := os.Stat(stateFile); os.IsNotExist(err) {
			return fmt.Errorf("state file does not exist: %s", stateFile)
		}
		if _, err := types.NewStateFromFile(stateFile); err != nil {
			return fmt.Errorf("failed to load state file %q: %w", stateFile, err)
		}
	}

	return nil
}

func runScanConnectTopics(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return fmt.Errorf("failed to load state file: %w", err)
	}

	sourceType, err := utils.InferSourceTypeFromClusterID(state, clusterID)
	if err != nil {
		return err
	}

	if sourceType == types.SourceTypeMSK && filepath.Base(credentialsFile) != "msk-credentials.yaml" {
		slog.Warn("credentials file should be named 'msk-credentials.yaml' for MSK sources", "file", credentialsFile)
	}
	if sourceType == types.SourceTypeOSK && filepath.Base(credentialsFile) != "osk-credentials.yaml" {
		slog.Warn("credentials file should be named 'osk-credentials.yaml' for OSK sources", "file", credentialsFile)
	}

	var source sources.Source
	switch sourceType {
	case types.SourceTypeMSK:
		source = msk.NewMSKSource()
	case types.SourceTypeOSK:
		source = osk.NewOSKSource()
	default:
		return fmt.Errorf("unsupported source type: %s", sourceType)
	}

	if err := source.LoadCredentials(credentialsFile); err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	scanner := NewConnectTopicsScanner(ConnectTopicsScannerOpts{
		Source:    source,
		State:     state,
		ClusterID: clusterID,
		Topics:    topics,
		Stdout:    cmd.OutOrStdout(),
		Stderr:    cmd.ErrOrStderr(),
	})

	return scanner.Run(ctx)
}
