package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateRoute53Zone(vpcId string) *hclwrite.Block {
	route53ZoneBlock := hclwrite.NewBlock("resource", []string{"aws_route53_zone", "cflt_private_link_zone"})
	route53ZoneBlock.Body().SetAttributeRaw("name", utils.TokensForResourceReference("confluent_private_link_attachment.private_link_attachment.dns_domain"))
	route53ZoneBlock.Body().AppendNewline()

	vpcBlock := hclwrite.NewBlock("vpc", nil)
	vpcBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	route53ZoneBlock.Body().AppendBlock(vpcBlock)

	return route53ZoneBlock
}

func GenerateRoute53Record() *hclwrite.Block {
	route53RecordBlock := hclwrite.NewBlock("resource", []string{"aws_route53_record", "cflt_route_entries"})
	route53RecordBlock.Body().SetAttributeRaw("zone_id", utils.TokensForResourceReference("aws_route53_zone.cflt_private_link_zone.zone_id"))
	route53RecordBlock.Body().SetAttributeValue("name", cty.StringVal("*"))
	route53RecordBlock.Body().SetAttributeValue("type", cty.StringVal("CNAME"))
	route53RecordBlock.Body().SetAttributeValue("ttl", cty.NumberIntVal(60))
	route53RecordBlock.Body().SetAttributeRaw("records", utils.TokensForList([]string{"aws_vpc_endpoint.cflt_private_link_vpc_endpoint.dns_entry[0].dns_name"}))

	return route53RecordBlock
}
