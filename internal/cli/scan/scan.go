package scan

import (
	"github.com/confluentinc/kcp/internal/cli/scan/client_inventory"
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
		client_inventory.NewScanClientInventoryCmd(),
	)

	return scanCmd
}
