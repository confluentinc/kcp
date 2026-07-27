// Package txndiscovery wires `kcp migration txn-discovery`: the flags, the
// preflight, and the run orchestration that drives the txndiscovery services.
package txndiscovery

import (
	"github.com/spf13/cobra"
)

// NewMigrationTxnDiscoveryCmd builds the `kcp migration txn-discovery` command.
func NewMigrationTxnDiscoveryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "txn-discovery",
		Short:         "Discover which topics are coupled by Kafka transactions and must migrate together",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
	}

	return cmd
}
