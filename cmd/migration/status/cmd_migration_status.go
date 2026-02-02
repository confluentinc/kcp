package status

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	migrationId string

	optionalFlagPlaceholder string
)

func NewMigrationStatusCmd() *cobra.Command {
	migrationStatusCmd := &cobra.Command{
		Use:           "status",
		Short:         "Get the status of a migration",
		Long:          "Get the status of a migration",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunMigrationStatus,
		RunE:          runMigrationStatus,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&migrationId, "migration-id", "", "The ID of the migration to get the status of.")

	migrationStatusCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&optionalFlagPlaceholder, "optional-flag-placeholder", "", "The path to the Kubernetes config file to use for the migration.")

	migrationStatusCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	migrationStatusCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	migrationStatusCmd.MarkFlagRequired("migration-id")

	return migrationStatusCmd
}

func preRunMigrationStatus(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrationStatus(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrationStatusOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migration status opts: %v", err)
	}

	migrationStatus := NewMigationStatusChecker(*opts)
	if err := migrationStatus.Run(); err != nil {
		return err
	}

	return nil
}

func parseMigrationStatusOpts() (*MigationStatusFlags, error) {
	return &MigationStatusFlags{
		migrationId: migrationId,
	}, nil
}
