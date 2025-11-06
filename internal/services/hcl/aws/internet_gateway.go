package aws

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

var InternetGatewayVariables = []types.TerraformVariable{
	{Name: VarVpcId, Description: "ID of the VPC", Sensitive: false, Type: "string"},
}

func GenerateInternetGatewayResource(tfResourceName string) *hclwrite.Block {
	internetGatewayBlock := hclwrite.NewBlock("resource", []string{"aws_internet_gateway", tfResourceName})
	internetGatewayBlock.Body().SetAttributeRaw("vpc_id", utils.TokensForVarReference(VarVpcId))
	internetGatewayBlock.Body().AppendNewline()

	return internetGatewayBlock
}

func GenerateInternetGatewayDataSource(tfResourceName string) *hclwrite.Block {
	internetGatewayDataBlock := hclwrite.NewBlock("data", []string{"aws_internet_gateway", tfResourceName})
	internetGatewayDataBlock.Body().SetAttributeRaw("filter", utils.TokensForMap(map[string]hclwrite.Tokens{
		"name":   utils.TokensForStringTemplate("attachment.vpc-id"),
		"values": utils.TokensForList([]string{"var." + VarVpcId}), // TODO: revisit this - can we use the variable reference directly?
	}))

	return internetGatewayDataBlock
}

func GetInternetGatewayReference(existingInternetGateway bool, internetGatewayTfResourceName string) string {
	if existingInternetGateway {
		return fmt.Sprintf("data.aws_internet_gateway.%s.id", internetGatewayTfResourceName)
	}
	return fmt.Sprintf("aws_internet_gateway.%s.id", internetGatewayTfResourceName)
}
