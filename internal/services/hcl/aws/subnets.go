package aws

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateSubnetResource(tfResourceNamePrefix, vpcId, cidrRange, availabilityZoneRef string, index int) *hclwrite.Block {
	subnetBlock := hclwrite.NewBlock("resource", []string{"aws_subnet", fmt.Sprintf("%s_%d", tfResourceNamePrefix, index)})
	subnetBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	subnetBlock.Body().SetAttributeRaw("availability_zone", utils.TokensForResourceReference(availabilityZoneRef))
	subnetBlock.Body().SetAttributeValue("cidr_block", cty.StringVal(cidrRange))
	return subnetBlock
}
