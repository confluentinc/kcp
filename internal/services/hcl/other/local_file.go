package other

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateLocalFileResource(tfResourceName, contentReference, filename, filePermission string) *hclwrite.Block {
	localFileBlock := hclwrite.NewBlock("resource", []string{"local_file", tfResourceName})
	localFileBlock.Body().SetAttributeRaw("content", utils.TokensForResourceReference(contentReference))
	localFileBlock.Body().SetAttributeValue("filename", cty.StringVal(filename))
	localFileBlock.Body().SetAttributeValue("file_permission", cty.StringVal(filePermission))
	return localFileBlock
}
