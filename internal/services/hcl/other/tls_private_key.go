package other

import (
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateTLSPrivateKeyResource(tfResourceName, algorithm string, rsaBits int) *hclwrite.Block {
	privateKeyBlock := hclwrite.NewBlock("resource", []string{"tls_private_key", tfResourceName})
	privateKeyBlock.Body().SetAttributeValue("algorithm", cty.StringVal(algorithm))
	privateKeyBlock.Body().SetAttributeValue("rsa_bits", cty.NumberIntVal(int64(rsaBits)))
	return privateKeyBlock
}
