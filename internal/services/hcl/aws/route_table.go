package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateRouteTableResource(tfResourceName, vpcId, gatewayIdRef string) *hclwrite.Block {
	routeTableBlock := hclwrite.NewBlock("resource", []string{"aws_route_table", tfResourceName})
	routeTableBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	routeTableBlock.Body().SetAttributeRaw("gateway_id", utils.TokensForResourceReference(gatewayIdRef))
	routeTableBlock.Body().AppendNewline()

	return routeTableBlock
}
