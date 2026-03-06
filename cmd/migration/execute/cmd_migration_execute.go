package execute

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	migrationStateFile string
	migrationId        string
	lagThreshold       int64
	clusterApiKey      string
	clusterApiSecret   string
)

func NewMigrationExecuteCmd() *cobra.Command {
	migrationExecuteCmd := &cobra.Command{
		Use:   "execute",
		Short: "Execute an initialized migration",
		Long: `Execute an initialized migration through its remaining workflow steps.

This command resumes a migration from its current state, progressing through:
lag checking, gateway fencing, topic promotion, and gateway switchover.

The migration must first be created with 'kcp migration init'. If execution is
interrupted, re-running this command will resume from the last completed step.`,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationExecute,
		RunE:          runMigrationExecute,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&migrationStateFile, "migration-state-file", "migration-state.json", "Path to the migration state file.")
	requiredFlags.StringVar(&migrationId, "migration-id", "", "ID of the migration to execute (from 'kcp migration list').")
	requiredFlags.Int64Var(&lagThreshold, "lag-threshold", 0, "Total topic replication lag threshold (sum of all partition lags) before proceeding with migration.")
	requiredFlags.StringVar(&clusterApiKey, "cluster-api-key", "", "API key for authenticating with the destination cluster.")
	requiredFlags.StringVar(&clusterApiSecret, "cluster-api-secret", "", "API secret for authenticating with the destination cluster.")
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

	migrationExecuteCmd.MarkFlagRequired("migration-id")
	migrationExecuteCmd.MarkFlagRequired("lag-threshold")
	migrationExecuteCmd.MarkFlagRequired("cluster-api-key")
	migrationExecuteCmd.MarkFlagRequired("cluster-api-secret")

	return migrationExecuteCmd
}

func preRunMigrationExecute(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrationExecute(cmd *cobra.Command, args []string) error {
	// Load migration state (following established pattern)
	migrationState, err := types.NewMigrationStateFromFile(migrationStateFile)
	if err != nil {
		return fmt.Errorf("migration state file not found: %s\nRun 'kcp migration init' to create a new migration first", migrationStateFile)
	}

	// Get MigrationConfig by ID with two-level error handling
	config, err := migrationState.GetMigrationById(migrationId)
	if err != nil {
		return fmt.Errorf("migration '%s' not found in %s\nRun 'kcp migration list' to see available migrations", migrationId, migrationStateFile)
	}

	opts := parseMigrationExecutorOpts(*migrationState, *config)

	migrationExecutor := NewMigrationExecutor(opts)
	if err := migrationExecutor.Run(); err != nil {
		return err
	}

	return nil
}

func parseMigrationExecutorOpts(migrationState types.MigrationState, config types.MigrationConfig) MigrationExecutorOpts {
	return MigrationExecutorOpts{
		MigrationStateFile: migrationStateFile,
		MigrationState:     migrationState,
		MigrationConfig:    config,
		LagThreshold:       lagThreshold,
		ClusterApiKey:      clusterApiKey,
		ClusterApiSecret:   clusterApiSecret,
	}
}
