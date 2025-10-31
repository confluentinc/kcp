package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GeneratePrivateLinkAttachment(displayName, region string) *hclwrite.Block {
	privateLinkAttachmentBlock := hclwrite.NewBlock("resource", []string{"confluent_private_link_attachment", "private_link_attachment"})
	privateLinkAttachmentBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))
	privateLinkAttachmentBlock.Body().SetAttributeValue("cloud", cty.StringVal("AWS"))
	privateLinkAttachmentBlock.Body().SetAttributeValue("region", cty.StringVal(region))
	privateLinkAttachmentBlock.Body().AppendNewline()

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference("confluent_environment.environment.id"))
	privateLinkAttachmentBlock.Body().AppendBlock(environmentBlock)
	privateLinkAttachmentBlock.Body().AppendNewline()

	return privateLinkAttachmentBlock
}

func GeneratePrivateLinkAttachmentConnection(displayName string) *hclwrite.Block {
	privateLinkAttachmentConnectionBlock := hclwrite.NewBlock("resource", []string{"confluent_private_link_attachment_connection", "private_link_attachment_connection"})
	privateLinkAttachmentConnectionBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference("confluent_environment.environment.id"))
	privateLinkAttachmentConnectionBlock.Body().AppendBlock(environmentBlock)
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	awsBlock := hclwrite.NewBlock("aws", nil)
	awsBlock.Body().SetAttributeRaw("vpc_endpoint_id", utils.TokensForResourceReference("aws_vpc_endpoint.cflt_private_link_vpc_endpoint.id"))
	privateLinkAttachmentConnectionBlock.Body().AppendBlock(awsBlock)
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	privateLinkAttachment := hclwrite.NewBlock("private_link_attachment", nil)
	privateLinkAttachment.Body().SetAttributeRaw("id", utils.TokensForResourceReference("confluent_private_link_attachment.private_link_attachment.id"))
	privateLinkAttachmentConnectionBlock.Body().AppendBlock(privateLinkAttachment)
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	return privateLinkAttachmentConnectionBlock
}
