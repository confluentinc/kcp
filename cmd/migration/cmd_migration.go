package migration

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewMigrationCmd returns a hidden, deprecated stand-in for the old
// `kcp migration` command, which has been renamed to `kcp cutover`.
//
// It is hidden from help, but if anyone invokes it — including with the old
// subcommands and flags, e.g. `kcp migration execute --migration-id ...` — it
// prints a clear pointer to the new command and exits non-zero, so existing
// scripts and muscle memory get a helpful message instead of a bare
// "unknown command" error (and so a script never mistakes the no-op for success).
//
// DisableFlagParsing + ArbitraryArgs ensure every old invocation routes to the
// deprecation notice rather than failing on an unrecognised subcommand or flag.
func NewMigrationCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "migration",
		Short:              "Deprecated: renamed to 'kcp cutover'",
		Hidden:             true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("'kcp migration' has been renamed to 'kcp cutover' — please use 'kcp cutover' instead (e.g. 'kcp cutover init', 'kcp cutover execute', 'kcp cutover lag', 'kcp cutover list')")
		},
	}
}
