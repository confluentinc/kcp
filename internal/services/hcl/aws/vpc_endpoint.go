package aws

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateVpcEndpoint(tfResourceName, vpcId, privateLinkAttachmentName, securityGroupName, subnetPrefix string, subnetCount int) *hclwrite.Block {
	vpcEndpointBlock := hclwrite.NewBlock("resource", []string{"aws_vpc_endpoint", tfResourceName})

	vpcEndpointBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
	vpcEndpointBlock.Body().SetAttributeRaw("service_name", utils.TokensForResourceReference(fmt.Sprintf("confluent_private_link_attachment.%s.aws[0].vpc_endpoint_service_name", privateLinkAttachmentName)))
	vpcEndpointBlock.Body().SetAttributeValue("vpc_endpoint_type", cty.StringVal("Interface"))
	vpcEndpointBlock.Body().SetAttributeRaw("security_group_ids", utils.TokensForResourceReference(fmt.Sprintf("aws_security_group.%s[*].id", securityGroupName)))

	// Build subnet references dynamically
	subnetRefs := make([]string, subnetCount)
	for i := 0; i < subnetCount; i++ {
		subnetRefs[i] = fmt.Sprintf("aws_subnet.%s_%d.id", subnetPrefix, i)
	}
	vpcEndpointBlock.Body().SetAttributeRaw("subnet_ids", utils.TokensForList(subnetRefs))
	vpcEndpointBlock.Body().AppendNewline()
	vpcEndpointBlock.Body().SetAttributeRaw("depends_on", utils.TokensForList([]string{fmt.Sprintf("confluent_private_link_attachment.%s", privateLinkAttachmentName)}))

	return vpcEndpointBlock
}
