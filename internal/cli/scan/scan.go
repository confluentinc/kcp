package scan

import (
	"github.com/confluentinc/kcp/internal/cli/scan/client_inventory"
	"github.com/confluentinc/kcp/internal/cli/scan/cluster"
	"github.com/confluentinc/kcp/internal/cli/scan/clusters"
	"github.com/confluentinc/kcp/internal/cli/scan/other"
	"github.com/confluentinc/kcp/internal/cli/scan/region"
	"github.com/spf13/cobra"
)

var (
	clusterCmd *cobra.Command
	regionCmd  *cobra.Command
)

func NewScanCmd() *cobra.Command {
	scanCmd := &cobra.Command{
		Use:           "scan",
		Short:         "Scan AWS resources for migration planning",
		Long:          "Scan AWS resources like MSK clusters and regions to gather information for migration planning",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	clusterCmd = cluster.NewScanClusterCmd()
	regionCmd = region.NewScanRegionCmd()
	scanCmd.AddCommand(
		cluster.NewScanClusterCmd(),
		region.NewScanRegionCmd(),
		client_inventory.NewScanClientInventoryCmd(),
		clusters.NewScanClustersCmd(),
		other.NewScanOtherCmd(),
	)

	return scanCmd
}
