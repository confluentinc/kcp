package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateMirrorTopic(tfResourceName, topicName, clusterLinkName, clusterId, clusterRestEndpoint string) *hclwrite.Block {
	mirrorTopicBlock := hclwrite.NewBlock("resource", []string{"confluent_kafka_mirror_topic", tfResourceName})

	sourceKafkaTopicBlock := hclwrite.NewBlock("source_kafka_topic", nil)
	sourceKafkaTopicBlock.Body().SetAttributeValue("topic_name", cty.StringVal(topicName))
	mirrorTopicBlock.Body().AppendBlock(sourceKafkaTopicBlock)
	mirrorTopicBlock.Body().AppendNewline()

	clusterLinkBlock := hclwrite.NewBlock("cluster_link", nil)
	clusterLinkBlock.Body().SetAttributeValue("link_name", cty.StringVal(clusterLinkName))
	mirrorTopicBlock.Body().AppendBlock(clusterLinkBlock)
	mirrorTopicBlock.Body().AppendNewline()

	kafkaClusterBlock := hclwrite.NewBlock("kafka_cluster", nil)
	kafkaClusterBlock.Body().SetAttributeValue("id", cty.StringVal(clusterId))
	kafkaClusterBlock.Body().SetAttributeValue("rest_endpoint", cty.StringVal(clusterRestEndpoint))
	kafkaClusterBlock.Body().AppendNewline()
	kafkaClusterCredentialsBlock := hclwrite.NewBlock("credentials", nil)
	kafkaClusterCredentialsBlock.Body().SetAttributeRaw("key", utils.TokensForResourceReference("var.confluent_cloud_cluster_api_key"))
	kafkaClusterCredentialsBlock.Body().SetAttributeRaw("secret", utils.TokensForResourceReference("var.confluent_cloud_cluster_api_secret"))
	kafkaClusterBlock.Body().AppendBlock(kafkaClusterCredentialsBlock)
	mirrorTopicBlock.Body().AppendBlock(kafkaClusterBlock)

	return mirrorTopicBlock
}
