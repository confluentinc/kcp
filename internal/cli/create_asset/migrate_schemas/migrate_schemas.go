package migrate_schemas

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	schemaRegistryScanFile string
	outputDir string 
)

func NewCreateAssetSchemaMigrationCmd() *cobra.Command {
	schemaMigrationCmd := &cobra.Command{
			Use:           "migrate-schemas",
			Short:         "Migrate schemas to Confluent Cloud",
			Long:			     "Migrate schemas to executable Terraform assets for Confluent Cloud.",
			SilenceErrors: true,
			Args:          cobra.NoArgs,
			RunE:          runSchemaMigration,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("scan-files", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&schemaRegistryScanFile, "schema-registry-scan-file", "", "Schema registry scan JSON file from 'kcp scan schema-registry'")
	schemaMigrationCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Scan Files Flags"

	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.StringVar(&outputDir, "output-dir", "", "The directory where the Terraform schema assets will be written to")
	schemaMigrationCmd.Flags().AddFlagSet(optionalFlags)

	schemaMigrationCmd.MarkFlagRequired("schema-registry-scan-file")

	return schemaMigrationCmd
}

func runSchemaMigration(cmd *cobra.Command, args []string) error {
    scanData, err := os.ReadFile(schemaRegistryScanFile)
    if err != nil {
        return fmt.Errorf("failed to read scan file: %v", err)
    }

    var scanResult types.SchemaRegistryScanResult
    if err := json.Unmarshal(scanData, &scanResult); err != nil {
        return fmt.Errorf("failed to parse scan file: %v", err)
    }

    if err := RunConvertSchemas(scanResult, outputDir); err != nil {
        return fmt.Errorf("failed to convert schemas to Terraform: %v", err)
    }

    fmt.Printf("🚀 Starting schema migration for %d subjects...\n", len(scanResult.Subjects))

    return nil
}

func RunConvertSchemas(scanResult types.SchemaRegistryScanResult, outputDir string) error {
	for _, subject := range scanResult.Subjects {
		// Create Terraform file for each schema
		terraformContent := fmt.Sprintf(`
resource "confluent_schema" "%s" {
  name = "%s"
  schema = <<EOF
%s
EOF
}
`, subject.Name, subject.Name, subject.Latest.Schema)

		// Write to file
		filePath := fmt.Sprintf("%s/%s.tf", outputDir, subject.Name)
		if err := os.WriteFile(filePath, []byte(terraformContent), 0644); err != nil {
			return fmt.Errorf("failed to write Terraform file for %s: %v", subject.Name, err)
		}
	}
	return nil
}