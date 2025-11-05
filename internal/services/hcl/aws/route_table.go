package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateRouteTableResource(tfResourceName, vpcId, gatewayIdRef string) *hclwrite.Block {
	routeTableBlock := hclwrite.NewBlock("resource", []string{"aws_route_table", tfResourceName})
	routeTableBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	routeBlock := hclwrite.NewBlock("route", nil)
	routeBlock.Body().SetAttributeValue("cidr_block", cty.StringVal("0.0.0.0/0"))
	routeBlock.Body().SetAttributeRaw("gateway_id", utils.TokensForResourceReference(gatewayIdRef))
	routeTableBlock.Body().AppendBlock(routeBlock)

	routeTableBlock.Body().AppendNewline()

	return routeTableBlock
}
