package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// GenerateSchemaRegistryDataSource creates a data source for the Schema Registry cluster
func GenerateSchemaRegistryDataSource(tfResourceName, environmentIdRef, dependsOnApiKeyRef string) *hclwrite.Block {
	schemaRegistryDataBlock := hclwrite.NewBlock("data", []string{"confluent_schema_registry_cluster", tfResourceName})

	environmentSRBlock := hclwrite.NewBlock("environment", nil)
	environmentSRBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(environmentIdRef))

	schemaRegistryDataBlock.Body().AppendBlock(environmentSRBlock)
	schemaRegistryDataBlock.Body().AppendNewline()

	schemaRegistryDataBlock.Body().SetAttributeRaw("depends_on", utils.TokensForList([]string{
		dependsOnApiKeyRef,
	}))

	return schemaRegistryDataBlock
}
