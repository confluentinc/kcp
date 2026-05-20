package report

import (
	"github.com/confluentinc/kcp/cmd/report/costs"
	"github.com/confluentinc/kcp/cmd/report/metrics"
	"github.com/confluentinc/kcp/cmd/report/plan"
	"github.com/spf13/cobra"
)

func NewReportCmd() *cobra.Command {
	reportCmd := &cobra.Command{
		Use:           "report",
		Short:         "Generate reports (costs, metrics, migration plan) from kcp scan data",
		Long:          "Generate reports from the data collected by `kcp discover` / `kcp scan ...`. Subcommands: `costs` (AWS bill reconciliation), `metrics` (CloudWatch throughput aggregates), `plan` (deterministic migration plan).",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	reportCmd.AddCommand(costs.NewReportCostsCmd())
	reportCmd.AddCommand(metrics.NewReportMetricsCmd())
	reportCmd.AddCommand(plan.NewReportPlanCmd())

	return reportCmd
}
