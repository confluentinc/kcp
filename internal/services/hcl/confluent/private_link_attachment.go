package confluent

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GeneratePrivateLinkAttachment(tfResourceName, displayName, region, environmentName string) *hclwrite.Block {
	privateLinkAttachmentBlock := hclwrite.NewBlock("resource", []string{"confluent_private_link_attachment", tfResourceName})
	privateLinkAttachmentBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))
	privateLinkAttachmentBlock.Body().SetAttributeValue("cloud", cty.StringVal("AWS"))
	privateLinkAttachmentBlock.Body().SetAttributeValue("region", cty.StringVal(region))
	privateLinkAttachmentBlock.Body().AppendNewline()

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(fmt.Sprintf("confluent_environment.%s.id", environmentName)))
	privateLinkAttachmentBlock.Body().AppendBlock(environmentBlock)
	privateLinkAttachmentBlock.Body().AppendNewline()

	return privateLinkAttachmentBlock
}

func GeneratePrivateLinkAttachmentConnection(tfResourceName, displayName, environmentName, vpcEndpointName, privateLinkAttachmentName string) *hclwrite.Block {
	privateLinkAttachmentConnectionBlock := hclwrite.NewBlock("resource", []string{"confluent_private_link_attachment_connection", tfResourceName})
	privateLinkAttachmentConnectionBlock.Body().SetAttributeValue("display_name", cty.StringVal(displayName))
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	environmentBlock := hclwrite.NewBlock("environment", nil)
	environmentBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(fmt.Sprintf("confluent_environment.%s.id", environmentName)))
	privateLinkAttachmentConnectionBlock.Body().AppendBlock(environmentBlock)
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	awsBlock := hclwrite.NewBlock("aws", nil)
	awsBlock.Body().SetAttributeRaw("vpc_endpoint_id", utils.TokensForResourceReference(fmt.Sprintf("aws_vpc_endpoint.%s.id", vpcEndpointName)))
	privateLinkAttachmentConnectionBlock.Body().AppendBlock(awsBlock)
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	privateLinkAttachment := hclwrite.NewBlock("private_link_attachment", nil)
	privateLinkAttachment.Body().SetAttributeRaw("id", utils.TokensForResourceReference(fmt.Sprintf("confluent_private_link_attachment.%s.id", privateLinkAttachmentName)))
	privateLinkAttachmentConnectionBlock.Body().AppendBlock(privateLinkAttachment)
	privateLinkAttachmentConnectionBlock.Body().AppendNewline()

	return privateLinkAttachmentConnectionBlock
}
