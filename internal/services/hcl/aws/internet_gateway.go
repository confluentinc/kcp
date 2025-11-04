package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateInternetGatewayResource(tfResourceName, vpcId string) *hclwrite.Block {
	internetGatewayBlock := hclwrite.NewBlock("resource", []string{"aws_internet_gateway", tfResourceName})
	internetGatewayBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	internetGatewayBlock.Body().AppendNewline()

	return internetGatewayBlock
}

func GenerateInternetGatewayDataSource(tfResourceName, vpcId string) *hclwrite.Block {
	internetGatewayDataBlock := hclwrite.NewBlock("data", []string{"aws_internet_gateway", tfResourceName})
	internetGatewayDataBlock.Body().SetAttributeRaw("filter", utils.TokensForMap(map[string]hclwrite.Tokens{
		"name":   utils.TokensForStringTemplate("attachment.vpc-id"),
		"values": utils.TokensForList([]string{vpcId}),
	}))

	return internetGatewayDataBlock
}
