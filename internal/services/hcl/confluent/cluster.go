package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateKafkaClusterResource creates a new Confluent Kafka cluster resource
func GenerateKafkaClusterResource(tfResourceName, clusterVarName, clusterType, availability string, cku int, regionVarName, environmentIdRef, networkIdRef string, preventDestroy bool) *hclwrite.Block {
	clusterBlock := hclwrite.NewBlock("resource", []string{"confluent_kafka_cluster", tfResourceName})
	clusterBlock.Body().SetAttributeRaw("display_name", utils.TokensForVarReference(clusterVarName))
	clusterBlock.Body().SetAttributeValue("cloud", cty.StringVal("AWS"))
	clusterBlock.Body().SetAttributeRaw("region", utils.TokensForVarReference(regionVarName))

	switch clusterType {
	case "dedicated":
		clusterBlock.Body().SetAttributeValue("availability", cty.StringVal(availability))
		clusterBlock.Body().AppendNewline()
		dedicatedBlock := clusterBlock.Body().AppendNewBlock("dedicated", nil)
		dedicatedBlock.Body().SetAttributeValue("cku", cty.NumberIntVal(int64(cku)))
	case "enterprise":
		clusterBlock.Body().SetAttributeValue("availability", cty.StringVal("HIGH"))
		clusterBlock.Body().AppendNewline()
		enterpriseBlock := clusterBlock.Body().AppendNewBlock("enterprise", nil)
		enterpriseBlock.Body().Clear()
	}

	clusterBlock.Body().AppendNewline()
	environmentRefBlock := hclwrite.NewBlock("environment", nil)
	environmentRefBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(environmentIdRef))

	clusterBlock.Body().AppendBlock(environmentRefBlock)
	clusterBlock.Body().AppendNewline()

	if networkIdRef != "" {
		networkRefBlock := hclwrite.NewBlock("network", nil)
		networkRefBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(networkIdRef))
		clusterBlock.Body().AppendBlock(networkRefBlock)
		clusterBlock.Body().AppendNewline()
	}

	utils.GenerateLifecycleBlock(clusterBlock, "prevent_destroy", preventDestroy)

	return clusterBlock
}

// GenerateKafkaClusterDataSource creates a data source for an existing cluster
func GenerateKafkaClusterDataSource(tfResourceName, id, environmentIdRef string) *hclwrite.Block {
	clusterDataBlock := hclwrite.NewBlock("data", []string{"confluent_kafka_cluster", tfResourceName})
	clusterDataBlock.Body().SetAttributeValue("id", cty.StringVal(id))

	environmentRefBlock := hclwrite.NewBlock("environment", nil)
	environmentRefBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(environmentIdRef))

	clusterDataBlock.Body().AppendBlock(environmentRefBlock)
	return clusterDataBlock
}
