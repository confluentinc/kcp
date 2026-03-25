package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GeneratePrivateLinkAttachmentResource(tfResourceName, displayName, awsRegionVarName, targetEnvironmentIdVarName string) *hclwrite.Block {
	privateLinkAttachmentBlock := hclwrite.NewBlock("resource", []string{"confluent_private_link_attachment", tfResourceName})
	privateLinkAttachmentBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))
	privateLinkAttachmentBlock.Body().SetAttributeValue("cloud", cty.StringVal("AWS"))
	privateLinkAttachmentBlock.Body().SetAttributeRaw("region", utils.TokensForVarReference(awsRegionVarName))
	privateLinkAttachmentBlock.Body().AppendNewline()

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForVarReference(targetEnvironmentIdVarName))
	privateLinkAttachmentBlock.Body().AppendBlock(environmentBlock)
	privateLinkAttachmentBlock.Body().AppendNewline()

	return privateLinkAttachmentBlock
}

// GenerateIngressGatewayResource generates a confluent_gateway resource with aws_ingress_private_link_gateway.
// Replaces GeneratePrivateLinkAttachmentResource for enterprise clusters.
func GenerateIngressGatewayResource(tfResourceName, displayName, awsRegionVarName, environmentIdVarName string) *hclwrite.Block {
	gatewayBlock := hclwrite.NewBlock("resource", []string{"confluent_gateway", tfResourceName})
	gatewayBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForVarReference(environmentIdVarName))
	gatewayBlock.Body().AppendBlock(environmentBlock)

	ingressBlock := hclwrite.NewBlock("aws_ingress_private_link_gateway", nil)
	ingressBlock.Body().SetAttributeRaw("region", utils.TokensForVarReference(awsRegionVarName))
	gatewayBlock.Body().AppendBlock(ingressBlock)

	return gatewayBlock
}

// GenerateAccessPointResource generates a confluent_access_point resource with aws_ingress_private_link_endpoint.
// Replaces GeneratePrivateLinkAttachmentConnectionResource for enterprise clusters.
func GenerateAccessPointResource(tfResourceName, displayName, environmentIdVarName, vpcEndpointIdRef, gatewayIdRef string) *hclwrite.Block {
	accessPointBlock := hclwrite.NewBlock("resource", []string{"confluent_access_point", tfResourceName})
	accessPointBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForVarReference(environmentIdVarName))
	accessPointBlock.Body().AppendBlock(environmentBlock)

	gatewayBlock := hclwrite.NewBlock("gateway", nil)
	gatewayBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(gatewayIdRef))
	accessPointBlock.Body().AppendBlock(gatewayBlock)

	ingressBlock := hclwrite.NewBlock("aws_ingress_private_link_endpoint", nil)
	ingressBlock.Body().SetAttributeRaw("vpc_endpoint_id", utils.TokensForResourceReference(vpcEndpointIdRef))
	accessPointBlock.Body().AppendBlock(ingressBlock)

	return accessPointBlock
}

func GeneratePrivateLinkAttachmentConnectionResource(tfResourceName, displayName, targetEnvironmentIdVarName, vpcEndpointIdRef, privateLinkAttachmentIdRef string) *hclwrite.Block {
	privateLinkAttachmentConnectionBlock := hclwrite.NewBlock("resource", []string{"confluent_private_link_attachment_connection", tfResourceName})
	privateLinkAttachmentConnectionBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForVarReference(targetEnvironmentIdVarName))
	privateLinkAttachmentConnectionBlock.Body().AppendBlock(environmentBlock)
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	awsBlock := hclwrite.NewBlock("aws", nil)
	awsBlock.Body().SetAttributeRaw("vpc_endpoint_id", utils.TokensForResourceReference(vpcEndpointIdRef))
	privateLinkAttachmentConnectionBlock.Body().AppendBlock(awsBlock)
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	privateLinkAttachment := hclwrite.NewBlock("private_link_attachment", nil)
	privateLinkAttachment.Body().SetAttributeRaw("id", utils.TokensForResourceReference(privateLinkAttachmentIdRef))
	privateLinkAttachmentConnectionBlock.Body().AppendBlock(privateLinkAttachment)
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	return privateLinkAttachmentConnectionBlock
}
