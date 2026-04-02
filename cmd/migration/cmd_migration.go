package migration

import (
	"github.com/confluentinc/kcp/cmd/migration/execute"
	i "github.com/confluentinc/kcp/cmd/migration/init"
	"github.com/confluentinc/kcp/cmd/migration/lagcheck"
	"github.com/confluentinc/kcp/cmd/migration/list"

	"github.com/spf13/cobra"
)

func NewMigrationCmd() *cobra.Command {
	migrationCmd := &cobra.Command{
		Use:           "migration",
		Short:         "Commands for migrating using CPC Gateway.",
		Long:          "Commands for migrating using CPC Gateway.",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	migrationCmd.AddCommand(
		i.NewMigrationInitCmd(),
		execute.NewMigrationExecuteCmd(),
		lagcheck.NewMigrationLagCheckCmd(),
		list.NewMigrationListCmd(),
	)

	return migrationCmd
}
