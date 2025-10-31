package confluent

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateKafkaACL creates a Kafka ACL resource
func GenerateKafkaACL(tfResourceName, resourceType, resourceName, patternType, principal, operation, clusterName, apiKeyName string) *hclwrite.Block {
	aclBlock := hclwrite.NewBlock("resource", []string{"confluent_kafka_acl", tfResourceName})

	kafkaClusterBlock := hclwrite.NewBlock("kafka_cluster", nil)
	kafkaClusterBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(fmt.Sprintf("confluent_kafka_cluster.%s.id", clusterName)))
	aclBlock.Body().AppendBlock(kafkaClusterBlock)
	aclBlock.Body().AppendNewline()

	aclBlock.Body().SetAttributeValue("resource_type", cty.StringVal(resourceType))
	aclBlock.Body().SetAttributeValue("resource_name", cty.StringVal(resourceName))
	aclBlock.Body().SetAttributeValue("pattern_type", cty.StringVal(patternType))
	aclBlock.Body().SetAttributeRaw("principal", utils.TokensForStringTemplate(principal))
	aclBlock.Body().SetAttributeValue("host", cty.StringVal("*"))
	aclBlock.Body().SetAttributeValue("operation", cty.StringVal(operation))
	aclBlock.Body().SetAttributeValue("permission", cty.StringVal("ALLOW"))
	aclBlock.Body().SetAttributeRaw("rest_endpoint", utils.TokensForResourceReference(fmt.Sprintf("confluent_kafka_cluster.%s.rest_endpoint", clusterName)))
	aclBlock.Body().AppendNewline()

	credentialsBlock := hclwrite.NewBlock("credentials", nil)
	credentialsBlock.Body().SetAttributeRaw("key", utils.TokensForResourceReference(fmt.Sprintf("confluent_api_key.%s.id", apiKeyName)))
	credentialsBlock.Body().SetAttributeRaw("secret", utils.TokensForResourceReference(fmt.Sprintf("confluent_api_key.%s.secret", apiKeyName)))
	aclBlock.Body().AppendBlock(credentialsBlock)

	return aclBlock
}
