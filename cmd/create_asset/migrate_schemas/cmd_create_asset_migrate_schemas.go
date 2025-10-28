package migrate_schemas

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile string
	url       string
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
	requiredFlags.StringVar(&url, "url", "", "The URL of the schema registry to migrate schemas from.")
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
	migrateSchemasCmd.MarkFlagRequired("url")

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
	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	var schemaRegistry types.SchemaRegistryInformation
	found := false
	for _, sr := range state.SchemaRegistries {
		if sr.URL == url {
			schemaRegistry = sr
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("schema registry with URL %q not found in state file", url)
	}

	opts := MigrateSchemasOpts{
		SchemaRegistry: schemaRegistry,
		// Default exporter for CLI - this may be configurable in the future
		Exporters: []SchemaExporter{
			{
				Name:        "kcp-schemas-to-cc-exporter",
				ContextType: "NONE",
				Subjects:    []string{":*:"},
			},
		},
	}

	return &opts, nil
}
