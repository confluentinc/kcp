// Package connect groups commands that discover Kafka Connect resources from
// a Kafka cluster — currently `kcp scan connect clusters`.
package connect

import (
	"github.com/confluentinc/kcp/cmd/scan/connect/clusters"
	"github.com/spf13/cobra"
)

func NewScanConnectCmd() *cobra.Command {
	connectCmd := &cobra.Command{
		Use:           "connect",
		Short:         "Discover Kafka Connect resources from a Kafka cluster",
		Long:          "Commands that read Kafka Connect-related topics on a Kafka cluster to surface Connect clusters and related metadata. Today this group contains the `clusters` subcommand, which extracts Connect worker URLs from the `connect-status` topic.",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	connectCmd.AddCommand(clusters.NewScanConnectClustersCmd())
	return connectCmd
}
