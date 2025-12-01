package other

import (
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateRandomStringResource(tfResourceName string, length int, special, numeric, upper bool) *hclwrite.Block {
	randomStringBlock := hclwrite.NewBlock("resource", []string{"random_string", tfResourceName})
	randomStringBlock.Body().SetAttributeValue("length", cty.NumberIntVal(int64(length)))
	randomStringBlock.Body().SetAttributeValue("special", cty.BoolVal(special))
	randomStringBlock.Body().SetAttributeValue("numeric", cty.BoolVal(numeric))
	randomStringBlock.Body().SetAttributeValue("upper", cty.BoolVal(upper))
	return randomStringBlock
}

