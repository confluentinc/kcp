package confluent

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type ConfluentCloudHCLService struct {
}

func NewConfluentCloudHCLService() *ConfluentCloudHCLService {
	return &ConfluentCloudHCLService{}
}

func (cc *ConfluentCloudHCLService) GenerateTerraformFiles(request types.WizardRequest) (types.TerraformFiles, error) {
	terraformFiles := types.TerraformFiles{
		MainTf:      cc.generateMainTf(request),
		ProvidersTf: cc.generateProvidersTf(),
		VariablesTf: cc.generateVariablesTf(),
	}

	return terraformFiles, nil
}

// GenerateMainTf generates the main.tf file content using individual resource functions
func (cc *ConfluentCloudHCLService) generateMainTf(request types.WizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Add environment (create or use data source)
	if request.NeedsEnvironment {
		rootBody.AppendBlock(generateEnvironmentResource(request.EnvironmentName))
		rootBody.AppendNewline()
	} else {
		rootBody.AppendBlock(generateEnvironmentDataSource(request.EnvironmentId))
		rootBody.AppendNewline()
	}

	// Add Kafka cluster (create if needed)
	if request.NeedsCluster || request.NeedsEnvironment {
		rootBody.AppendBlock(generateKafkaClusterResource(request.ClusterName, request.ClusterType, "us-east-1", request.NeedsEnvironment))
		rootBody.AppendNewline()
	}

	// Add Schema Registry data source
	rootBody.AppendBlock(generateSchemaRegistryDataSource(request.NeedsEnvironment))
	rootBody.AppendNewline()

	// Add Service Account
	description := fmt.Sprintf("Service account to manage the %s environment.", request.EnvironmentName)
	rootBody.AppendBlock(generateServiceAccount("app-manager", description))
	rootBody.AppendNewline()

	// Add Role Bindings
	rootBody.AppendBlock(generateRoleBinding(
		"subject-resource-owner",
		"User:${confluent_service_account.app-manager.id}",
		"ResourceOwner",
		utils.TokensForStringTemplate("${data.confluent_schema_registry_cluster.schema_registry.resource_name}/subject=*"),
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(generateRoleBinding(
		"app-manager-kafka-cluster-admin",
		"User:${confluent_service_account.app-manager.id}",
		"CloudClusterAdmin",
		utils.TokensForResourceReference("confluent_kafka_cluster.cluster.rbac_crn"),
	))
	rootBody.AppendNewline()

	envResourceName := getEnvironmentResourceName(request.NeedsEnvironment)
	rootBody.AppendBlock(generateRoleBinding(
		"app-manager-kafka-data-steward",
		"User:${confluent_service_account.app-manager.id}",
		"DataSteward",
		utils.TokensForResourceReference(envResourceName),
	))
	rootBody.AppendNewline()

	// Add Kafka ACLs
	rootBody.AppendBlock(generateKafkaACL(
		"app-manager-create-on-cluster",
		"CLUSTER",
		"kafka-cluster",
		"LITERAL",
		"User:${confluent_service_account.app-manager.id}",
		"CREATE",
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(generateKafkaACL(
		"app-manager-describe-on-cluster",
		"CLUSTER",
		"kafka-cluster",
		"LITERAL",
		"User:${confluent_service_account.app-manager.id}",
		"DESCRIBE",
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(generateKafkaACL(
		"app-manager-read-all-consumer-groups",
		"GROUP",
		"*",
		"PREFIXED",
		"User:${confluent_service_account.app-manager.id}",
		"READ",
	))
	rootBody.AppendNewline()

	// Add API Keys
	rootBody.AppendBlock(generateSchemaRegistryAPIKey(request.EnvironmentName, request.NeedsEnvironment))
	rootBody.AppendNewline()

	rootBody.AppendBlock(generateKafkaAPIKey(request.EnvironmentName, request.NeedsEnvironment))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// GenerateProvidersTf generates the providers.tf file content
func (cc *ConfluentCloudHCLService) generateProvidersTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Terraform block
	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	// Required providers block
	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	// Confluent provider
	requiredProvidersBody.SetAttributeValue("confluent", cty.ObjectVal(map[string]cty.Value{
		"source":  cty.StringVal("confluentinc/confluent"),
		"version": cty.StringVal("2.50.0"),
	}))

	rootBody.AppendNewline()

	// Provider block
	providerBlock := rootBody.AppendNewBlock("provider", []string{"confluent"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeRaw("cloud_api_key", utils.TokensForResourceReference("var.confluent_cloud_api_key"))
	providerBody.SetAttributeRaw("cloud_api_secret", utils.TokensForResourceReference("var.confluent_cloud_api_secret"))

	return string(f.Bytes())
}

// GenerateVariablesTf generates the variables.tf file content
func (cc *ConfluentCloudHCLService) generateVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Define base variables
	variables := []struct {
		name        string
		description string
		sensitive   bool
	}{
		{"confluent_cloud_api_key", "Confluent Cloud API Key", true},
		{"confluent_cloud_api_secret", "Confluent Cloud API Secret", true},
	}

	for _, v := range variables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.name})
		variableBody := variableBlock.Body()
		variableBody.SetAttributeRaw("type", utils.TokensForResourceReference("string"))
		if v.description != "" {
			variableBody.SetAttributeValue("description", cty.StringVal(v.description))
		}
		if v.sensitive {
			variableBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}
