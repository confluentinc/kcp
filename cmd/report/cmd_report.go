package report

import (
	"github.com/confluentinc/kcp/cmd/report/costs"
	"github.com/confluentinc/kcp/cmd/report/metrics"
	"github.com/spf13/cobra"
)

func NewReportCmd() *cobra.Command {
	reportCmd := &cobra.Command{
		Use:           "report",
		Short:         "Generate a report of costs for given region(s)",
		Long:          "Generate a report of costs for the given region(s) based on the data collected by `kcp discover`",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	reportCmd.AddCommand(costs.NewReportCostsCmd())
	reportCmd.AddCommand(metrics.NewReportMetricsCmd())

	return reportCmd
}
