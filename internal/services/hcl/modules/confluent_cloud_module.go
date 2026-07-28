package modules

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/hcl/hclrequests"
	"github.com/confluentinc/kcp/internal/services/hcl/hcltypes"
)

func GetConfluentCloudVariables() []ModuleVariable[hclrequests.TargetClusterWizardRequest] {
	return []ModuleVariable[hclrequests.TargetClusterWizardRequest]{
		{
			Name: "aws_region",
			Definition: hcltypes.TerraformVariable{
				Name:        "aws_region",
				Description: "The AWS region in which the Confluent Cloud cluster is provisioned in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.TargetClusterWizardRequest) any {
				return request.AwsRegion
			},
			Condition: nil,
		},
		{
			Name: "environment_id",
			Definition: hcltypes.TerraformVariable{
				Name:        "environment_id",
				Description: "ID of the environment",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.TargetClusterWizardRequest) any {
				return request.EnvironmentId
			},
			Condition: func(request hclrequests.TargetClusterWizardRequest) bool {
				return !request.NeedsEnvironment
			},
		},
		{
			Name: "environment_name",
			Definition: hcltypes.TerraformVariable{
				Name:        "environment_name",
				Description: "Name of the environment",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.TargetClusterWizardRequest) any {
				return request.EnvironmentName
			},
			Condition: func(request hclrequests.TargetClusterWizardRequest) bool {
				return request.NeedsEnvironment
			},
		},
		{
			Name: "cluster_name",
			Definition: hcltypes.TerraformVariable{
				Name:        "cluster_name",
				Description: "Name of the cluster",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.TargetClusterWizardRequest) any {
				return request.ClusterName
			},
			Condition: func(request hclrequests.TargetClusterWizardRequest) bool {
				return request.NeedsEnvironment || request.NeedsCluster
			},
		},
		{
			Name: "cluster_type",
			Definition: hcltypes.TerraformVariable{
				Name:        "cluster_type",
				Description: "Type of the cluster",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.TargetClusterWizardRequest) any {
				return request.ClusterType
			},
			Condition: func(request hclrequests.TargetClusterWizardRequest) bool {
				return request.NeedsEnvironment || request.NeedsCluster
			},
		},
		{
			Name: "cluster_availability",
			Definition: hcltypes.TerraformVariable{
				Name:        "cluster_availability",
				Description: "Cluster availability zone type (SINGLE_ZONE or MULTI_ZONE)",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.TargetClusterWizardRequest) any {
				return request.ClusterAvailability
			},
			Condition: func(request hclrequests.TargetClusterWizardRequest) bool {
				return (request.NeedsEnvironment || request.NeedsCluster) && request.ClusterType == "dedicated"
			},
		},
		{
			Name: "cluster_cku",
			Definition: hcltypes.TerraformVariable{
				Name:        "cluster_cku",
				Description: "Number of CKUs for dedicated clusters",
				Sensitive:   false,
				Type:        "number",
			},
			ValueExtractor: func(request hclrequests.TargetClusterWizardRequest) any {
				return request.ClusterCku
			},
			Condition: func(request hclrequests.TargetClusterWizardRequest) bool {
				return (request.NeedsEnvironment || request.NeedsCluster) && request.ClusterType == "dedicated"
			},
		},
	}
}

func GetConfluentCloudVariableDefinitions(request hclrequests.TargetClusterWizardRequest) []hcltypes.TerraformVariable {
	return ExtractModuleVariableDefinitions(GetConfluentCloudVariables(), request)
}

type ConfluentCloudOutputParams struct {
	EnvironmentName    string
	NetworkName        string
	ClusterName        string
	ServiceAccountName string
	KafkaAPIKeyName    string
}

func GetConfluentCloudModuleOutputDefinitions(request hclrequests.TargetClusterWizardRequest, params ConfluentCloudOutputParams) []hcltypes.TerraformOutput {
	var definitions []hcltypes.TerraformOutput

	var envIdValue string
	if request.NeedsEnvironment {
		envIdValue = fmt.Sprintf("confluent_environment.%s.id", params.EnvironmentName)
	} else {
		envIdValue = fmt.Sprintf("data.confluent_environment.%s.id", params.EnvironmentName)
	}

	definitions = append(definitions, hcltypes.TerraformOutput{
		Name:        "environment_id",
		Description: "ID of the environment",
		Sensitive:   false,
		Value:       envIdValue,
	})

	// Cluster outputs
	definitions = append(definitions,
		hcltypes.TerraformOutput{
			Name:        "cluster_id",
			Description: "ID of the Kafka cluster",
			Value:       fmt.Sprintf("confluent_kafka_cluster.%s.id", params.ClusterName),
		},
		hcltypes.TerraformOutput{
			Name:        "cluster_bootstrap_endpoint",
			Description: "Bootstrap endpoint of the Kafka cluster",
			Value:       fmt.Sprintf("confluent_kafka_cluster.%s.bootstrap_endpoint", params.ClusterName),
		},
		hcltypes.TerraformOutput{
			Name:        "cluster_rest_endpoint",
			Description: "REST endpoint of the Kafka cluster",
			Value:       fmt.Sprintf("confluent_kafka_cluster.%s.rest_endpoint", params.ClusterName),
		},
	)

	// Service account output
	definitions = append(definitions, hcltypes.TerraformOutput{
		Name:        "service_account_id",
		Description: "ID of the service account",
		Value:       fmt.Sprintf("confluent_service_account.%s.id", params.ServiceAccountName),
	})

	// Kafka API key outputs
	definitions = append(definitions,
		hcltypes.TerraformOutput{
			Name:        "kafka_api_key_id",
			Description: "ID of the Kafka API key",
			Value:       fmt.Sprintf("confluent_api_key.%s.id", params.KafkaAPIKeyName),
		},
		hcltypes.TerraformOutput{
			Name:        "kafka_api_key_secret",
			Description: "Secret of the Kafka API key",
			Sensitive:   true,
			Value:       fmt.Sprintf("confluent_api_key.%s.secret", params.KafkaAPIKeyName),
		},
	)

	// Network outputs (only for dedicated clusters with private link)
	if request.NeedsPrivateLink && request.ClusterType == "dedicated" {
		definitions = append(definitions,
			hcltypes.TerraformOutput{
				Name:        "network_id",
				Description: "ID of the Confluent Cloud network",
				Value:       fmt.Sprintf("confluent_network.%s.id", params.NetworkName),
			},
			hcltypes.TerraformOutput{
				Name:        "network_dns_domain",
				Description: "DNS domain of the Confluent Cloud network",
				Value:       fmt.Sprintf("confluent_network.%s.dns_domain", params.NetworkName),
			},
			hcltypes.TerraformOutput{
				Name:        "network_private_link_endpoint_service",
				Description: "AWS VPC endpoint service name for the Confluent Cloud network",
				Value:       fmt.Sprintf("confluent_network.%s.aws[0].private_link_endpoint_service", params.NetworkName),
			},
			hcltypes.TerraformOutput{
				Name:        "network_zones",
				Description: "Availability zone IDs supported by the Confluent Cloud network",
				Value:       fmt.Sprintf("confluent_network.%s.zones", params.NetworkName),
			},
		)
	}

	return definitions
}
