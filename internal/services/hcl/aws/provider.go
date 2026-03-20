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
		"version": utils.TokensForStringTemplate("~> 6.0"),
	}

	return "aws", utils.TokensForMap(awsProvider)
}

func GenerateProviderBlock(region string) *hclwrite.Block {
	return generateProviderBlockWithDeploymentID(region, "")
}

// GenerateProviderBlockWithDeploymentID generates an AWS provider block with a fixed deployment ID (for testing).
func GenerateProviderBlockWithDeploymentID(region, deploymentID string) *hclwrite.Block {
	return generateProviderBlockWithDeploymentID(region, deploymentID)
}

func generateProviderBlockWithDeploymentID(region, deploymentID string) *hclwrite.Block {
	if deploymentID == "" {
		deploymentID = utils.RandomString(8)
	}

	providerBlock := hclwrite.NewBlock("provider", []string{"aws"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeValue("region", cty.StringVal(region))
	providerBody.AppendNewline()

	defaultTagsBlock := hclwrite.NewBlock("default_tags", nil)
	defaultTagsBlock.Body().SetAttributeRaw("tags", utils.TokensForMap(map[string]hclwrite.Tokens{
		"managed_by":            utils.TokensForStringTemplate("kcp"),
		"deployment_identifier": utils.TokensForStringTemplate(deploymentID),
	}))
	providerBody.AppendBlock(defaultTagsBlock)

	return providerBlock
}

// GenerateProviderBlockWithVar generates an AWS provider block that uses a variable reference for the region
func GenerateProviderBlockWithVar() *hclwrite.Block {
	return GenerateProviderBlockWithVarAndDeploymentID("")
}

// GenerateProviderBlockWithVarAndDeploymentID generates an AWS provider block with var reference and a fixed deployment ID.
func GenerateProviderBlockWithVarAndDeploymentID(deploymentID string) *hclwrite.Block {
	if deploymentID == "" {
		deploymentID = utils.RandomString(8)
	}

	providerBlock := hclwrite.NewBlock("provider", []string{"aws"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeRaw("region", utils.TokensForVarReference(VarAwsRegion))
	providerBody.AppendNewline()

	defaultTagsBlock := hclwrite.NewBlock("default_tags", nil)
	defaultTagsBlock.Body().SetAttributeRaw("tags", utils.TokensForMap(map[string]hclwrite.Tokens{
		"managed_by":            utils.TokensForStringTemplate("kcp"),
		"deployment_identifier": utils.TokensForStringTemplate(deploymentID),
	}))
	providerBody.AppendBlock(defaultTagsBlock)

	return providerBlock
}
