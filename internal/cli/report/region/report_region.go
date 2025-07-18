package region

import (
	rrc "github.com/confluentinc/kcp-internal/internal/cli/report/region/costs"
	rrm "github.com/confluentinc/kcp-internal/internal/cli/report/region/metrics"
	"github.com/spf13/cobra"
)

func NewReportRegionCmd() *cobra.Command {
	regionCmd := &cobra.Command{
		Use:   "region",
		Short: "Generate Report on an AWS region",
		Long:  `Generate a report on an AWS region.`,
	}

	regionCmd.AddCommand(
		rrc.NewReportRegionCostsCmd(),
		rrm.NewReportRegionMetricsCmd(),
	)

	return regionCmd
}
