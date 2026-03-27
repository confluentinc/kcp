package scan

import (
	"github.com/confluentinc/kcp/cmd/scan/client_inventory"
	"github.com/confluentinc/kcp/cmd/scan/clusters"
	"github.com/confluentinc/kcp/cmd/scan/schema_registry"
	"github.com/confluentinc/kcp/cmd/scan/self_managed_connectors"
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
		schema_registry.NewScanSchemaRegistryCmd(),
		self_managed_connectors.NewScanSelfManagedConnectorsCmd(),
	)

	return scanCmd
}
