package aws

import (
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

const (
	VarAwsRegion = "aws_region"
)

var AwsProviderVariables = []types.TerraformVariable{
	{Name: VarAwsRegion, Description: "The AWS region", Sensitive: false, Type: "string"},
}

func GenerateRequiredProviderTokens() (string, hclwrite.Tokens) {
	awsProvider := map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("hashicorp/aws"),
		"version": utils.TokensForStringTemplate("6.18.0"),
	}

	return "aws", utils.TokensForMap(awsProvider)
}

func GenerateProviderBlock(region string) *hclwrite.Block {
	providerBlock := hclwrite.NewBlock("provider", []string{"aws"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeValue("region", cty.StringVal(region))

	return providerBlock
}

// GenerateProviderBlockWithVar generates an AWS provider block that uses a variable reference for the region
func GenerateProviderBlockWithVar() *hclwrite.Block {
	providerBlock := hclwrite.NewBlock("provider", []string{"aws"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeRaw("region", utils.TokensForVarReference(VarAwsRegion))

	return providerBlock
}
