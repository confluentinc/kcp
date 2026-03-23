package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateEndpointDataSource generates a confluent_endpoint data source to look up
// gateway-specific private endpoints for a Confluent Cloud service.
func GenerateEndpointDataSource(tfResourceName, environmentIdRef, service, resourceRef, cloud, regionVarName, dependsOnModule string) *hclwrite.Block {
	block := hclwrite.NewBlock("data", []string{"confluent_endpoint", tfResourceName})

	filterBlock := block.Body().AppendNewBlock("filter", nil)
	filterBody := filterBlock.Body()

	envBlock := filterBody.AppendNewBlock("environment", nil)
	envBlock.Body().SetAttributeRaw("id", utils.TokensForResourceReference(environmentIdRef))

	filterBody.SetAttributeValue("service", cty.StringVal(service))
	filterBody.SetAttributeRaw("resource", utils.TokensForResourceReference(resourceRef))
	filterBody.SetAttributeValue("cloud", cty.StringVal(cloud))
	filterBody.SetAttributeRaw("region", utils.TokensForVarReference(regionVarName))
	filterBody.SetAttributeValue("is_private", cty.BoolVal(true))

	block.Body().AppendNewline()
	block.Body().SetAttributeRaw("depends_on", utils.TokensForList([]string{dependsOnModule}))

	return block
}
