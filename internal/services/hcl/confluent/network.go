package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateNetworkResource creates a confluent_network resource for dedicated private link clusters.
func GenerateNetworkResource(tfResourceName, regionVarName, environmentIdRef string, preventDestroy bool) *hclwrite.Block {
	networkBlock := hclwrite.NewBlock("resource", []string{"confluent_network", tfResourceName})
	networkBlock.Body().SetAttributeValue("display_name", cty.StringVal("private-link-network"))
	networkBlock.Body().SetAttributeValue("cloud", cty.StringVal("AWS"))
	networkBlock.Body().SetAttributeRaw("region", utils.TokensForVarReference(regionVarName))
	networkBlock.Body().SetAttributeRaw("connection_types", utils.TokensForStringList([]string{"PRIVATELINK"}))
	networkBlock.Body().AppendNewline()

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(environmentIdRef))
	networkBlock.Body().AppendBlock(environmentBlock)
	networkBlock.Body().AppendNewline()

	dnsConfigBlock := hclwrite.NewBlock("dns_config", nil)
	dnsConfigBlock.Body().SetAttributeValue("resolution", cty.StringVal("PRIVATE"))
	networkBlock.Body().AppendBlock(dnsConfigBlock)
	networkBlock.Body().AppendNewline()

	utils.GenerateLifecycleBlock(networkBlock, "prevent_destroy", preventDestroy)

	return networkBlock
}

// GeneratePrivateLinkAccessResource creates a confluent_private_link_access resource for dedicated clusters.
func GeneratePrivateLinkAccessResource(tfResourceName, displayName, awsAccountIdRef, environmentIdRef, networkIdRef string) *hclwrite.Block {
	plAccessBlock := hclwrite.NewBlock("resource", []string{"confluent_private_link_access", tfResourceName})
	plAccessBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))
	plAccessBlock.Body().AppendNewline()

	awsBlock := hclwrite.NewBlock("aws", nil)
	awsBlock.Body().SetAttributeRaw("account", utils.TokensForResourceReference(awsAccountIdRef))
	plAccessBlock.Body().AppendBlock(awsBlock)
	plAccessBlock.Body().AppendNewline()

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(environmentIdRef))
	plAccessBlock.Body().AppendBlock(environmentBlock)
	plAccessBlock.Body().AppendNewline()

	networkBlock := hclwrite.NewBlock("network", nil)
	networkBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(networkIdRef))
	plAccessBlock.Body().AppendBlock(networkBlock)
	plAccessBlock.Body().AppendNewline()

	return plAccessBlock
}
