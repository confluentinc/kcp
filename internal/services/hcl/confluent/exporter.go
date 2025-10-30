package confluent

import (
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclsyntax"
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
)

// SchemaExporterVariable defines a Terraform variable for schema exporters
type SchemaExporterVariable struct {
	Name        string
	Description string
	Sensitive   bool
}

// SchemaExporterVariables defines all the variables needed for schema exporter resources
var SchemaExporterVariables = []SchemaExporterVariable{
	{VarSourceSchemaRegistryID, "ID of the source schema registry", false},
	{VarSourceSchemaRegistryURL, "URL of the source schema registry", false},
	{VarSourceSchemaRegistryUsername, "Username for source schema registry authentication", false},
	{VarSourceSchemaRegistryPassword, "Password for source schema registry authentication", true},
	{VarConfluentCloudSchemaRegistryURL, "URL of the target schema registry (Confluent Cloud)", false},
	{VarConfluentCloudSchemaRegistryAPIKey, "API key for the target schema registry (Confluent Cloud)", false},
	{VarConfluentCloudSchemaRegistrySecret, "API secret for the target schema registry (Confluent Cloud)", true},
}

// GenerateSchemaExporter creates a Terraform resource for a single confluent_schema_exporter
func GenerateSchemaExporter(exporter types.Exporter) *hclwrite.Block {
	resourceName := utils.FormatHclResourceName(exporter.Name)
	exporterBlock := hclwrite.NewBlock("resource", []string{"confluent_schema_exporter", resourceName})

	// Set name attribute
	exporterBlock.Body().SetAttributeValue("name", cty.StringVal(exporter.Name))
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
	subjectsTokens := hclwrite.Tokens{}
	subjectsTokens = append(subjectsTokens, &hclwrite.Token{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")})
	for i, subject := range exporter.Subjects {
		subjectsTokens = append(subjectsTokens, &hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)})
		subjectsTokens = append(subjectsTokens, &hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte(subject)})
		subjectsTokens = append(subjectsTokens, &hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)})
		if i < len(exporter.Subjects)-1 {
			subjectsTokens = append(subjectsTokens, &hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(", ")})
		}
	}
	subjectsTokens = append(subjectsTokens, &hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")})
	exporterBlock.Body().SetAttributeRaw("subjects", subjectsTokens)

	// context_type and context attributes
	exporterBlock.Body().SetAttributeValue("context_type", cty.StringVal(exporter.ContextType))
	exporterBlock.Body().SetAttributeValue("context", cty.StringVal(exporter.ContextName))
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
