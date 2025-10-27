package migrate_schemas

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile            string
	migrationInfraFolder string
)

func NewMigrateSchemasCmd() *cobra.Command {
	migrateSchemasCmd := &cobra.Command{
		Use:           "migrate-schemas",
		Short:         "Create assets for the migrate schemas",
		Long:          "Create assets to enable the migration of schemas to Confluent Cloud",
		SilenceErrors: true,
		PreRunE:       preRunMigrateSchemas,
		RunE:          runMigrateSchemas,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&migrationInfraFolder, "migration-infra-folder", "", "The migration-infra folder produced from 'kcp create-asset migration-infra' command after applying the Terraform")
	migrateSchemasCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	migrateSchemasCmd.SetUsageFunc(func(c *cobra.Command) error {
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

	migrateSchemasCmd.MarkFlagRequired("state-file")
	migrateSchemasCmd.MarkFlagRequired("migration-infra-folder")

	return migrateSchemasCmd
}

func preRunMigrateSchemas(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	return nil
}

func runMigrateSchemas(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrateSchemasOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migrate schemas opts: %v", err)
	}

	migrateSchemasAssetGenerator := NewMigrateSchemasAssetGenerator(*opts)
	if err := migrateSchemasAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create migrate schemas assets: %v", err)
	}

	return nil
}

func parseMigrateSchemasOpts() (*MigrateSchemasOpts, error) {
	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster file: %v", err)
	}

	var state types.State
	if err := json.Unmarshal(file, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	return nil, nil
}
