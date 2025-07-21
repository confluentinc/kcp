package report

import (
	rc "github.com/confluentinc/kcp/internal/cli/report/cluster"
	rr "github.com/confluentinc/kcp/internal/cli/report/region"
	"github.com/spf13/cobra"
)

func NewReportCmd() *cobra.Command {
	reportCmd := &cobra.Command{
		Use:   "report",
		Short: "Generate reports on migration planning",
		Long:  "Generate reports on migration planning",
	}

	reportCmd.AddCommand(
		rc.NewReportClusterCmd(),
		rr.NewReportRegionCmd(),
	)

	return reportCmd
}
