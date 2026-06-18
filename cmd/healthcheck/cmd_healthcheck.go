package healthcheck

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	sourceType      string
	credentialsFile string
	outputFile      string
)

func NewHealthcheckCmd() *cobra.Command {
	healthcheckCmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Run a live healthcheck against a Kafka cluster",
		Long: `Run a live operational healthcheck against a Kafka cluster via the Kafka Admin API.

The healthcheck connects to the cluster, inventories topics, ACLs, and broker metadata, and writes a markdown report. A short summary is also printed to stdout.

v1 supports Apache Kafka sources only. MSK support is planned as a follow-up.`,
		Example: `  # Healthcheck an Apache Kafka cluster
  kcp healthcheck --source-type apache-kafka --credentials-file apache-kafka-credentials.yaml

  # Specify the output markdown file
  kcp healthcheck --source-type apache-kafka --credentials-file apache-kafka-credentials.yaml \
      --output ./my-healthcheck.md`,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunHealthcheck,
		RunE:          runHealthcheck,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&sourceType, "source-type", "", "Source type: 'apache-kafka' (required). MSK support is planned for a future release.")
	requiredFlags.StringVar(&credentialsFile, "credentials-file", "", "Path to credentials file (e.g. apache-kafka-credentials.yaml)")
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

	switch sourceType {
	case "apache-kafka":
		// supported
	case "msk":
		return fmt.Errorf("--source-type msk is not yet supported for healthcheck; v1 supports apache-kafka only")
	default:
		return fmt.Errorf("invalid --source-type %q; supported values: apache-kafka", sourceType)
	}

	return nil
}
