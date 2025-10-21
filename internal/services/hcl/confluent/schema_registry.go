package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// GenerateSchemaRegistryDataSource creates a data source for the Schema Registry cluster
func generateSchemaRegistryDataSource(isNewEnv bool) *hclwrite.Block {
	schemaRegistryDataBlock := hclwrite.NewBlock("data", []string{"confluent_schema_registry_cluster", "schema_registry"})

	environmentSRBlock := hclwrite.NewBlock("environment", nil)
	envRef := getEnvironmentReference(isNewEnv)
	environmentSRBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(envRef))

	schemaRegistryDataBlock.Body().AppendBlock(environmentSRBlock)
	schemaRegistryDataBlock.Body().AppendNewline()

	schemaRegistryDataBlock.Body().SetAttributeRaw("depends_on", utils.TokensForList([]string{
		"confluent_api_key.app-manager-kafka-api-key",
	}))

	return schemaRegistryDataBlock
}
