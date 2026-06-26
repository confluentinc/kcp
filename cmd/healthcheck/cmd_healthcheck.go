package healthcheck

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/services/iampolicy"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	sourceType      string
	stateFile       string
	credentialsFile string
	outputFile      string
)

func healthcheckIAMAnnotation() string {
	return iampolicy.RenderStatements(
		"Only required for `--source-type msk`. Apache Kafka healthchecks use credentials from the credentials file, not AWS IAM.",
		[]iampolicy.Statement{
			{
				Sid: "MSKClusterKafkaAccess",
				Actions: []string{
					"kafka-cluster:Connect",
					"kafka-cluster:DescribeCluster",
					"kafka-cluster:DescribeClusterDynamicConfiguration",
					"kafka-cluster:DescribeTopic",
				},
				Resources: []string{
					"arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:topic/<MSK CLUSTER NAME>/<MSK CLUSTER ID>/*",
					"arn:aws:kafka:<AWS REGION>:<AWS ACCOUNT ID>:cluster/<MSK CLUSTER NAME>/<MSK CLUSTER ID>",
				},
			},
		},
	)
}

func NewHealthcheckCmd() *cobra.Command {
	healthcheckCmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Run a live healthcheck against a Kafka cluster",
		Long: `Run a live operational healthcheck against a Kafka cluster via the Kafka Admin API.

The healthcheck connects to the cluster, inventories topics, ACLs, and broker metadata, and writes a markdown report. A short summary is also printed to stdout.

Source-specific notes:

- ` + "`--source-type msk`" + ` reads cluster connection details from the ` + "`msk-credentials.yaml`" + ` file produced by ` + "`kcp discover`" + ` and the discovery state file. SCRAM is forced to SHA-512 (the only mechanism MSK supports).
- ` + "`--source-type apache-kafka`" + ` reads from a hand-authored ` + "`apache-kafka-credentials.yaml`" + ` file.`,
		Example: `  # Healthcheck an Apache Kafka cluster
  kcp healthcheck --source-type apache-kafka --credentials-file apache-kafka-credentials.yaml

  # Healthcheck an MSK cluster (credentials and state from kcp discover)
  kcp healthcheck --source-type msk --state-file kcp-state.json --credentials-file msk-credentials.yaml

  # Specify the output markdown file
  kcp healthcheck --source-type apache-kafka --credentials-file apache-kafka-credentials.yaml \
      --output ./my-healthcheck.md`,
		Annotations: map[string]string{
			iampolicy.AnnotationKey: healthcheckIAMAnnotation(),
		},
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunHealthcheck,
		RunE:          runHealthcheck,
		// Hidden while we decide on the migration-readiness framing and
		// the discover-gated vs first-touch invocation model. The
		// command is wired up and works (so other refactors can rely on
		// the struct surface), but is intentionally absent from
		// `kcp --help` and the generated command reference so users
		// don't discover it before the UX is settled.
		Hidden: true,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&sourceType, "source-type", "", "Source type: 'msk' or 'apache-kafka' (required)")
	requiredFlags.StringVar(&stateFile, "state-file", "kcp-state.json", "Path to the KCP state file (required for --source-type msk)")
	requiredFlags.StringVar(&credentialsFile, "credentials-file", "", "Path to credentials file (msk-credentials.yaml or apache-kafka-credentials.yaml)")
	healthcheckCmd.Flags().AddFlagSet(requiredFlags)

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&outputFile, "output", "", "Path for the generated markdown report. Defaults to ./healthcheck-<cluster-id>-<timestamp>.md.")
	healthcheckCmd.Flags().AddFlagSet(optionalFlags)

	_ = healthcheckCmd.MarkFlagRequired("source-type")
	_ = healthcheckCmd.MarkFlagRequired("credentials-file")

	return healthcheckCmd
}

func preRunHealthcheck(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	// Validate and normalize source type. "apache-kafka" is the user-facing value;
	// internally the source is represented by the "osk" token.
	normalizedSourceType, err := types.ParseSourceTypeFlag(sourceType)
	if err != nil {
		return err
	}
	sourceType = string(normalizedSourceType)

	// Credentials file naming convention — warn rather than reject so a
	// renamed file still works, mirroring `scan clusters`.
	if sourceType == "msk" && filepath.Base(credentialsFile) != "msk-credentials.yaml" {
		slog.Warn("credentials file should be named 'msk-credentials.yaml' for MSK sources", "file", credentialsFile)
	}
	if sourceType == "osk" && filepath.Base(credentialsFile) != "apache-kafka-credentials.yaml" {
		slog.Warn("credentials file should be named 'apache-kafka-credentials.yaml' for Apache Kafka sources", "file", credentialsFile)
	}

	// MSK requires a populated state file (produced by `kcp discover`) to
	// resolve broker addresses for each cluster ARN.
	if sourceType == "msk" && stateFile == "" {
		return fmt.Errorf("--state-file is required when --source-type msk (run 'kcp discover' first)")
	}

	return nil
}
