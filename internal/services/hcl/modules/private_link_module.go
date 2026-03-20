package modules

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
)

func GetTargetClusterPrivateLinkVariables() []ModuleVariable[types.TargetClusterWizardRequest] {
	return []ModuleVariable[types.TargetClusterWizardRequest]{
		{
			Name: "aws_region",
			Definition: types.TerraformVariable{
				Name:        "aws_region",
				Description: "The AWS region of the VPC that the private link connection is established in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.AwsRegion
			},
			Condition: nil,
		},
		{
			Name: "vpc_id",
			Definition: types.TerraformVariable{
				Name:        "vpc_id",
				Description: "The ID of the VPC that the private link connection is established in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.VpcId
			},
			Condition: nil,
		},
		{
			Name: "subnet_cidr_ranges",
			Definition: types.TerraformVariable{
				Name:        "subnet_cidr_ranges",
				Description: "The CIDR ranges of the subnets that the private link connection is established in.",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.SubnetCidrRanges
			},
			Condition: nil,
		},
		{
			Name: "environment_id",
			Definition: types.TerraformVariable{
				Name:        "environment_id",
				Description: "The ID of the environment that the private link connection is established in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor:   nil,
			FromModuleOutput: "confluent_cloud",
			Condition:        nil,
		},
		{
			Name: "network_id",
			Definition: types.TerraformVariable{
				Name:        "network_id",
				Description: "The ID of the Confluent Cloud network (for dedicated cluster private link).",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor:   nil,
			FromModuleOutput: "confluent_cloud",
			Condition: func(request types.TargetClusterWizardRequest) bool {
				return request.ClusterType == "dedicated"
			},
		},
		{
			Name: "network_dns_domain",
			Definition: types.TerraformVariable{
				Name:        "network_dns_domain",
				Description: "The DNS domain of the Confluent Cloud network (for dedicated cluster private link).",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor:   nil,
			FromModuleOutput: "confluent_cloud",
			Condition: func(request types.TargetClusterWizardRequest) bool {
				return request.ClusterType == "dedicated"
			},
		},
		{
			Name: "network_private_link_endpoint_service",
			Definition: types.TerraformVariable{
				Name:        "network_private_link_endpoint_service",
				Description: "The AWS VPC endpoint service name for the Confluent Cloud network (for dedicated cluster private link).",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor:   nil,
			FromModuleOutput: "confluent_cloud",
			Condition: func(request types.TargetClusterWizardRequest) bool {
				return request.ClusterType == "dedicated"
			},
		},
		{
			Name: "network_zones",
			Definition: types.TerraformVariable{
				Name:        "network_zones",
				Description: "Availability zone IDs supported by the Confluent Cloud network (for dedicated cluster private link).",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor:   nil,
			FromModuleOutput: "confluent_cloud",
			Condition: func(request types.TargetClusterWizardRequest) bool {
				return request.ClusterType == "dedicated"
			},
		},
	}
}

func GetPrivateLinkModuleOutputDefinitions(vpcEndpointResourceName string) []types.TerraformOutput {
	return []types.TerraformOutput{
		{
			Name:        "vpc_endpoint_id",
			Description: "ID of the AWS VPC Endpoint for the Private Link connection",
			Value:       fmt.Sprintf("aws_vpc_endpoint.%s.id", vpcEndpointResourceName),
		},
	}
}

func GetTargetClusterPrivateLinkModuleVariableDefinitions(request types.TargetClusterWizardRequest) []types.TerraformVariable {
	return ExtractModuleVariableDefinitions(GetTargetClusterPrivateLinkVariables(), request)
}
