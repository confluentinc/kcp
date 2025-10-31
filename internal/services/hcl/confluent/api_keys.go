package confluent

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateSchemaRegistryAPIKey creates a Schema Registry API key resource
func GenerateSchemaRegistryAPIKey(tfResourceName, envName, serviceAccountName, schemaRegistryName string, isNewEnv bool) *hclwrite.Block {
	apiKeyBlock := hclwrite.NewBlock("resource", []string{"confluent_api_key", tfResourceName})
	apiKeyBlock.Body().SetAttributeValue("display_name", cty.StringVal("env-manager-schema-registry-api-key"))
	apiKeyBlock.Body().SetAttributeValue("description", cty.StringVal("Schema Registry API Key that is owned by the "+envName+" environment."))
	apiKeyBlock.Body().AppendNewline()

	ownerBlock := hclwrite.NewBlock("owner", nil)
	ownerBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(fmt.Sprintf("confluent_service_account.%s.id", serviceAccountName)))
	ownerBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference(fmt.Sprintf("confluent_service_account.%s.api_version", serviceAccountName)))
	ownerBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference(fmt.Sprintf("confluent_service_account.%s.kind", serviceAccountName)))
	apiKeyBlock.Body().AppendBlock(ownerBlock)
	apiKeyBlock.Body().AppendNewline()

	managedResourceBlock := hclwrite.NewBlock("managed_resource", nil)
	managedResourceBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(fmt.Sprintf("data.confluent_schema_registry_cluster.%s.id", schemaRegistryName)))
	managedResourceBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference(fmt.Sprintf("data.confluent_schema_registry_cluster.%s.api_version", schemaRegistryName)))
	managedResourceBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference(fmt.Sprintf("data.confluent_schema_registry_cluster.%s.kind", schemaRegistryName)))

	environmentApiKeyBlock := hclwrite.NewBlock("environment", nil)
	envRef := GetEnvironmentReference(isNewEnv)
	environmentApiKeyBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(envRef))
	managedResourceBlock.Body().AppendBlock(environmentApiKeyBlock)
	apiKeyBlock.Body().AppendBlock(managedResourceBlock)

	return apiKeyBlock
}

// GenerateKafkaAPIKey creates a Kafka API key resource
func GenerateKafkaAPIKey(tfResourceName, envName, serviceAccountName, clusterName, clusterAdminRoleBindingName string, isNewEnv bool) *hclwrite.Block {
	apiKeyBlock := hclwrite.NewBlock("resource", []string{"confluent_api_key", tfResourceName})
	apiKeyBlock.Body().SetAttributeValue("display_name", cty.StringVal("app-manager-kafka-api-key"))
	apiKeyBlock.Body().SetAttributeValue("description", cty.StringVal("Kafka API Key that has been created by the "+envName+" environment."))
	apiKeyBlock.Body().AppendNewline()

	ownerBlock := hclwrite.NewBlock("owner", nil)
	ownerBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(fmt.Sprintf("confluent_service_account.%s.id", serviceAccountName)))
	ownerBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference(fmt.Sprintf("confluent_service_account.%s.api_version", serviceAccountName)))
	ownerBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference(fmt.Sprintf("confluent_service_account.%s.kind", serviceAccountName)))
	apiKeyBlock.Body().AppendBlock(ownerBlock)
	apiKeyBlock.Body().AppendNewline()

	managedResourceBlock := hclwrite.NewBlock("managed_resource", nil)
	managedResourceBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(fmt.Sprintf("confluent_kafka_cluster.%s.id", clusterName)))
	managedResourceBlock.Body().SetAttributeRaw("api_version", utils.TokensForResourceReference(fmt.Sprintf("confluent_kafka_cluster.%s.api_version", clusterName)))
	managedResourceBlock.Body().SetAttributeRaw("kind", utils.TokensForResourceReference(fmt.Sprintf("confluent_kafka_cluster.%s.kind", clusterName)))
	managedResourceBlock.Body().AppendNewline()

	environmentApiKeyBlock := hclwrite.NewBlock("environment", nil)
	envRef := GetEnvironmentReference(isNewEnv)
	environmentApiKeyBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(envRef))
	managedResourceBlock.Body().AppendBlock(environmentApiKeyBlock)
	apiKeyBlock.Body().AppendBlock(managedResourceBlock)
	apiKeyBlock.Body().AppendNewline()

	apiKeyBlock.Body().SetAttributeValue("disable_wait_for_ready", cty.BoolVal(true))

	apiKeyBlock.Body().SetAttributeRaw("depends_on", utils.TokensForList([]string{
		fmt.Sprintf("confluent_role_binding.%s", clusterAdminRoleBindingName),
	}))

	return apiKeyBlock
}
