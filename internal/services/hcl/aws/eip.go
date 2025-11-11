package aws

import (
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateEIPResource(tfResourceName string) *hclwrite.Block {
	eipBlock := hclwrite.NewBlock("resource", []string{"aws_eip", tfResourceName})
	eipBlock.Body().SetAttributeValue("domain", cty.StringVal("vpc"))

	return eipBlock
}
