package aws

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateRoute53Zone(tfResourceName, vpcId, privateLinkAttachmentName string) *hclwrite.Block {
	route53ZoneBlock := hclwrite.NewBlock("resource", []string{"aws_route53_zone", tfResourceName})
	route53ZoneBlock.Body().SetAttributeRaw("name", utils.TokensForResourceReference(fmt.Sprintf("confluent_private_link_attachment.%s.dns_domain", privateLinkAttachmentName)))
	route53ZoneBlock.Body().AppendNewline()

	vpcBlock := hclwrite.NewBlock("vpc", nil)
	vpcBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	route53ZoneBlock.Body().AppendBlock(vpcBlock)

	return route53ZoneBlock
}

func GenerateRoute53Record(tfResourceName, route53ZoneName, vpcEndpointName string) *hclwrite.Block {
	route53RecordBlock := hclwrite.NewBlock("resource", []string{"aws_route53_record", tfResourceName})
	route53RecordBlock.Body().SetAttributeRaw("zone_id", utils.TokensForResourceReference(fmt.Sprintf("aws_route53_zone.%s.zone_id", route53ZoneName)))
	route53RecordBlock.Body().SetAttributeValue("name", cty.StringVal("*"))
	route53RecordBlock.Body().SetAttributeValue("type", cty.StringVal("CNAME"))
	route53RecordBlock.Body().SetAttributeValue("ttl", cty.NumberIntVal(60))
	route53RecordBlock.Body().SetAttributeRaw("records", utils.TokensForList([]string{fmt.Sprintf("aws_vpc_endpoint.%s.dns_entry[0].dns_name", vpcEndpointName)}))

	return route53RecordBlock
}
