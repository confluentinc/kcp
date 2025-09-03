package schema_registry

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/schema_registry"
	sr "github.com/confluentinc/kcp/internal/generators/scan/schema_registry"
)

var (
	schemaRegistryURL string
)

func NewScanSchemaRegistryCmd() *cobra.Command {
	schemaRegistryCmd := &cobra.Command{
		Use:           "schema-registry",
		Short:         "Scan Schema Registry and export all schemas",
		Long:          "Scan Schema Registry to discover all schemas, subjects, and versions, then export to JSON",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          runScanSchemaRegistry,
	}

	schemaRegistryCmd.Flags().StringVar(&schemaRegistryURL, "schema-registry-url", "http://localhost:8081", "Schema Registry URL")
	
	return schemaRegistryCmd
}
// }

func runScanSchemaRegistry(cmd *cobra.Command, args []string) error {
	client, err := client.NewSchemaRegistryClient(client.SchemaRegistryConfig{URL: schemaRegistryURL})
	if err != nil {
		return fmt.Errorf("❌ Failed to create schema registry client: %v", err)
	}

	service := schema_registry.NewSchemaRegistryService(client)

	scanner := sr.NewSchemaRegistryScanner(service, schemaRegistryURL)

	return scanner.Run()
}
