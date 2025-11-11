package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateKeyPairResource(tfResourceName, keyName, publicKey string) *hclwrite.Block {
	keyPairBlock := hclwrite.NewBlock("resource", []string{"aws_key_pair", tfResourceName})
	keyPairBlock.Body().SetAttributeValue("key_name", cty.StringVal(keyName))
	keyPairBlock.Body().SetAttributeRaw("public_key", utils.TokensForResourceReference(publicKey))
	return keyPairBlock
}
