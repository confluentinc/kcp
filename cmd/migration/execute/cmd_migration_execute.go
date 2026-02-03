package execute

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile   string
	migrationId string
	maxLag      int64
	maxWaitTime int64 // in seconds
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
	requiredFlags.Int64Var(&maxLag, "max-lag", 0, "Maximum replication lag in milliseconds before proceeding with migration.")
	requiredFlags.Int64Var(&maxWaitTime, "max-wait-time", 0, "Maximum time in seconds to wait for lags to decrease below threshold.")

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
	migrationExecuteCmd.MarkFlagRequired("max-lag")
	migrationExecuteCmd.MarkFlagRequired("max-wait-time")


	return migrationExecuteCmd
}

func preRunMigrationInit(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrationInit(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrationExecutorOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migration execute opts: %v", err)
	}

	migrationExecutor := NewMigrationExecutor(*opts)
	if err := migrationExecutor.Run(); err != nil {
		return err
	}

	return nil
}

func parseMigrationExecutorOpts() (*MigrationExecutorOpts, error) {
	return &MigrationExecutorOpts{
		stateFile:   stateFile,
		migrationId: migrationId,
		maxLag:      maxLag,
		maxWaitTime: maxWaitTime,
	}, nil
}
