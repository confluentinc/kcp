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
	providerBody.SetAttributeRaw("cloud_api_key", utils.TokensForVarReference(VarConfluentCloudAPIKey))
	providerBody.SetAttributeRaw("cloud_api_secret", utils.TokensForVarReference(VarConfluentCloudAPISecret))

	return providerBlock
}

func GenerateEmptyProviderBlock() *hclwrite.Block {
	return hclwrite.NewBlock("provider", []string{"confluent"})
}
