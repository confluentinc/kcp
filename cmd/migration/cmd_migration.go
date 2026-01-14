package migration

import (
	migration_init "github.com/confluentinc/kcp/cmd/migration/init"

	"github.com/spf13/cobra"
)

func NewMigrationCmd() *cobra.Command {
	migraitonCmd := &cobra.Command{
		Use:           "migration",
		Short:         "Commands for migrating using CPC Gateway.",
		Long:          "Commands for migrating using CPC Gateway.",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	migraitonCmd.AddCommand(
		migration_init.NewMigrationInitCmd(),
	)

	return migraitonCmd
}
