package aws

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateSubnets(tfResourceNamePrefix, vpcId, cidrRange, availabilityZoneRef string, index int) *hclwrite.Block {
	subnetsBlock := hclwrite.NewBlock("resource", []string{"aws_subnet", fmt.Sprintf("%s_%d", tfResourceNamePrefix, index)})
	subnetsBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	subnetsBlock.Body().SetAttributeRaw("availability_zone", utils.TokensForResourceReference(availabilityZoneRef))
	subnetsBlock.Body().SetAttributeValue("cidr_block", cty.StringVal(cidrRange))
	return subnetsBlock
}
