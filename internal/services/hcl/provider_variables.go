package hcl

import (
	"github.com/confluentinc/kcp/internal/types"
)

func GetProviderVariables() []ModuleVariableDefinition {
	return []ModuleVariableDefinition{
		{
			Name: "confluent_cloud_api_key",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_api_key",
				Description: "Confluent Cloud API Key",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return ""
			},
			Condition: nil,
		},
		{
			Name: "confluent_cloud_api_secret",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_api_secret",
				Description: "Confluent Cloud API Secret",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return ""
			},
			Condition: nil,
		},
		{
			Name: "aws_region",
			Definition: types.TerraformVariable{
				Name:        "aws_region",
				Description: "The AWS region",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.MskRegion
			},
			Condition: nil,
		},
	}
}
