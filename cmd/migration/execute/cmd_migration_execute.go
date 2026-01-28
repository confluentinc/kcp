package migration_execute

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile string
	migrationId string
)

func NewMigrationExecuteCmd() *cobra.Command {
	migrationExecuteCmd := &cobra.Command{
		Use:           "execute",
		Short:         "PLACEHOLDER",
		Long:          "PLACEHOLDER",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationInit,
		RunE:          runMigrationInit,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the state file to use for the migration.")

	requiredFlags.StringVar(&migrationId, "migration-id", "", "The ID of the migration to execute.")

	migrationExecuteCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	migrationExecuteCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags}
		groupNames := []string{"Required Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	migrationExecuteCmd.MarkFlagRequired("state-file")
	migrationExecuteCmd.MarkFlagRequired("migration-id")


	return migrationExecuteCmd
}

func preRunMigrationInit(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrationInit(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrationOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migration opts: %v", err)
	}

	migrationExecute := NewMigrationExecute(*opts)
	if err := migrationExecute.Run(); err != nil {
		return err
	}

	return nil
}

func parseMigrationOpts() (*MigrationExecuteOpts, error) {
	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	return &MigrationExecuteOpts{
		stateFile: stateFile,
		state:     *state,
		migrationId: migrationId,
	}, nil
}
