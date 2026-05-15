package clusters

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
	sourceType      string
	clusterID       string
	topics          []string
)

func scanConnectClustersIAMAnnotation() string {
	return iampolicy.RenderStatements(
		"Only required for `--source-type msk`. OSK scans use credentials from the credentials file, not AWS IAM. `<TOPIC NAME>` placeholders should be repeated per `--topics` value.",
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

func NewScanConnectClustersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clusters",
		Short: "Discover Kafka Connect cluster addresses by parsing the connect-status topic",
		Long: `Read one or more Kafka topics that play the role of Kafka Connect's default ` + "`connect-status`" + ` storage topic, parse the messages for worker_id values, and print the unique Kafka Connect worker addresses to stdout.

The goal is to surface Kafka Connect clusters that may be running against a Kafka cluster without the operator's knowledge — orthogonal to ` + "`kcp scan self-managed-connectors`" + `, which inventories a Connect cluster the operator already knows the REST URL of.`,
		Example: `  # Discover Connect worker addresses for one OSK cluster
  kcp scan connect clusters \
      --credentials-file osk-credentials.yaml \
      --state-file kcp-state.json \
      --cluster-id production-kafka \
      --topics connect-status

  # MSK cluster (source-type auto-detected from the ARN)
  kcp scan connect clusters \
      --credentials-file msk-credentials.yaml \
      --state-file kcp-state.json \
      --cluster-id arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc-123 \
      --topics connect-status

  # Multiple status topics (e.g. several Connect clusters sharing one Kafka cluster)
  kcp scan connect clusters \
      --credentials-file osk-credentials.yaml \
      --state-file kcp-state.json \
      --cluster-id production-kafka \
      --topics connect-status-A,connect-status-B`,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: scanConnectClustersIAMAnnotation(),
		},
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanConnectClusters,
		RunE:          runScanConnectClusters,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "Path to the KCP state file (read-only in this phase; required for forward compatibility)")
	requiredFlags.StringVar(&credentialsFile, "credentials-file", "", "Path to credentials file (msk-credentials.yaml or osk-credentials.yaml)")
	requiredFlags.StringVar(&clusterID, "cluster-id", "", "Cluster identifier from the credentials file. Accepts both MSK ARNs (arn:aws:kafka:...) and OSK cluster IDs.")
	requiredFlags.StringSliceVar(&topics, "topics", nil, "Comma-separated list (or repeated flag) of Kafka topic names that play the role of 'connect-status'")
	cmd.Flags().AddFlagSet(requiredFlags)

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&sourceType, "source-type", "", "Source type: 'msk' or 'osk'. If not specified, auto-detects from cluster-id format (ARN = MSK, non-ARN = OSK).")
	cmd.Flags().AddFlagSet(optionalFlags)

	cmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		if c.Example != "" {
			fmt.Printf("Examples:\n%s\n\n", c.Example)
		}
		fmt.Printf("Required Flags:\n%s\n", requiredFlags.FlagUsages())
		fmt.Printf("Optional Flags:\n%s\n", optionalFlags.FlagUsages())
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	_ = cmd.MarkFlagRequired("state-file")
	_ = cmd.MarkFlagRequired("credentials-file")
	_ = cmd.MarkFlagRequired("cluster-id")
	_ = cmd.MarkFlagRequired("topics")

	return cmd
}

func preRunScanConnectClusters(cmd *cobra.Command, _ []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	// Resolve source type: explicit --source-type wins; otherwise infer from
	// the cluster-id shape (matches `kcp scan self-managed-connectors`). When
	// both are empty, skip validation so Cobra's MarkFlagRequired for
	// --cluster-id (which runs after PreRunE) can surface the canonical
	// "required flag" error.
	if sourceType == "" && clusterID != "" {
		if strings.HasPrefix(clusterID, "arn:") {
			sourceType = "msk"
		} else {
			sourceType = "osk"
		}
	}
	if sourceType != "" && sourceType != "msk" && sourceType != "osk" {
		return fmt.Errorf("invalid source-type '%s': must be 'msk' or 'osk'", sourceType)
	}

	if sourceType == "msk" && filepath.Base(credentialsFile) != "msk-credentials.yaml" {
		slog.Warn("credentials file should be named 'msk-credentials.yaml' for MSK sources", "file", credentialsFile)
	}
	if sourceType == "osk" && filepath.Base(credentialsFile) != "osk-credentials.yaml" {
		slog.Warn("credentials file should be named 'osk-credentials.yaml' for OSK sources", "file", credentialsFile)
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

func runScanConnectClusters(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return fmt.Errorf("failed to load state file: %w", err)
	}

	var source sources.Source
	switch sourceType {
	case "msk":
		source = msk.NewMSKSource()
	case "osk":
		source = osk.NewOSKSource()
	default:
		return fmt.Errorf("unsupported source type: %s", sourceType)
	}

	if err := source.LoadCredentials(credentialsFile); err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	scanner := NewConnectClustersScanner(ConnectClustersScannerOpts{
		Source:    source,
		State:     state,
		ClusterID: clusterID,
		Topics:    topics,
		Stdout:    cmd.OutOrStdout(),
		Stderr:    cmd.ErrOrStderr(),
	})

	return scanner.Run(ctx)
}
