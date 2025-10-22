package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateServiceAccount creates a service account resource
func GenerateServiceAccount(name, description string) *hclwrite.Block {
	serviceAccountBlock := hclwrite.NewBlock("resource", []string{"confluent_service_account", name})
	serviceAccountBlock.Body().SetAttributeValue("display_name", cty.StringVal(name))
	serviceAccountBlock.Body().SetAttributeValue("description", cty.StringVal(description))
	return serviceAccountBlock
}

// GenerateRoleBinding creates a role binding resource
func GenerateRoleBinding(name, principal, roleName string, crnPattern hclwrite.Tokens) *hclwrite.Block {
	roleBindingBlock := hclwrite.NewBlock("resource", []string{"confluent_role_binding", name})
	roleBindingBlock.Body().SetAttributeRaw("principal", utils.TokensForStringTemplate(principal))
	roleBindingBlock.Body().SetAttributeValue("role_name", cty.StringVal(roleName))
	roleBindingBlock.Body().SetAttributeRaw("crn_pattern", crnPattern)
	return roleBindingBlock
}
