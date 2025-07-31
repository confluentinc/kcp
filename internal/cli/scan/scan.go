package scan

import (
	"github.com/confluentinc/kcp/internal/cli/scan/broker_logs"
	"github.com/confluentinc/kcp/internal/cli/scan/cluster"
	"github.com/confluentinc/kcp/internal/cli/scan/region"
	"github.com/spf13/cobra"
)

func NewScanCmd() *cobra.Command {
	scanCmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan AWS resources for migration planning",
		Long:  "Scan AWS resources like MSK clusters and regions to gather information for migration planning.",
	}

	scanCmd.AddCommand(
		cluster.NewScanClusterCmd(),
		region.NewScanRegionCmd(),
		broker_logs.NewScanBrokerLogsCmd(),
	)

	return scanCmd
}
