package schema_registry

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/schema_registry"
)

var (
	schemaRegistryURL string
)

func NewScanSchemaRegistryCmd() *cobra.Command {
	schemaRegistryCmd := &cobra.Command{
		Use:           "schema-registry",
		Short:         "Discover schemas from Schema Registry",
		Long:          "Discover schemas, subjects, and their versions from Schema Registry",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	schemaRegistryCmd.PersistentFlags().StringVar(&schemaRegistryURL, "schema-registry-url", "http://localhost:8081", "Schema Registry URL")
	
	// Add subcommands
	schemaRegistryCmd.AddCommand(
		newListSubjectsCmd(),
		newGetVersionsCmd(),
		newGetLatestSchemaCmd(),
	)

	return schemaRegistryCmd
}
// }

func createService() *schema_registry.SchemaRegistryService {
	config := client.SchemaRegistryConfig{URL: schemaRegistryURL}
	client, _ := client.NewSchemaRegistryClient(config)
	return schema_registry.NewSchemaRegistryService(client)
}

func newListSubjectsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-subjects",
		Short: "List all subjects in Schema Registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			service := createService()
			subjects, err := service.ListSubjects()
			if err != nil {
				return err
			}
			
			fmt.Printf("Found %d subjects:\n", len(subjects))
			for _, subject := range subjects {
				fmt.Printf("  ✅ %s\n", subject)
			}
			return nil
		},
	}
}

func newGetVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get-versions <subject>",
		Short: "Get all versions for a subject",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := createService()
			versions, err := service.GetAllSubjectVersions(args[0])
			if err != nil {
				return err
			}
			
			fmt.Printf("Versions for %s: %v\n", args[0], versions)
			return nil
		},
	}
}

func newGetLatestSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get-schema <subject>",
		Short: "Get the latest schema for a subject",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := createService()
			schema, err := service.GetLatestSchema(args[0])
			if err != nil {
				return err
			}
			
			fmt.Printf("Latest schema for %s:\n", args[0])
			fmt.Printf("  Version: %d\n", schema.Version)
			fmt.Printf("  ID: %d\n", schema.ID)
			fmt.Printf("  Schema: %s\n", schema.Schema)
			return nil
		},
	}
}

