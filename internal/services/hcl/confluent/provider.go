package confluent

import (
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

const (
	VarConfluentCloudAPIKey = "confluent_cloud_api_key"
	VarConfluentCloudAPISecret = "confluent_cloud_api_secret"
)

var ConfluentProviderVariables = []types.TerraformVariable{
	{Name: VarConfluentCloudAPIKey, Description: "Confluent Cloud API Key", Sensitive: false, Type: "string"},
	{Name: VarConfluentCloudAPISecret, Description: "Confluent Cloud API Secret", Sensitive: true, Type: "string"},
}

func GenerateRequiredProviderTokens() (string, hclwrite.Tokens) {
	confluentProvider := map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("confluentinc/confluent"),
		"version": utils.TokensForStringTemplate("~> 2.5"),
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
