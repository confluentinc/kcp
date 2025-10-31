package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateSchemaRegistryAPIKey creates a Schema Registry API key resource
func GenerateSchemaRegistryAPIKey(tfResourceName, envName, serviceAccountIdRef, serviceAccountApiVersionRef, serviceAccountKindRef, schemaRegistryIdRef, schemaRegistryApiVersionRef, schemaRegistryKindRef, environmentIdRef string) *hclwrite.Block {
	apiKeyBlock := hclwrite.NewBlock("resource", []string{"confluent_api_key", tfResourceName})
	apiKeyBlock.Body().SetAttributeValue("display_name", cty.StringVal("env-manager-schema-registry-api-key"))
	apiKeyBlock.Body().SetAttributeValue("description", cty.StringVal("Schema Registry API Key that is owned by the "+envName+" environment."))
	apiKeyBlock.Body().AppendNewline()

	ownerBlock := hclwrite.NewBlock("owner", nil)
	ownerBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(serviceAccountIdRef))
	ownerBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference(serviceAccountApiVersionRef))
	ownerBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference(serviceAccountKindRef))
	apiKeyBlock.Body().AppendBlock(ownerBlock)
	apiKeyBlock.Body().AppendNewline()

	managedResourceBlock := hclwrite.NewBlock("managed_resource", nil)
	managedResourceBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(schemaRegistryIdRef))
	managedResourceBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference(schemaRegistryApiVersionRef))
	managedResourceBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference(schemaRegistryKindRef))

	environmentApiKeyBlock := hclwrite.NewBlock("environment", nil)
	environmentApiKeyBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(environmentIdRef))
	managedResourceBlock.Body().AppendBlock(environmentApiKeyBlock)
	apiKeyBlock.Body().AppendBlock(managedResourceBlock)

	return apiKeyBlock
}

// GenerateKafkaAPIKey creates a Kafka API key resource
func GenerateKafkaAPIKey(tfResourceName, envName, serviceAccountIdRef, serviceAccountApiVersionRef, serviceAccountKindRef, clusterIdRef, clusterApiVersionRef, clusterKindRef, environmentIdRef, dependsOnRoleBindingRef string) *hclwrite.Block {
	apiKeyBlock := hclwrite.NewBlock("resource", []string{"confluent_api_key", tfResourceName})
	apiKeyBlock.Body().SetAttributeValue("display_name", cty.StringVal("app-manager-kafka-api-key"))
	apiKeyBlock.Body().SetAttributeValue("description", cty.StringVal("Kafka API Key that has been created by the "+envName+" environment."))
	apiKeyBlock.Body().AppendNewline()

	ownerBlock := hclwrite.NewBlock("owner", nil)
	ownerBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(serviceAccountIdRef))
	ownerBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference(serviceAccountApiVersionRef))
	ownerBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference(serviceAccountKindRef))
	apiKeyBlock.Body().AppendBlock(ownerBlock)
	apiKeyBlock.Body().AppendNewline()

	managedResourceBlock := hclwrite.NewBlock("managed_resource", nil)
	managedResourceBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(clusterIdRef))
	managedResourceBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference(clusterApiVersionRef))
	managedResourceBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference(clusterKindRef))
	managedResourceBlock.Body().AppendNewline()

	environmentApiKeyBlock := hclwrite.NewBlock("environment", nil)
	environmentApiKeyBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(environmentIdRef))
	managedResourceBlock.Body().AppendBlock(environmentApiKeyBlock)
	apiKeyBlock.Body().AppendBlock(managedResourceBlock)
	apiKeyBlock.Body().AppendNewline()

	apiKeyBlock.Body().SetAttributeValue("disable_wait_for_ready", cty.BoolVal(true))

	apiKeyBlock.Body().SetAttributeRaw("depends_on", utils.TokensForList([]string{
		dependsOnRoleBindingRef,
	}))

	return apiKeyBlock
}
