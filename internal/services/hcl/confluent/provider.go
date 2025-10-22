package confluent

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

/*
    confluent = {
      source  = "confluentinc/confluent"
      version = "2.23.0"
    }
*/
func GenerateRequiredProviderBlock() *hclwrite.Block {

	confluentBlock := hclwrite.NewBlock("confluent", nil)
	confluentBlock.Body().SetAttributeValue("source", cty.StringVal("confluentinc/confluent"))
	confluentBlock.Body().SetAttributeValue("version", cty.StringVal("2.50.0"))
	
	return confluentBlock
}

func GenerateProviderBlock() *hclwrite.Block {
	providerBlock := hclwrite.NewBlock("provider", []string{"confluent"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeRaw("cloud_api_key", utils.TokensForResourceReference("var.confluent_cloud_api_key"))
	providerBody.SetAttributeRaw("cloud_api_secret", utils.TokensForResourceReference("var.confluent_cloud_api_secret"))
	
	return providerBlock
}

