package aws

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// Generates a single subnet resource when the CIDR range is known and a single string variable.
func GenerateSubnetResource(tfResourceName, cidrRangeVarName, availabilityZoneRef, vpcIdVarName string) *hclwrite.Block {
	subnetBlock := hclwrite.NewBlock("resource", []string{"aws_subnet", tfResourceName})
	subnetBlock.Body().SetAttributeRaw("vpc_id", utils.TokensForVarReference(vpcIdVarName))
	subnetBlock.Body().SetAttributeRaw("availability_zone", utils.TokensForResourceReference(availabilityZoneRef))
	subnetBlock.Body().SetAttributeRaw("cidr_block", utils.TokensForVarReference(cidrRangeVarName))

	return subnetBlock
}

// Generates a single subnet resource with a `for_each` meta-argument to iterate through a list of string variable containing CIDR ranges.
func GenerateSubnetResourceWithForEach(tfResourceName, subnetCidrsVarName, availabilityZoneRef, vpcIdVarName string) *hclwrite.Block {
	subnetBlock := hclwrite.NewBlock("resource", []string{"aws_subnet", tfResourceName})
	subnetBlock.Body().SetAttributeRaw("for_each", utils.TokensForVarReference(subnetCidrsVarName))
	subnetBlock.Body().AppendNewline()
	
	subnetBlock.Body().SetAttributeRaw("vpc_id", utils.TokensForVarReference(vpcIdVarName))
	subnetBlock.Body().SetAttributeRaw("availability_zone", utils.TokensForResourceReference(fmt.Sprintf("%s.names[each.key]", availabilityZoneRef)))
	subnetBlock.Body().SetAttributeRaw("cidr_block", utils.TokensForResourceReference("each.value"))

	return subnetBlock
}

func GenerateSubnetDataSource(tfResourceName, subnetId string) *hclwrite.Block {
	subnetDataBlock := hclwrite.NewBlock("data", []string{"aws_subnet", tfResourceName})
	subnetDataBlock.Body().SetAttributeValue("id", cty.StringVal(subnetId))

	return subnetDataBlock
}

func GenerateSubnetResourceReference(tfResourceName string) string {
	return fmt.Sprintf("aws_subnet.%s.id", tfResourceName)
}
