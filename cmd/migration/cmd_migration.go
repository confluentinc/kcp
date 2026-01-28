package migration

import (
	migration_init "github.com/confluentinc/kcp/cmd/migration/init"
	migration_execute "github.com/confluentinc/kcp/cmd/migration/execute"

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
		migration_execute.NewMigrationExecuteCmd(),
	)

	return migraitonCmd
}
