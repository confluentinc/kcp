package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

func GenerateRequiredProviderTokens() (string, hclwrite.Tokens) {
    confluentProvider := map[string]hclwrite.Tokens{
        "source":  utils.TokensForStringTemplate("confluentinc/confluent"),
        "version": utils.TokensForStringTemplate("2.50.0"),
    }
    
    return "confluent", utils.TokensForMap(confluentProvider)
}

func GenerateProviderBlock() *hclwrite.Block {
	providerBlock := hclwrite.NewBlock("provider", []string{"confluent"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeRaw("cloud_api_key", utils.TokensForResourceReference("var.confluent_cloud_api_key"))
	providerBody.SetAttributeRaw("cloud_api_secret", utils.TokensForResourceReference("var.confluent_cloud_api_secret"))

	return providerBlock
}
