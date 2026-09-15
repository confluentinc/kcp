package list

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/migration"
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
		Short:         "List all migrations from a migration state file",
		Long:          "Display all migrations from a migration state file in a human-readable format, showing migration IDs, status, gateway configuration, and topics. There is no default file: since kcp migration execute now names each migration's state file after its own metadata.name, list has no single file to guess — pass the exact path.",
		Example:       `  kcp migration list --migration-state-file /path/to/msk-prod-to-cc-batch-1-state.json`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationList,
		RunE:          runMigrationList,
	}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&migrationStateFile, "migration-state-file", "", "The path to the migration state file to read.")
	cmd.Flags().AddFlagSet(requiredFlags)
	_ = cmd.MarkFlagRequired("migration-state-file")

	cmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)
		fmt.Printf("Required:\n%s\n", requiredFlags.FlagUsages())
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
	state, err := migration.NewMigrationStateFromFile(migrationStateFile)
	if err != nil {
		return fmt.Errorf("failed to load migration state file %q: %w\nEnsure the file exists or run 'kcp migration execute' to create a new migration", migrationStateFile, err)
	}

	opts := MigrationListerOpts{
		MigrationStateFile: migrationStateFile,
		MigrationState:     *state,
	}

	lister := NewMigrationLister(opts)
	return lister.Run()
}
