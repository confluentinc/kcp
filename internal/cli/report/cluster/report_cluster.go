package cluster

import (
	"github.com/spf13/cobra"
)

func NewReportClusterCmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:   "cluster",
		Short: "Generate Report on an AWS MSK cluster",
		Long:  `Generate a report on an AWS MSK cluster.`,
	}

	clusterCmd.AddCommand(
	// rcm.NewReportClusterMetricsCmd(),
	)

	return clusterCmd
}
