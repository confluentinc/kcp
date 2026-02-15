package list

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	migrationStateFile string
)

func NewMigrationListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List all migrations from the migration state file",
		Long:          "Display all migrations from the migration state file in a human-readable format, showing migration IDs, status, gateway configuration, and topics.",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationList,
		RunE:          runMigrationList,
	}

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&migrationStateFile, "migration-state-file", "migration-state.json", "The path to the migration state file to read.")
	cmd.Flags().AddFlagSet(optionalFlags)

	cmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		fmt.Printf("Optional:\n%s\n", optionalFlags.FlagUsages())
		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")
		return nil
	})

	return cmd
}

func preRunMigrationList(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runMigrationList(cmd *cobra.Command, args []string) error {
	// Load migration state (following KCP pattern)
	migrationState, err := types.NewMigrationStateFromFile(migrationStateFile)
	if err != nil {
		return fmt.Errorf("failed to load migration state file: %s\nEnsure the file exists or run 'kcp migration init' to create a new migration", migrationStateFile)
	}

	opts := MigrationListerOpts{
		MigrationStateFile: migrationStateFile,
		MigrationState:     *migrationState,
	}

	lister := NewMigrationLister(opts)
	return lister.Run()
}
