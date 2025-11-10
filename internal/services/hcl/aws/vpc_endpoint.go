package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateVpcEndpointResourceNew(tfResourceName, vpcIdVarName, privateLinkAttachmentServiceNameRef, securityGroupRef, subnetRef string, dependsOnRefs []string) *hclwrite.Block {
	vpcEndpointBlock := hclwrite.NewBlock("resource", []string{"aws_vpc_endpoint", tfResourceName})

	vpcEndpointBlock.Body().SetAttributeRaw("vpc_id", utils.TokensForVarReference(vpcIdVarName))
	vpcEndpointBlock.Body().SetAttributeRaw("service_name", utils.TokensForResourceReference(privateLinkAttachmentServiceNameRef))
	vpcEndpointBlock.Body().SetAttributeValue("vpc_endpoint_type", cty.StringVal("Interface"))
	vpcEndpointBlock.Body().SetAttributeRaw("security_group_ids", utils.TokensForList([]string{securityGroupRef}))
	vpcEndpointBlock.Body().SetAttributeRaw("subnet_ids", utils.TokensForResourceReference(subnetRef))
	vpcEndpointBlock.Body().AppendNewline()
	
	vpcEndpointBlock.Body().SetAttributeRaw("depends_on", utils.TokensForList(dependsOnRefs))

	return vpcEndpointBlock
}

func GenerateVpcEndpointResource(tfResourceName, vpcIdVarName, privateLinkAttachmentServiceNameRef, securityGroupIdsVarName, subnetIdsVarName string, dependsOnRefs []string) *hclwrite.Block {
	vpcEndpointBlock := hclwrite.NewBlock("resource", []string{"aws_vpc_endpoint", tfResourceName})

	vpcEndpointBlock.Body().SetAttributeRaw("vpc_id", utils.TokensForVarReference(vpcIdVarName))
	vpcEndpointBlock.Body().SetAttributeRaw("service_name", utils.TokensForResourceReference(privateLinkAttachmentServiceNameRef))
	vpcEndpointBlock.Body().SetAttributeValue("vpc_endpoint_type", cty.StringVal("Interface"))
	vpcEndpointBlock.Body().SetAttributeRaw("security_group_ids", utils.TokensForVarReference(securityGroupIdsVarName))
	vpcEndpointBlock.Body().SetAttributeRaw("subnet_ids", utils.TokensForVarReference(subnetIdsVarName))
	vpcEndpointBlock.Body().AppendNewline()
	
	vpcEndpointBlock.Body().SetAttributeRaw("depends_on", utils.TokensForList(dependsOnRefs))

	return vpcEndpointBlock
}