package aws

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateSubnets(vpcId, cidrRange string, index int) *hclwrite.Block {
	subnetsBlock := hclwrite.NewBlock("resource", []string{"aws_subnet", fmt.Sprintf("cflt_private_link_subnet_%d", index)})
	subnetsBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	subnetsBlock.Body().SetAttributeRaw("availability_zone", utils.TokensForResourceReference(fmt.Sprintf("data.aws_availability_zones.available.names[%d]", index)))
	subnetsBlock.Body().SetAttributeValue("cidr_block", cty.StringVal(cidrRange))
	return subnetsBlock
}
