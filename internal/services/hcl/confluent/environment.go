package confluent

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateEnvironmentResource creates a new Confluent environment resource
func GenerateEnvironmentResource(tfResourceName, envVarName string) *hclwrite.Block {
	environmentBlock := hclwrite.NewBlock("resource", []string{"confluent_environment", tfResourceName})
	environmentBlock.Body().SetAttributeRaw("display_name", utils.TokensForVarReference(envVarName))
	environmentBlock.Body().AppendNewline()

	streamGovernanceBlock := hclwrite.NewBlock("stream_governance", nil)
	streamGovernanceBlock.Body().SetAttributeValue("package", cty.StringVal("ADVANCED"))
	environmentBlock.Body().AppendBlock(streamGovernanceBlock)
	environmentBlock.Body().AppendNewline()

	utils.GenerateLifecycleBlock(environmentBlock, "prevent_destroy", true)

	return environmentBlock
}

// GenerateEnvironmentDataSource creates a data source for an existing environment
func GenerateEnvironmentDataSource(tfResourceName, envIdVarName string) *hclwrite.Block {
	environmentDataBlock := hclwrite.NewBlock("data", []string{"confluent_environment", tfResourceName})
	environmentDataBlock.Body().SetAttributeRaw("id", utils.TokensForVarReference(envIdVarName))
	return environmentDataBlock
}

// GetEnvironmentReference returns the reference string for the environment ID - either a resource or data source reference.
func GetEnvironmentReference(isNewEnv bool, envResourceName string) string {
	if isNewEnv {
		return fmt.Sprintf("confluent_environment.%s.id", envResourceName)
	}
	return fmt.Sprintf("data.confluent_environment.%s.id", envResourceName)
}

// GetEnvironmentResourceName returns the reference string for the environment resource name - either a resource or data source reference.
func GetEnvironmentResourceName(isNewEnv bool, envResourceName string) string {
	if isNewEnv {
		return fmt.Sprintf("confluent_environment.%s.resource_name", envResourceName)
	}
	return fmt.Sprintf("data.confluent_environment.%s.resource_name", envResourceName)
}
