package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateVpcEndpoint(vpcId string) *hclwrite.Block {
	vpcEndpointBlock := hclwrite.NewBlock("resource", []string{"aws_vpc_endpoint", "cflt_private_link_vpc_endpoint"})

	vpcEndpointBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	vpcEndpointBlock.Body().SetAttributeRaw("service_name", utils.TokensForResourceReference("confluent_private_link_attachment.private_link_attachment.aws[0].vpc_endpoint_service_name"))
	vpcEndpointBlock.Body().SetAttributeValue("vpc_endpoint_type", cty.StringVal("Interface"))
	vpcEndpointBlock.Body().SetAttributeRaw("security_group_ids", utils.TokensForResourceReference("aws_security_group.cflt_private_link_sg[*].id"))
	// We could potentially update the subnet resource to use a count and then reference all subnets like `..._subnet.[*].id`
	vpcEndpointBlock.Body().SetAttributeRaw("subnet_ids", utils.TokensForList([]string{"aws_subnet.cflt_private_link_subnet_0.id", "aws_subnet.cflt_private_link_subnet_1.id", "aws_subnet.cflt_private_link_subnet_2.id"}))
	vpcEndpointBlock.Body().AppendNewline()
	vpcEndpointBlock.Body().SetAttributeRaw("depends_on", utils.TokensForList([]string{"confluent_private_link_attachment.private_link_attachment"}))

	return vpcEndpointBlock
}
