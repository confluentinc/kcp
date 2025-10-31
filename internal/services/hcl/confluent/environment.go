package confluent

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateEnvironmentResource creates a new Confluent environment resource
func GenerateEnvironmentResource(tfResourceName, name string) *hclwrite.Block {
	environmentBlock := hclwrite.NewBlock("resource", []string{"confluent_environment", tfResourceName})
	environmentBlock.Body().SetAttributeValue("display_name", cty.StringVal(name))
	environmentBlock.Body().AppendNewline()

	streamGovernanceBlock := hclwrite.NewBlock("stream_governance", nil)
	streamGovernanceBlock.Body().SetAttributeValue("package", cty.StringVal("ADVANCED"))
	environmentBlock.Body().AppendBlock(streamGovernanceBlock)

	return environmentBlock
}

// GenerateEnvironmentDataSource creates a data source for an existing environment
func GenerateEnvironmentDataSource(tfResourceName, id string) *hclwrite.Block {
	environmentDataBlock := hclwrite.NewBlock("data", []string{"confluent_environment", tfResourceName})
	environmentDataBlock.Body().SetAttributeValue("id", cty.StringVal(id))
	return environmentDataBlock
}

// GetEnvironmentReference returns the reference string for the environment ID - either a resource or data source reference.
func GetEnvironmentReference(isNewEnv bool, environmentResourceName string) string {
	if isNewEnv {
		return fmt.Sprintf("confluent_environment.%s.id", environmentResourceName)
	}
	return fmt.Sprintf("data.confluent_environment.%s.id", environmentResourceName)
}

// GetEnvironmentResourceName returns the reference string for the environment resource name - either a resource or data source reference.
func GetEnvironmentResourceName(isNewEnv bool, environmentResourceName string) string {
	if isNewEnv {
		return fmt.Sprintf("confluent_environment.%s.resource_name", environmentResourceName)
	}
	return fmt.Sprintf("data.confluent_environment.%s.resource_name", environmentResourceName)
}
