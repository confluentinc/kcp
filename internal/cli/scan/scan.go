package scan

import (
	"github.com/confluentinc/kcp/internal/cli/scan/client_inventory"
	"github.com/confluentinc/kcp/internal/cli/scan/clusters"
	"github.com/spf13/cobra"
)

func NewScanCmd() *cobra.Command {
	scanCmd := &cobra.Command{
		Use:           "scan",
		Short:         "Scan AWS resources for migration planning",
		Long:          "Scan AWS resources like MSK clusters and regions to gather information for migration planning",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	scanCmd.AddCommand(
		client_inventory.NewScanClientInventoryCmd(),
		clusters.NewScanClustersCmd(),
	)

	return scanCmd
}
