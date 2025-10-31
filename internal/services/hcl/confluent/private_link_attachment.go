package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GeneratePrivateLinkAttachment(tfResourceName, displayName, region, environmentIdRef string) *hclwrite.Block {
	privateLinkAttachmentBlock := hclwrite.NewBlock("resource", []string{"confluent_private_link_attachment", tfResourceName})
	privateLinkAttachmentBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))
	privateLinkAttachmentBlock.Body().SetAttributeValue("cloud", cty.StringVal("AWS"))
	privateLinkAttachmentBlock.Body().SetAttributeValue("region", cty.StringVal(region))
	privateLinkAttachmentBlock.Body().AppendNewline()

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(environmentIdRef))
	privateLinkAttachmentBlock.Body().AppendBlock(environmentBlock)
	privateLinkAttachmentBlock.Body().AppendNewline()

	return privateLinkAttachmentBlock
}

func GeneratePrivateLinkAttachmentConnection(tfResourceName, displayName, environmentIdRef, vpcEndpointIdRef, privateLinkAttachmentIdRef string) *hclwrite.Block {
	privateLinkAttachmentConnectionBlock := hclwrite.NewBlock("resource", []string{"confluent_private_link_attachment_connection", tfResourceName})
	privateLinkAttachmentConnectionBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(environmentIdRef))
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
