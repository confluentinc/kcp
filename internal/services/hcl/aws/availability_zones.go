package aws

import (
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateAvailabilityZonesDataSource(tfResourceName string) *hclwrite.Block {
	availabilityZoneBlock := hclwrite.NewBlock("data", []string{"aws_availability_zones", tfResourceName})
	availabilityZoneBlock.Body().SetAttributeValue("state", cty.StringVal("available"))
	availabilityZoneBlock.Body().AppendNewline()

	filterBlock := hclwrite.NewBlock("filter", nil)
	filterBlock.Body().SetAttributeValue("name", cty.StringVal("zone-type"))
	filterBlock.Body().SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("availability-zone")}))
	availabilityZoneBlock.Body().AppendBlock(filterBlock)

	return availabilityZoneBlock
}
