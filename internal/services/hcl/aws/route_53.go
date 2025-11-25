package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateRoute53ZoneResource(tfResourceName, vpcIdVarName, privateLinkAttachmentDnsDomainRef string) *hclwrite.Block {
	route53ZoneBlock := hclwrite.NewBlock("resource", []string{"aws_route53_zone", tfResourceName})
	route53ZoneBlock.Body().SetAttributeRaw("name", utils.TokensForResourceReference(privateLinkAttachmentDnsDomainRef))
	route53ZoneBlock.Body().AppendNewline()

	vpcBlock := hclwrite.NewBlock("vpc", nil)
	vpcBlock.Body().SetAttributeRaw("vpc_id", utils.TokensForVarReference(vpcIdVarName))
	route53ZoneBlock.Body().AppendBlock(vpcBlock)

	return route53ZoneBlock
}

func GenerateRoute53ZoneDataSource(tfResourceName, vpcIdVarName, privateLinkAttachmentDnsDomainRef string) *hclwrite.Block {
	route53ZoneDataBlock := hclwrite.NewBlock("data", []string{"aws_route53_zone", tfResourceName})
	route53ZoneDataBlock.Body().SetAttributeRaw("name", utils.TokensForResourceReference(privateLinkAttachmentDnsDomainRef))
	route53ZoneDataBlock.Body().SetAttributeValue("private_zone", cty.BoolVal(true))
	route53ZoneDataBlock.Body().SetAttributeRaw("vpc_id", utils.TokensForVarReference(vpcIdVarName))

	return route53ZoneDataBlock
}

func GenerateRoute53RecordResource(tfResourceName, route53ZoneIdRef, recordName, vpcEndpointDnsEntryRef string) *hclwrite.Block {
	route53RecordBlock := hclwrite.NewBlock("resource", []string{"aws_route53_record", tfResourceName})
	route53RecordBlock.Body().SetAttributeRaw("zone_id", utils.TokensForResourceReference(route53ZoneIdRef))
	route53RecordBlock.Body().SetAttributeValue("name", cty.StringVal(recordName))
	route53RecordBlock.Body().SetAttributeValue("type", cty.StringVal("CNAME"))
	route53RecordBlock.Body().SetAttributeValue("ttl", cty.NumberIntVal(60))
	route53RecordBlock.Body().SetAttributeRaw("records", utils.TokensForList([]string{vpcEndpointDnsEntryRef}))

	return route53RecordBlock
}