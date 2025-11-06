package aws

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateSubnetResource(tfResourceName, vpcId, cidrRange, availabilityZoneRef string) *hclwrite.Block {
	subnetBlock := hclwrite.NewBlock("resource", []string{"aws_subnet", tfResourceName})
	subnetBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	subnetBlock.Body().SetAttributeRaw("availability_zone", utils.TokensForResourceReference(availabilityZoneRef))
	subnetBlock.Body().SetAttributeValue("cidr_block", cty.StringVal(cidrRange))

	return subnetBlock
}

func GenerateSubnetResourceReference(tfResourceName string) string {
	return fmt.Sprintf("aws_subnet.%s.id", tfResourceName)
}
