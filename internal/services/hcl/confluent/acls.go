package confluent

import (
	"strings"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateKafkaACL creates a Kafka ACL resource
func GenerateKafkaACL(tfResourceName, resourceType, resourceName, patternType, principal, host, operation, permission, clusterIdRef, clusterRestEndpointRef, apiKeyIdRef, apiKeySecretRef string) *hclwrite.Block {
	aclBlock := hclwrite.NewBlock("resource", []string{"confluent_kafka_acl", tfResourceName})

	kafkaClusterBlock := hclwrite.NewBlock("kafka_cluster", nil)
	kafkaClusterBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(clusterIdRef))
	aclBlock.Body().AppendBlock(kafkaClusterBlock)
	aclBlock.Body().AppendNewline()

	aclBlock.Body().SetAttributeValue("resource_type", cty.StringVal(strings.ToUpper(resourceType)))
	aclBlock.Body().SetAttributeValue("resource_name", cty.StringVal(resourceName))
	aclBlock.Body().SetAttributeValue("pattern_type", cty.StringVal(strings.ToUpper(patternType)))
	aclBlock.Body().SetAttributeRaw("principal", utils.TokensForStringTemplate(principal))
	aclBlock.Body().SetAttributeValue("host", cty.StringVal(host))
	aclBlock.Body().SetAttributeValue("operation", cty.StringVal(strings.ToUpper(operation)))
	aclBlock.Body().SetAttributeValue("permission", cty.StringVal(strings.ToUpper(permission)))
	aclBlock.Body().SetAttributeRaw("rest_endpoint", utils.TokensForResourceReference(clusterRestEndpointRef))
	aclBlock.Body().AppendNewline()

	credentialsBlock := hclwrite.NewBlock("credentials", nil)
	credentialsBlock.Body().SetAttributeRaw("key", utils.TokensForResourceReference(apiKeyIdRef))
	credentialsBlock.Body().SetAttributeRaw("secret", utils.TokensForResourceReference(apiKeySecretRef))
	aclBlock.Body().AppendBlock(credentialsBlock)
	aclBlock.Body().AppendNewline()

	utils.GenerateLifecycleBlock(aclBlock, "prevent_destroy", true)

	return aclBlock
}
