package migrate_schemas

import (
	"fmt"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile        string
	url              string
	glueRegistryName string
	glueRegion       string
	ccSRRestEndpoint string
	outputDir        string
	schemasFilter    string
)

func NewMigrateSchemasCmd() *cobra.Command {
	migrateSchemasCmd := &cobra.Command{
		Use:   "migrate-schemas",
		Short: "Create assets for the migrate schemas",
		Long:  "Create assets to enable the migration of schemas to Confluent Cloud.\nSupports both Confluent Schema Registry (--url) and AWS Glue Schema Registry (--glue-registry) sources.",
		Example: `  # From a Confluent Schema Registry (uses schema exporter resources)
  kcp create-asset migrate-schemas \
      --state-file kcp-state.json \
      --url https://my-schema-registry.example.com \
      --cc-sr-rest-endpoint https://psrc-xxxxx.us-east-2.aws.confluent.cloud

  # From an AWS Glue Schema Registry (generates confluent_schema resources)
  kcp create-asset migrate-schemas \
      --state-file kcp-state.json \
      --glue-registry my-glue-registry \
      --region us-east-1 \
      --cc-sr-rest-endpoint https://psrc-xxxxx.us-east-2.aws.confluent.cloud`,
		SilenceErrors: true,
		PreRunE:       preRunMigrateSchemas,
		RunE:          runMigrateSchemas,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&ccSRRestEndpoint, "cc-sr-rest-endpoint", "", "The REST endpoint of the Confluent Cloud target schema registry.")
	migrateSchemasCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Source flags (one of these is required).
	sourceFlags := pflag.NewFlagSet("source", pflag.ExitOnError)
	sourceFlags.SortFlags = false
	sourceFlags.StringVar(&url, "url", "", "The URL of a Confluent Schema Registry to migrate schemas from (uses schema exporter).")
	sourceFlags.StringVar(&glueRegistryName, "glue-registry", "", "The name of an AWS Glue Schema Registry to migrate schemas from (uses confluent_schema resources).")
	sourceFlags.StringVar(&glueRegion, "region", "", "The AWS region of the Glue Schema Registry (required when the same registry name exists in multiple regions).")
	migrateSchemasCmd.Flags().AddFlagSet(sourceFlags)
	groups[sourceFlags] = "Source Flags (one required)"

	// Optional flags.
	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&outputDir, "output-dir", "migrate_schemas", "The output directory for the generated assets.")
	optionalFlags.StringVar(&schemasFilter, "schemas", "", "Comma-separated list of schema names to migrate (default: all schemas). Only applies with --glue-registry.")
	migrateSchemasCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	migrateSchemasCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, sourceFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Source Flags (one required)", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	_ = migrateSchemasCmd.MarkFlagRequired("state-file")
	_ = migrateSchemasCmd.MarkFlagRequired("cc-sr-rest-endpoint")

	return migrateSchemasCmd
}

func preRunMigrateSchemas(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	if url == "" && glueRegistryName == "" {
		return fmt.Errorf("one of --url or --glue-registry is required")
	}
	if url != "" && glueRegistryName != "" {
		return fmt.Errorf("--url and --glue-registry are mutually exclusive")
	}

	return nil
}

func runMigrateSchemas(cmd *cobra.Command, args []string) error {
	if glueRegistryName != "" {
		return runMigrateGlueSchemas()
	}

	return runMigrateConfluentSchemas()
}

func runMigrateConfluentSchemas() error {
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

func runMigrateGlueSchemas() error {
	opts, err := parseMigrateGlueSchemasOpts()
	if err != nil {
		return fmt.Errorf("failed to parse glue schema migration opts: %v", err)
	}

	generator := NewMigrateGlueSchemasAssetGenerator(*opts)
	if err := generator.Run(); err != nil {
		return fmt.Errorf("failed to create glue schema migration assets: %v", err)
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
	if state.SchemaRegistries != nil {
		for _, sr := range state.SchemaRegistries.ConfluentSchemaRegistry {
			if sr.URL == url {
				schemaRegistry = sr
				found = true
				break
			}
		}
	}

	if !found {
		return nil, fmt.Errorf("schema registry with URL %q not found in state file", url)
	}

	opts := MigrateSchemasOpts{
		SchemaRegistry:   schemaRegistry,
		CCSRRestEndpoint: ccSRRestEndpoint,
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

func parseMigrateGlueSchemasOpts() (*MigrateGlueSchemasOpts, error) {
	state, err := types.NewStateFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing state file: %v", err)
	}

	var glueRegistry types.GlueSchemaRegistryInformation
	found := false
	var matches []string
	if state.SchemaRegistries != nil {
		for _, gr := range state.SchemaRegistries.AWSGlue {
			if gr.RegistryName == glueRegistryName {
				if glueRegion != "" && gr.Region != glueRegion {
					continue
				}
				matches = append(matches, gr.Region)
				if !found {
					glueRegistry = gr
					found = true
				}
			}
		}
	}

	if !found {
		if glueRegion != "" {
			return nil, fmt.Errorf("glue schema registry %q in region %q not found in state file", glueRegistryName, glueRegion)
		}
		return nil, fmt.Errorf("glue schema registry %q not found in state file", glueRegistryName)
	}

	if len(matches) > 1 {
		return nil, fmt.Errorf("glue schema registry %q exists in multiple regions %v; specify --region to disambiguate", glueRegistryName, matches)
	}

	// Filter schemas if --schemas flag is provided
	if schemasFilter != "" {
		filterNames := make(map[string]bool)
		for _, name := range strings.Split(schemasFilter, ",") {
			trimmed := strings.TrimSpace(name)
			if trimmed != "" {
				filterNames[trimmed] = true
			}
		}

		var filtered []types.GlueSchema
		for _, s := range glueRegistry.Schemas {
			if filterNames[s.SchemaName] {
				filtered = append(filtered, s)
			}
		}

		if len(filtered) == 0 {
			return nil, fmt.Errorf("none of the specified schemas found in registry %q", glueRegistryName)
		}
		glueRegistry.Schemas = filtered
	}

	return &MigrateGlueSchemasOpts{
		GlueRegistry:     glueRegistry,
		CCSRRestEndpoint: ccSRRestEndpoint,
		OutputDir:        outputDir,
	}, nil
}
