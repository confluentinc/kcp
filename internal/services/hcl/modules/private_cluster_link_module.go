package modules

import (
	"github.com/confluentinc/kcp/internal/services/hcl/hclrequests"
	"github.com/confluentinc/kcp/internal/services/hcl/hcltypes"
)

func GetPrivateClusterLinkVariables() []ModuleVariable[hclrequests.MigrationWizardRequest] {
	return []ModuleVariable[hclrequests.MigrationWizardRequest]{
		{
			Name: "aws_region",
			Definition: hcltypes.TerraformVariable{
				Name:        "aws_region",
				Description: "AWS region of the source Kafka cluster that data will be migrated from.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.SourceRegion
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "aws_vpc_id",
			Definition: hcltypes.TerraformVariable{
				Name:        "aws_vpc_id",
				Description: "VPC ID of the source Kafka cluster that data will be migrated from.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.VpcId
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "aws_kafka_brokers",
			Definition: hcltypes.TerraformVariable{
				Name:        "aws_kafka_brokers",
				Description: "AWS Kafka brokers of the source Kafka cluster that data will be migrated from.",
				Sensitive:   false,
				Type:        "list(object({id=string,subnet_id=string,endpoints=list(object({host=string,port=number,ip=string}))}))",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.ExtOutboundBrokers
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "cc_env_id",
			Definition: hcltypes.TerraformVariable{
				Name:        "target_environment_id",
				Description: "Target environment ID where Confluent Cloud cluster is located.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.TargetEnvironmentId
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "cc_cluster_id",
			Definition: hcltypes.TerraformVariable{
				Name:        "target_cluster_id",
				Description: "Target cluster ID where data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.TargetClusterId
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
	}
}
