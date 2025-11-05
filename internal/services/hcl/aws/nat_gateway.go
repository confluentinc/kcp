package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

func GenerateNATGatewayResource(tfResourceName, allocationId, subnetId string) *hclwrite.Block {
	natGatewayBlock := hclwrite.NewBlock("resource", []string{"aws_nat_gateway", tfResourceName})
	natGatewayBlock.Body().SetAttributeRaw("allocation_id", utils.TokensForResourceReference(allocationId))
	natGatewayBlock.Body().SetAttributeRaw("subnet_id", utils.TokensForResourceReference(subnetId))
	natGatewayBlock.Body().AppendNewline()

	return natGatewayBlock
}
