package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateKafkaClusterResource creates a new Confluent Kafka cluster resource
func GenerateKafkaClusterResource(name, clusterType, region string, isNewEnv bool) *hclwrite.Block {
	clusterBlock := hclwrite.NewBlock("resource", []string{"confluent_kafka_cluster", "cluster"})
	clusterBlock.Body().SetAttributeValue("display_name", cty.StringVal(name))
	clusterBlock.Body().SetAttributeValue("cloud", cty.StringVal("AWS"))
	clusterBlock.Body().SetAttributeValue("region", cty.StringVal(region))

	switch clusterType {
	case "dedicated":
		/*
			When we begin work on sizing the Confluent Cloud dedicated cluster based on the MSK cluster, we need to beware that
			`MULTI_ZONE` is required if CKUs exceed 1.
		*/
		clusterBlock.Body().SetAttributeValue("availability", cty.StringVal("SINGLE_ZONE"))
		clusterBlock.Body().AppendNewline()
		dedicatedBlock := clusterBlock.Body().AppendNewBlock("dedicated", nil)
		dedicatedBlock.Body().SetAttributeValue("cku", cty.NumberIntVal(1))
	case "enterprise":
		clusterBlock.Body().SetAttributeValue("availability", cty.StringVal("HIGH"))
		clusterBlock.Body().AppendNewline()
		enterpriseBlock := clusterBlock.Body().AppendNewBlock("enterprise", nil)
		enterpriseBlock.Body().Clear()
	}

	clusterBlock.Body().AppendNewline()
	environmentRefBlock := hclwrite.NewBlock("environment", nil)
	envRef := GetEnvironmentReference(isNewEnv)
	environmentRefBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(envRef))

	clusterBlock.Body().AppendBlock(environmentRefBlock)
	return clusterBlock
}

// GenerateKafkaClusterDataSource creates a data source for an existing cluster
func GenerateKafkaClusterDataSource(id string, isNewEnv bool) *hclwrite.Block {
	clusterDataBlock := hclwrite.NewBlock("data", []string{"confluent_kafka_cluster", "cluster"})
	clusterDataBlock.Body().SetAttributeValue("id", cty.StringVal(id))

	environmentRefBlock := hclwrite.NewBlock("environment", nil)
	envRef := GetEnvironmentReference(isNewEnv)
	environmentRefBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(envRef))

	clusterDataBlock.Body().AppendBlock(environmentRefBlock)
	return clusterDataBlock
}
