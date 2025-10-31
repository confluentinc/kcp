package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateSchemaRegistryAPIKey creates a Schema Registry API key resource
func GenerateSchemaRegistryAPIKey(envName string, isNewEnv bool) *hclwrite.Block {
	apiKeyBlock := hclwrite.NewBlock("resource", []string{"confluent_api_key", "env-manager-schema-registry-api-key"})
	apiKeyBlock.Body().SetAttributeValue("display_name", cty.StringVal("env-manager-schema-registry-api-key"))
	apiKeyBlock.Body().SetAttributeValue("description", cty.StringVal("Schema Registry API Key that is owned by the "+envName+" environment."))
	apiKeyBlock.Body().AppendNewline()

	ownerBlock := hclwrite.NewBlock("owner", nil)
	ownerBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference("confluent_service_account.app-manager.id"))
	ownerBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference("confluent_service_account.app-manager.api_version"))
	ownerBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference("confluent_service_account.app-manager.kind"))
	apiKeyBlock.Body().AppendBlock(ownerBlock)
	apiKeyBlock.Body().AppendNewline()

	managedResourceBlock := hclwrite.NewBlock("managed_resource", nil)
	managedResourceBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference("data.confluent_schema_registry_cluster.schema_registry.id"))
	managedResourceBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference("data.confluent_schema_registry_cluster.schema_registry.api_version"))
	managedResourceBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference("data.confluent_schema_registry_cluster.schema_registry.kind"))

	environmentApiKeyBlock := hclwrite.NewBlock("environment", nil)
	envRef := GetEnvironmentReference(isNewEnv)
	environmentApiKeyBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(envRef))
	managedResourceBlock.Body().AppendBlock(environmentApiKeyBlock)
	apiKeyBlock.Body().AppendBlock(managedResourceBlock)

	return apiKeyBlock
}

// GenerateKafkaAPIKey creates a Kafka API key resource
func GenerateKafkaAPIKey(envName string, isNewEnv bool) *hclwrite.Block {
	apiKeyBlock := hclwrite.NewBlock("resource", []string{"confluent_api_key", "app-manager-kafka-api-key"})
	apiKeyBlock.Body().SetAttributeValue("display_name", cty.StringVal("app-manager-kafka-api-key"))
	apiKeyBlock.Body().SetAttributeValue("description", cty.StringVal("Kafka API Key that has been created by the "+envName+" environment."))
	apiKeyBlock.Body().AppendNewline()

	ownerBlock := hclwrite.NewBlock("owner", nil)
	ownerBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference("confluent_service_account.app-manager.id"))
	ownerBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference("confluent_service_account.app-manager.api_version"))
	ownerBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference("confluent_service_account.app-manager.kind"))
	apiKeyBlock.Body().AppendBlock(ownerBlock)
	apiKeyBlock.Body().AppendNewline()

	managedResourceBlock := hclwrite.NewBlock("managed_resource", nil)
	managedResourceBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference("confluent_kafka_cluster.cluster.id"))
	managedResourceBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference("confluent_kafka_cluster.cluster.api_version"))
	managedResourceBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference("confluent_kafka_cluster.cluster.kind"))
	managedResourceBlock.Body().AppendNewline()

	environmentApiKeyBlock := hclwrite.NewBlock("environment", nil)
	envRef := GetEnvironmentReference(isNewEnv)
	environmentApiKeyBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(envRef))
	managedResourceBlock.Body().AppendBlock(environmentApiKeyBlock)
	apiKeyBlock.Body().AppendBlock(managedResourceBlock)
	apiKeyBlock.Body().AppendNewline()

	apiKeyBlock.Body().SetAttributeValue("disable_wait_for_ready", cty.BoolVal(true))

	apiKeyBlock.Body().SetAttributeRaw("depends_on", utils.TokensForList([]string{
		"confluent_role_binding.app-manager-kafka-cluster-admin",
	}))

	return apiKeyBlock
}
