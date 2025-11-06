package aws

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

func GenerateInternetGatewayResource(tfResourceName, vpcIdVarName string) *hclwrite.Block {
	internetGatewayBlock := hclwrite.NewBlock("resource", []string{"aws_internet_gateway", tfResourceName})
	internetGatewayBlock.Body().SetAttributeRaw("vpc_id", utils.TokensForVarReference(vpcIdVarName))
	internetGatewayBlock.Body().AppendNewline()

	return internetGatewayBlock
}

func GenerateInternetGatewayDataSource(tfResourceName, vpcIdVarName string) *hclwrite.Block {
	internetGatewayDataBlock := hclwrite.NewBlock("data", []string{"aws_internet_gateway", tfResourceName})
	internetGatewayDataBlock.Body().SetAttributeRaw("filter", utils.TokensForMap(map[string]hclwrite.Tokens{
		"name":   utils.TokensForStringTemplate("attachment.vpc-id"),
		"values": utils.TokensForList([]string{"var." + vpcIdVarName}),
	}))

	return internetGatewayDataBlock
}

func GetInternetGatewayReference(existingInternetGateway bool, internetGatewayTfResourceName string) string {
	if existingInternetGateway {
		return fmt.Sprintf("data.aws_internet_gateway.%s.id", internetGatewayTfResourceName)
	}
	return fmt.Sprintf("aws_internet_gateway.%s.id", internetGatewayTfResourceName)
}
