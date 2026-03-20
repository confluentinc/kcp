package hcl

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// SetVarRef sets an attribute to reference a Terraform variable: var.<varName>
func SetVarRef(body *hclwrite.Body, attrName, varName string) {
	body.SetAttributeRaw(attrName, utils.TokensForVarReference(varName))
}

// SetModuleRef sets an attribute to reference a module output: module.<moduleName>.<outputName>
func SetModuleRef(body *hclwrite.Body, attrName, moduleName, outputName string) {
	body.SetAttributeRaw(attrName, utils.TokensForModuleOutput(moduleName, outputName))
}

// SetResourceRef sets an attribute to a resource reference expression (unquoted).
func SetResourceRef(body *hclwrite.Body, attrName, ref string) {
	body.SetAttributeRaw(attrName, utils.TokensForResourceReference(ref))
}

// SetStringTemplate sets an attribute to a quoted string template.
func SetStringTemplate(body *hclwrite.Body, attrName, template string) {
	body.SetAttributeRaw(attrName, utils.TokensForStringTemplate(template))
}
