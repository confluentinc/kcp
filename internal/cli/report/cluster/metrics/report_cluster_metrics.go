package metrics

import (
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
)

var reportClusterMetricsRequiredEnvVars = []string{
	"aws_region",
	"aws_access_key",
	"aws_access_secret",
}

func NewReportClusterMetricsCmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Hidden: true,
		Use:    "metrics",
		Short:  "Generate metrics report on an msk cluster",
		Long: `Generate a metrics report on an msk cluster.

The env-file must contain the following environment variables:

` + strings.Join(reportClusterMetricsRequiredEnvVars, "\n"),
		SilenceErrors: true,
		RunE:          runReportClusterMetrics,
	}

	clusterCmd.Flags().StringP("env-file", "e", "", "env file")
	clusterCmd.MarkFlagRequired("env-file")

	return clusterCmd
}

func runReportClusterMetrics(cmd *cobra.Command, args []string) error {
	slog.Info("running report cluster metrics")

	return nil
}
