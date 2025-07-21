package cluster

import (
	rcm "github.com/confluentinc/kcp/internal/cli/report/cluster/metrics"
	"github.com/spf13/cobra"
)

func NewReportClusterCmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:   "cluster",
		Short: "Generate cluster reports",
		Long:  "Generate reports for cluster costs and metrics",
	}

	clusterCmd.AddCommand(
		rcm.NewReportClusterMetricsCmd(),
	)

	return clusterCmd
}
