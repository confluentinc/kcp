package migrate_schemas

import (
	"encoding/json"
	"fmt"
	"os"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/confluentinc/kcp/internal/services/schema_registry"
	"github.com/confluentinc/kcp/internal/types"
)

var (
	schemaRegistryScanFile string 
	confluentCloudSchemaRegistryEndpoint string
	confluentCloudSchemaRegistryApiKey   string
	confluentCloudSchemaRegistryApiSecret string
)

func NewCreateAssetSchemaMigrationCmd() *cobra.Command {
	schemaMigrationCmd := &cobra.Command{
			Use:           "migrate-schemas",
			Short:         "Migrate schemas to Confluent Cloud",
			Long:          "Register schemas from scan results to Confluent Cloud Schema Registry",
			SilenceErrors: true,
			Args:          cobra.NoArgs,
			RunE:          runSchemaMigration,
	}

	groups := map[*pflag.FlagSet]string{}

	// Scan Files flags
	schemaFileFlags := pflag.NewFlagSet("scan-files", pflag.ExitOnError)
	schemaFileFlags.SortFlags = false
	schemaFileFlags.StringVar(&schemaRegistryScanFile, "schema-registry-scan-file", "", "Schema registry scan JSON file from 'kcp scan schema-registry'")
	schemaMigrationCmd.Flags().AddFlagSet(schemaFileFlags)
	groups[schemaFileFlags] = "Scan Files Flags"

	// Confluent Cloud flags
	confluentCloudFlags := pflag.NewFlagSet("confluent-cloud", pflag.ExitOnError)
	confluentCloudFlags.SortFlags = false
	confluentCloudFlags.StringVar(&confluentCloudSchemaRegistryEndpoint, "cc-schema-registry-endpoint", "", "Confluent Cloud Schema Registry endpoint (e.g., https://psrc-xxxxx.us-east-2.aws.confluent.cloud)")
	confluentCloudFlags.StringVar(&confluentCloudSchemaRegistryApiKey, "cc-schema-registry-api-key", "", "Confluent Cloud Schema Registry API key")
	confluentCloudFlags.StringVar(&confluentCloudSchemaRegistryApiSecret, "cc-schema-registry-api-secret", "", "Confluent Cloud Schema Registry API secret")
	schemaMigrationCmd.Flags().AddFlagSet(confluentCloudFlags)
	groups[confluentCloudFlags] = "Confluent Cloud Flags"

	schemaMigrationCmd.MarkFlagRequired("schema-registry-scan-file")
	schemaMigrationCmd.MarkFlagRequired("cc-schema-registry-endpoint")
	schemaMigrationCmd.MarkFlagRequired("cc-schema-registry-api-key")
	schemaMigrationCmd.MarkFlagRequired("cc-schema-registry-api-secret")

	return schemaMigrationCmd
}

func runSchemaMigration(cmd *cobra.Command, args []string) error {
	if confluentCloudSchemaRegistryApiKey == "" {
		confluentCloudSchemaRegistryApiKey = os.Getenv("CONFLUENT_CLOUD_API_KEY")
	}
	if confluentCloudSchemaRegistryApiSecret == "" {
		confluentCloudSchemaRegistryApiSecret = os.Getenv("CONFLUENT_CLOUD_API_SECRET")
	}
	if confluentCloudSchemaRegistryEndpoint == "" {
		confluentCloudSchemaRegistryEndpoint = os.Getenv("CONFLUENT_CLOUD_SCHEMA_REGISTRY_ENDPOINT")
	}

	scanData, err := os.ReadFile(schemaRegistryScanFile)
	if err != nil {
		return fmt.Errorf("failed to read scan file: %v", err)
	}

	var scanResult types.SchemaRegistryScanResult
	if err := json.Unmarshal(scanData, &scanResult); err != nil {
		return fmt.Errorf("failed to parse scan file: %v", err)
	}

	service := schema_registry.NewSchemaRegistryService(nil)
	service.ConfigureConfluentCloud(confluentCloudSchemaRegistryEndpoint, confluentCloudSchemaRegistryApiKey, confluentCloudSchemaRegistryApiSecret)

	fmt.Printf("🚀 Starting schema migration for %d subjects...\n", len(scanResult.Subjects))
	
	successCount := 0
	errorCount := 0

	for _, subject := range scanResult.Subjects {
		fmt.Printf("📝 Registering schema for subject: %s\n", subject.Name)
		
		response, err := service.RegisterSchema(
			subject.Name,
			subject.Latest.Schema,
			subject.Latest.SchemaType,
		)
		
		if err != nil {
			fmt.Printf("❌ Failed to register %s: %v\n", subject.Name, err)
			errorCount++
		} else {
			fmt.Printf("✅ Successfully registered %s with ID: %d\n", subject.Name, response.ID)
			successCount++
		}
	}

	fmt.Printf("\n📊 Migration Summary:\n")
	fmt.Printf("  ✅ Successful: %d\n", successCount)
	fmt.Printf("  ❌ Failed: %d\n", errorCount)
	fmt.Printf("  📋 Total: %d\n", len(scanResult.Subjects))

	if errorCount > 0 {
		return fmt.Errorf("migration completed with %d errors", errorCount)
	}

	return nil
}