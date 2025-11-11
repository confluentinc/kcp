package aws

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateRouteTableResource(tfResourceName, gatewayIdReference, vpcIdVarName string) *hclwrite.Block {
	routeTableBlock := hclwrite.NewBlock("resource", []string{"aws_route_table", tfResourceName})
	routeTableBlock.Body().SetAttributeRaw("vpc_id", utils.TokensForVarReference(vpcIdVarName))
	routeBlock := hclwrite.NewBlock("route", nil)
	routeBlock.Body().SetAttributeValue("cidr_block", cty.StringVal("0.0.0.0/0"))
	routeBlock.Body().SetAttributeRaw("gateway_id", utils.TokensForResourceReference(gatewayIdReference))
	routeTableBlock.Body().AppendBlock(routeBlock)

	return routeTableBlock
}

func GenerateRouteTableAssociationResource(tfResourceName, subnetIdReference, routeTableIdReference string) *hclwrite.Block {
	routeTableAssociationBlock := hclwrite.NewBlock("resource", []string{"aws_route_table_association", tfResourceName})
	routeTableAssociationBlock.Body().SetAttributeRaw("subnet_id", utils.TokensForResourceReference(fmt.Sprintf("%s.id", subnetIdReference)))
	routeTableAssociationBlock.Body().SetAttributeRaw("route_table_id", utils.TokensForResourceReference(routeTableIdReference))

	return routeTableAssociationBlock
}

func GenerateRouteTableAssociationResourceWithCount(tfResourceName, subnetIdReference, routeTableIdReference string) *hclwrite.Block {
	routeTableAssociationBlock := hclwrite.NewBlock("resource", []string{"aws_route_table_association", tfResourceName})
	routeTableAssociationBlock.Body().SetAttributeRaw("count", utils.TokensForFunctionCall("length", utils.TokensForResourceReference(subnetIdReference)))
	routeTableAssociationBlock.Body().AppendNewline()

	routeTableAssociationBlock.Body().SetAttributeRaw("subnet_id", utils.TokensForResourceReference(fmt.Sprintf("%s[count.index].id", subnetIdReference)))
	routeTableAssociationBlock.Body().SetAttributeRaw("route_table_id", utils.TokensForResourceReference(routeTableIdReference))

	return routeTableAssociationBlock
}
