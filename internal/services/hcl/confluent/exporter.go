package confluent

import (
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

const (
	VarSourceSchemaRegistryID             = "source_schema_registry_id"
	VarSourceSchemaRegistryURL            = "source_schema_registry_url"
	VarSourceSchemaRegistryUsername       = "source_schema_registry_username"
	VarSourceSchemaRegistryPassword       = "source_schema_registry_password"
	VarConfluentCloudSchemaRegistryURL    = "confluent_cloud_schema_registry_url"
	VarConfluentCloudSchemaRegistryAPIKey = "confluent_cloud_schema_registry_api_key"
	VarConfluentCloudSchemaRegistrySecret = "confluent_cloud_schema_registry_api_secret"
	VarSubjects                           = "subjects"
)

// SchemaExporterVariables defines all the variables needed for schema exporter resources
var SchemaExporterVariables = []types.TerraformVariable{
	{Name: VarSourceSchemaRegistryID, Description: "ID of the source schema registry", Sensitive: false, Type: "string"},
	{Name: VarSourceSchemaRegistryURL, Description: "URL of the source schema registry", Sensitive: false, Type: "string"},
	{Name: VarSourceSchemaRegistryUsername, Description: "Username for source schema registry authentication", Sensitive: false, Type: "string"},
	{Name: VarSourceSchemaRegistryPassword, Description: "Password for source schema registry authentication", Sensitive: true, Type: "string"},
	{Name: VarConfluentCloudSchemaRegistryURL, Description: "URL of the target schema registry (Confluent Cloud)", Sensitive: false, Type: "string"},
	{Name: VarConfluentCloudSchemaRegistryAPIKey, Description: "API key for the target schema registry (Confluent Cloud)", Sensitive: false, Type: "string"},
	{Name: VarConfluentCloudSchemaRegistrySecret, Description: "API secret for the target schema registry (Confluent Cloud)", Sensitive: true, Type: "string"},
	{Name: VarSubjects, Description: "List of subjects to export", Sensitive: false, Type: "list(string)"},
}

func GenerateSchemaExporter(schemaRegistry types.SchemaRegistryExporterConfig) *hclwrite.Block {
	resourceName := "kcp_schema_exporter"
	exporterBlock := hclwrite.NewBlock("resource", []string{"confluent_schema_exporter", resourceName})

	// Set name attribute
	exporterBlock.Body().SetAttributeValue("name", cty.StringVal(resourceName))
	exporterBlock.Body().AppendNewline()

	// schema_registry_cluster block
	schemaRegistryClusterBlock := hclwrite.NewBlock("schema_registry_cluster", nil)
	schemaRegistryClusterBlock.Body().SetAttributeRaw("id", utils.TokensForVarReference(VarSourceSchemaRegistryID))
	exporterBlock.Body().AppendBlock(schemaRegistryClusterBlock)
	exporterBlock.Body().AppendNewline()

	// rest_endpoint attribute
	exporterBlock.Body().SetAttributeRaw("rest_endpoint", utils.TokensForVarReference(VarSourceSchemaRegistryURL))

	// credentials block for source
	credentialsBlock := hclwrite.NewBlock("credentials", nil)
	credentialsBlock.Body().SetAttributeRaw("key", utils.TokensForVarReference(VarSourceSchemaRegistryUsername))
	credentialsBlock.Body().SetAttributeRaw("secret", utils.TokensForVarReference(VarSourceSchemaRegistryPassword))
	exporterBlock.Body().AppendBlock(credentialsBlock)
	exporterBlock.Body().AppendNewline()

	// subjects attribute
	exporterBlock.Body().SetAttributeRaw("subjects", utils.TokensForVarReference(VarSubjects))

	// context_type attribute
	exporterBlock.Body().SetAttributeValue("context_type", cty.StringVal("NONE"))
	exporterBlock.Body().AppendNewline()

	// destination_schema_registry_cluster block
	destinationBlock := hclwrite.NewBlock("destination_schema_registry_cluster", nil)
	destinationBlock.Body().SetAttributeRaw("rest_endpoint", utils.TokensForVarReference(VarConfluentCloudSchemaRegistryURL))
	destinationBlock.Body().AppendNewline()

	// credentials block for destination
	destinationCredentialsBlock := hclwrite.NewBlock("credentials", nil)
	destinationCredentialsBlock.Body().SetAttributeRaw("key", utils.TokensForVarReference(VarConfluentCloudSchemaRegistryAPIKey))
	destinationCredentialsBlock.Body().SetAttributeRaw("secret", utils.TokensForVarReference(VarConfluentCloudSchemaRegistrySecret))
	destinationBlock.Body().AppendBlock(destinationCredentialsBlock)

	exporterBlock.Body().AppendBlock(destinationBlock)

	return exporterBlock
}
