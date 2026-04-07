package confluent

import (
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateGlueSchemaMigrationHCL_SingleSchemaMultipleVersions(t *testing.T) {
	schemas := []types.GlueSchema{
		{
			SchemaName: "UserEvent",
			DataFormat: "AVRO",
			Versions: []types.GlueSchemaVersion{
				{VersionNumber: 2, SchemaDefinition: `{"type":"record","name":"UserEvent","fields":[{"name":"id","type":"string"},{"name":"email","type":"string"}]}`},
				{VersionNumber: 1, SchemaDefinition: `{"type":"record","name":"UserEvent","fields":[{"name":"id","type":"string"}]}`},
			},
		},
	}

	files, err := GenerateGlueSchemaMigrationHCL(schemas)
	require.NoError(t, err)

	// Verify per-schema .tf file exists
	hcl, ok := files["userevent.tf"]
	require.True(t, ok, "userevent.tf should exist")

	// Verify HCL contains resources in correct order
	assert.Contains(t, hcl, `resource "confluent_subject_config" "userevent_compat_none"`)
	assert.Contains(t, hcl, `resource "confluent_schema" "userevent_v1"`)
	assert.Contains(t, hcl, `resource "confluent_schema" "userevent_v2"`)

	// Verify subject name is passed through 1:1 (hclwrite pads alignment)
	assert.Contains(t, hcl, `"UserEvent"`)

	// Verify format
	assert.Contains(t, hcl, `"AVRO"`)

	// Verify file references
	assert.Contains(t, hcl, `file("./schemas/UserEvent/v1.avsc")`)
	assert.Contains(t, hcl, `file("./schemas/UserEvent/v2.avsc")`)

	// Verify recreate_on_update
	assert.Contains(t, hcl, `recreate_on_update = true`)

	// Verify depends_on ordering: v1 depends on compat_none, v2 depends on v1
	assert.Contains(t, hcl, `depends_on = [confluent_subject_config.userevent_compat_none]`)
	assert.Contains(t, hcl, `depends_on = [confluent_schema.userevent_v1]`)

	// Verify lifecycle
	assert.Contains(t, hcl, `prevent_destroy = true`)

	// Verify compatibility is set to NONE
	assert.Contains(t, hcl, `compatibility_level = "NONE"`)

	// Verify schema definition files are generated
	assert.Contains(t, files, "schemas/UserEvent/v1.avsc")
	assert.Contains(t, files, "schemas/UserEvent/v2.avsc")

	// Verify schema content matches (v1 should have the simpler schema)
	assert.Contains(t, files["schemas/UserEvent/v1.avsc"], `"name":"UserEvent"`)
	assert.NotContains(t, files["schemas/UserEvent/v1.avsc"], `"name":"email"`)
	assert.Contains(t, files["schemas/UserEvent/v2.avsc"], `"name":"email"`)

	// Total files: 1 .tf + 2 schema definitions
	require.Len(t, files, 3)
}

func TestGenerateGlueSchemaMigrationHCL_VersionOrdering(t *testing.T) {
	// Versions provided out of order — should be sorted ascending
	schemas := []types.GlueSchema{
		{
			SchemaName: "TestSchema",
			DataFormat: "JSON",
			Versions: []types.GlueSchemaVersion{
				{VersionNumber: 3, SchemaDefinition: "v3"},
				{VersionNumber: 1, SchemaDefinition: "v1"},
				{VersionNumber: 2, SchemaDefinition: "v2"},
			},
		},
	}

	files, err := GenerateGlueSchemaMigrationHCL(schemas)
	require.NoError(t, err)

	hcl := files["testschema.tf"]

	// v1 should depend on compat_none
	assert.Contains(t, hcl, `depends_on = [confluent_subject_config.testschema_compat_none]`)
	// v2 should depend on v1
	assert.Contains(t, hcl, `depends_on = [confluent_schema.testschema_v1]`)
	// v3 should depend on v2
	assert.Contains(t, hcl, `depends_on = [confluent_schema.testschema_v2]`)

	// Verify correct file extensions for JSON
	assert.Contains(t, files, "schemas/TestSchema/v1.json")
	assert.Contains(t, files, "schemas/TestSchema/v2.json")
	assert.Contains(t, files, "schemas/TestSchema/v3.json")
}

func TestGenerateGlueSchemaMigrationHCL_MultipleSchemas(t *testing.T) {
	schemas := []types.GlueSchema{
		{
			SchemaName: "SchemaA",
			DataFormat: "AVRO",
			Versions: []types.GlueSchemaVersion{
				{VersionNumber: 1, SchemaDefinition: "schema-a-v1"},
			},
		},
		{
			SchemaName: "SchemaB",
			DataFormat: "PROTOBUF",
			Versions: []types.GlueSchemaVersion{
				{VersionNumber: 1, SchemaDefinition: "schema-b-v1"},
			},
		},
	}

	files, err := GenerateGlueSchemaMigrationHCL(schemas)
	require.NoError(t, err)

	// Verify separate .tf files for each schema
	schemaAHcl, ok := files["schemaa.tf"]
	require.True(t, ok, "schemaa.tf should exist")
	schemaBHcl, ok := files["schemab.tf"]
	require.True(t, ok, "schemab.tf should exist")

	// SchemaA resources in its own file
	assert.Contains(t, schemaAHcl, `resource "confluent_subject_config" "schemaa_compat_none"`)
	assert.Contains(t, schemaAHcl, `resource "confluent_schema" "schemaa_v1"`)
	assert.Contains(t, schemaAHcl, `"AVRO"`)

	// SchemaB resources in its own file
	assert.Contains(t, schemaBHcl, `resource "confluent_subject_config" "schemab_compat_none"`)
	assert.Contains(t, schemaBHcl, `resource "confluent_schema" "schemab_v1"`)
	assert.Contains(t, schemaBHcl, `"PROTOBUF"`)

	// SchemaA should NOT contain SchemaB resources
	assert.NotContains(t, schemaAHcl, "schemab")
	assert.NotContains(t, schemaBHcl, "schemaa")

	// Verify file extensions
	assert.Contains(t, files, "schemas/SchemaA/v1.avsc")
	assert.Contains(t, files, "schemas/SchemaB/v1.proto")

	// Total: 2 .tf files + 2 schema definitions
	require.Len(t, files, 4)
}

func TestGenerateGlueSchemaMigrationHCL_EmptySchemas(t *testing.T) {
	files, err := GenerateGlueSchemaMigrationHCL([]types.GlueSchema{})
	require.NoError(t, err)

	assert.Empty(t, files)
}

func TestGenerateGlueSchemaMigrationHCL_CredentialVariableReferences(t *testing.T) {
	schemas := []types.GlueSchema{
		{
			SchemaName: "Test",
			DataFormat: "AVRO",
			Versions: []types.GlueSchemaVersion{
				{VersionNumber: 1, SchemaDefinition: "test"},
			},
		},
	}

	files, err := GenerateGlueSchemaMigrationHCL(schemas)
	require.NoError(t, err)
	hcl := files["test.tf"]

	// Verify credential variable references appear in both resource types
	assert.True(t, strings.Count(hcl, "var.confluent_cloud_schema_registry_url") >= 2, "SR URL var should appear in both subject_config and schema resources")
	assert.True(t, strings.Count(hcl, "var.confluent_cloud_schema_registry_api_key") >= 2)
	assert.True(t, strings.Count(hcl, "var.confluent_cloud_schema_registry_api_secret") >= 2)
	assert.True(t, strings.Count(hcl, "var.schema_registry_cluster_id") >= 2)
}

func TestSchemaFormatExtension(t *testing.T) {
	assert.Equal(t, ".avsc", schemaFormatExtension("AVRO"))
	assert.Equal(t, ".avsc", schemaFormatExtension("avro"))
	assert.Equal(t, ".proto", schemaFormatExtension("PROTOBUF"))
	assert.Equal(t, ".proto", schemaFormatExtension("protobuf"))
	assert.Equal(t, ".json", schemaFormatExtension("JSON"))
	assert.Equal(t, ".json", schemaFormatExtension("json"))
	assert.Equal(t, ".json", schemaFormatExtension("unknown"))
}

func TestSchemaFormatToTerraform(t *testing.T) {
	assert.Equal(t, "AVRO", schemaFormatToTerraform("AVRO"))
	assert.Equal(t, "AVRO", schemaFormatToTerraform("avro"))
	assert.Equal(t, "PROTOBUF", schemaFormatToTerraform("PROTOBUF"))
	assert.Equal(t, "JSON", schemaFormatToTerraform("JSON"))
	assert.Equal(t, "JSON", schemaFormatToTerraform("unknown"))
}
