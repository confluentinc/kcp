package aws

import (
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateNATGatewayResource(tfResourceName, allocationId, subnetId string) *hclwrite.Block {
	natGatewayBlock := hclwrite.NewBlock("resource", []string{"aws_nat_gateway", tfResourceName})
	natGatewayBlock.Body().SetAttributeValue("allocation_id", cty.StringVal(allocationId))
	natGatewayBlock.Body().SetAttributeValue("subnet_id", cty.StringVal(subnetId))
	natGatewayBlock.Body().AppendNewline()

	return natGatewayBlock
}
