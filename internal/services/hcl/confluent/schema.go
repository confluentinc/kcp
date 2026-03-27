package confluent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// safePathName sanitizes a name for use in file paths, preventing path traversal
var unsafePathCharsRegex = regexp.MustCompile(`[^a-zA-Z0-9_\-.]`)

func safePathName(name string) string {
	return unsafePathCharsRegex.ReplaceAllString(name, "_")
}

// GlueSchemaVariables defines the variables needed for Glue schema migration resources
var GlueSchemaVariables = []types.TerraformVariable{
	{Name: VarConfluentCloudSchemaRegistryURL, Description: "REST endpoint of the target Confluent Cloud Schema Registry", Sensitive: false, Type: "string"},
	{Name: VarConfluentCloudSchemaRegistryAPIKey, Description: "API key for the target Confluent Cloud Schema Registry", Sensitive: false, Type: "string"},
	{Name: VarConfluentCloudSchemaRegistrySecret, Description: "API secret for the target Confluent Cloud Schema Registry", Sensitive: true, Type: "string"},
	{Name: "schema_registry_cluster_id", Description: "ID of the Confluent Cloud Schema Registry cluster", Sensitive: false, Type: "string"},
}

// schemaFormatExtension maps Glue data format strings to file extensions
func schemaFormatExtension(dataFormat string) string {
	switch strings.ToUpper(dataFormat) {
	case "AVRO":
		return ".avsc"
	case "PROTOBUF":
		return ".proto"
	default:
		return ".json"
	}
}

// schemaFormatToTerraform maps Glue data format strings to Terraform format values
func schemaFormatToTerraform(dataFormat string) string {
	switch strings.ToUpper(dataFormat) {
	case "AVRO":
		return "AVRO"
	case "PROTOBUF":
		return "PROTOBUF"
	default:
		return "JSON"
	}
}

// GenerateGlueSchemaMigrationHCL generates per-schema .tf files and schema definition files
// for migrating Glue schemas to Confluent Cloud Schema Registry.
// Returns a map of filename → content containing both .tf files (e.g., "test.tf") and
// schema definition files (e.g., "schemas/test/v1.avsc").
func GenerateGlueSchemaMigrationHCL(schemas []types.GlueSchema) (map[string]string, error) {
	files := make(map[string]string)

	for _, schema := range schemas {
		f := hclwrite.NewEmptyFile()
		rootBody := f.Body()

		resourcePrefix := utils.FormatHclResourceName(schema.SchemaName)
		format := schemaFormatToTerraform(schema.DataFormat)

		// Sort versions by version number ascending
		versions := make([]types.GlueSchemaVersion, len(schema.Versions))
		copy(versions, schema.Versions)
		sort.Slice(versions, func(a, b int) bool {
			return versions[a].VersionNumber < versions[b].VersionNumber
		})

		// Comment header for this schema
		rootBody.AppendUnstructuredTokens(utils.TokensForComment(
			fmt.Sprintf("# --- Schema: %s (%d version(s)) ---\n", schema.SchemaName, len(versions)),
		))

		// Set compatibility to NONE before registering versions
		compatNoneResourceName := resourcePrefix + "_compat_none"
		rootBody.AppendBlock(generateSubjectConfig(compatNoneResourceName, schema.SchemaName, "NONE"))
		rootBody.AppendNewline()

		// Generate a confluent_schema resource for each version
		prevResourceRef := fmt.Sprintf("confluent_subject_config.%s", compatNoneResourceName)
		safeName := safePathName(schema.SchemaName)
		for _, version := range versions {
			versionResourceName := fmt.Sprintf("%s_v%d", resourcePrefix, version.VersionNumber)
			ext := schemaFormatExtension(schema.DataFormat)
			schemaFilePath := fmt.Sprintf("./schemas/%s/v%d%s", safeName, version.VersionNumber, ext)

			block, err := generateConfluentSchema(versionResourceName, schema.SchemaName, format, schemaFilePath, prevResourceRef)
			if err != nil {
				return nil, err
			}
			rootBody.AppendBlock(block)
			rootBody.AppendNewline()

			// Write schema definition file
			fileKey := fmt.Sprintf("schemas/%s/v%d%s", safeName, version.VersionNumber, ext)
			files[fileKey] = version.SchemaDefinition

			prevResourceRef = fmt.Sprintf("confluent_schema.%s", versionResourceName)
		}

		// Write per-schema .tf file
		tfFileName := fmt.Sprintf("%s.tf", resourcePrefix)
		files[tfFileName] = string(f.Bytes())
	}

	return files, nil
}

// generateConfluentSchema creates a confluent_schema resource block
func generateConfluentSchema(resourceName, subjectName, format, schemaFilePath, dependsOn string) (*hclwrite.Block, error) {
	block := hclwrite.NewBlock("resource", []string{"confluent_schema", resourceName})
	body := block.Body()

	body.SetAttributeValue("subject_name", cty.StringVal(subjectName))
	body.SetAttributeValue("format", cty.StringVal(format))
	body.SetAttributeRaw("schema", utils.TokensForFunctionCall("file",
		hclwrite.TokensForValue(cty.StringVal(schemaFilePath)),
	))
	body.SetAttributeValue("recreate_on_update", cty.BoolVal(true))
	body.AppendNewline()

	// Schema registry cluster
	srClusterBlock := hclwrite.NewBlock("schema_registry_cluster", nil)
	srClusterBlock.Body().SetAttributeRaw("id", utils.TokensForVarReference("schema_registry_cluster_id"))
	body.AppendBlock(srClusterBlock)
	body.AppendNewline()

	// REST endpoint
	body.SetAttributeRaw("rest_endpoint", utils.TokensForVarReference(VarConfluentCloudSchemaRegistryURL))

	// Credentials
	credentialsBlock := hclwrite.NewBlock("credentials", nil)
	credentialsBlock.Body().SetAttributeRaw("key", utils.TokensForVarReference(VarConfluentCloudSchemaRegistryAPIKey))
	credentialsBlock.Body().SetAttributeRaw("secret", utils.TokensForVarReference(VarConfluentCloudSchemaRegistrySecret))
	body.AppendBlock(credentialsBlock)
	body.AppendNewline()

	// depends_on
	body.SetAttributeRaw("depends_on", utils.TokensForList([]string{dependsOn}))

	// lifecycle { prevent_destroy = true }
	if err := utils.GenerateLifecycleBlock(block, "prevent_destroy", true); err != nil {
		return nil, fmt.Errorf("failed to generate lifecycle block for %s: %w", resourceName, err)
	}

	return block, nil
}

// generateSubjectConfig creates a confluent_subject_config resource block
func generateSubjectConfig(resourceName, subjectName, compatibilityLevel string) *hclwrite.Block {
	block := hclwrite.NewBlock("resource", []string{"confluent_subject_config", resourceName})
	body := block.Body()

	body.SetAttributeValue("subject_name", cty.StringVal(subjectName))
	body.SetAttributeValue("compatibility_level", cty.StringVal(compatibilityLevel))
	body.AppendNewline()

	// Schema registry cluster
	srClusterBlock := hclwrite.NewBlock("schema_registry_cluster", nil)
	srClusterBlock.Body().SetAttributeRaw("id", utils.TokensForVarReference("schema_registry_cluster_id"))
	body.AppendBlock(srClusterBlock)
	body.AppendNewline()

	// REST endpoint
	body.SetAttributeRaw("rest_endpoint", utils.TokensForVarReference(VarConfluentCloudSchemaRegistryURL))

	// Credentials
	credentialsBlock := hclwrite.NewBlock("credentials", nil)
	credentialsBlock.Body().SetAttributeRaw("key", utils.TokensForVarReference(VarConfluentCloudSchemaRegistryAPIKey))
	credentialsBlock.Body().SetAttributeRaw("secret", utils.TokensForVarReference(VarConfluentCloudSchemaRegistrySecret))
	body.AppendBlock(credentialsBlock)

	return block
}
