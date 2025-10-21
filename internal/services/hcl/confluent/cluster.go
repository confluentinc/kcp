package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateKafkaClusterResource creates a new Confluent Kafka cluster resource
func generateKafkaClusterResource(name, clusterType, region string, isNewEnv bool) *hclwrite.Block {
	clusterBlock := hclwrite.NewBlock("resource", []string{"confluent_kafka_cluster", "cluster"})
	clusterBlock.Body().SetAttributeValue("display_name", cty.StringVal(name))
	clusterBlock.Body().SetAttributeValue("availability", cty.StringVal("SINGLE_ZONE"))
	clusterBlock.Body().SetAttributeValue("cloud", cty.StringVal("AWS"))
	clusterBlock.Body().SetAttributeValue("region", cty.StringVal(region))
	clusterBlock.Body().AppendNewline()

	if clusterType == "dedicated" {
		dedicatedBlock := clusterBlock.Body().AppendNewBlock("dedicated", nil)
		dedicatedBlock.Body().SetAttributeValue("cku", cty.NumberIntVal(1))
	}

	clusterBlock.Body().AppendNewline()
	environmentRefBlock := hclwrite.NewBlock("environment", nil)
	envRef := getEnvironmentReference(isNewEnv)
	environmentRefBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(envRef))

	clusterBlock.Body().AppendBlock(environmentRefBlock)
	return clusterBlock
}

// GenerateKafkaClusterDataSource creates a data source for an existing cluster
func generateKafkaClusterDataSource(id string, isNewEnv bool) *hclwrite.Block {
	clusterDataBlock := hclwrite.NewBlock("data", []string{"confluent_kafka_cluster", "cluster"})
	clusterDataBlock.Body().SetAttributeValue("id", cty.StringVal(id))

	environmentRefBlock := hclwrite.NewBlock("environment", nil)
	envRef := getEnvironmentReference(isNewEnv)
	environmentRefBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(envRef))

	clusterDataBlock.Body().AppendBlock(environmentRefBlock)
	return clusterDataBlock
}
