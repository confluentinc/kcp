package hcl

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/hcl/aws"
	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type TerraformResourceNames struct {
	Environment                     string
	Cluster                         string
	SchemaRegistry                  string
	ServiceAccount                  string
	SchemaRegistryAPIKey            string
	KafkaAPIKey                     string
	PrivateLinkAttachment           string
	PrivateLinkAttachmentConnection string
	SubjectResourceOwnerRoleBinding string
	KafkaClusterAdminRoleBinding    string
	DataStewardRoleBinding          string

	AvailabilityZones string
	VpcEndpoint       string
	Route53Zone       string
	Route53Record     string
	SecurityGroup     string
	SubnetPrefix      string
}

type TargetInfraHCLService struct {
	ResourceNames TerraformResourceNames
}

func NewTerraformResourceNames() TerraformResourceNames {
	return TerraformResourceNames{
		// Confluent Resources
		Environment:                     "environment",
		Cluster:                         "cluster",
		SchemaRegistry:                  "schema_registry",
		ServiceAccount:                  "app-manager",
		SchemaRegistryAPIKey:            "env-manager-schema-registry-api-key",
		KafkaAPIKey:                     "app-manager-kafka-api-key",
		PrivateLinkAttachment:           "private_link_attachment",
		PrivateLinkAttachmentConnection: "private_link_attachment_connection",
		SubjectResourceOwnerRoleBinding: "subject-resource-owner",
		KafkaClusterAdminRoleBinding:    "app-manager-kafka-cluster-admin",
		DataStewardRoleBinding:          "app-manager-kafka-data-steward",

		// AWS Resources
		AvailabilityZones: "available",
		VpcEndpoint:       "cflt_private_link_vpc_endpoint",
		Route53Zone:       "cflt_private_link_zone",
		Route53Record:     "cflt_route_entries",
		SecurityGroup:     "cflt_private_link_sg",
		SubnetPrefix:      "cflt_private_link_subnet",
	}
}

func NewTargetInfraHCLService() *TargetInfraHCLService {
	return &TargetInfraHCLService{
		ResourceNames: NewTerraformResourceNames(),
	}
}

func (ti *TargetInfraHCLService) GenerateTerraformFiles(request types.TargetClusterWizardRequest) (types.TerraformFiles, error) {
	terraformFiles := types.TerraformFiles{
		MainTf:      ti.generateMainTf(request),
		ProvidersTf: ti.generateProvidersTf(request),
		VariablesTf: ti.generateVariablesTf(),
	}

	return terraformFiles, nil
}

// GenerateMainTf generates the main.tf file content using individual resource functions
func (ti *TargetInfraHCLService) generateMainTf(request types.TargetClusterWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// TODO: Retrieve the region from the statefile.
	request.Region = "eu-west-3"

	// Add environment (create or use data source if user states an environment already exists).
	if request.NeedsEnvironment {
		rootBody.AppendBlock(confluent.GenerateEnvironmentResource(ti.ResourceNames.Environment, request.EnvironmentName))
		rootBody.AppendNewline()
	} else {
		rootBody.AppendBlock(confluent.GenerateEnvironmentDataSource(ti.ResourceNames.Environment, request.EnvironmentId))
		rootBody.AppendNewline()
	}

	// Add Kafka cluster (create or use data source if user states a cluster already exists).
	if request.NeedsCluster || request.NeedsEnvironment {
		rootBody.AppendBlock(confluent.GenerateKafkaClusterResource(ti.ResourceNames.Cluster, request.ClusterName, request.ClusterType, request.Region, request.NeedsEnvironment))
		rootBody.AppendNewline()
	}

	rootBody.AppendBlock(confluent.GenerateSchemaRegistryDataSource(ti.ResourceNames.SchemaRegistry, request.NeedsEnvironment))
	rootBody.AppendNewline()

	description := fmt.Sprintf("Service account to manage the %s environment.", request.EnvironmentName)
	rootBody.AppendBlock(confluent.GenerateServiceAccount(ti.ResourceNames.ServiceAccount, "app-manager", description))
	rootBody.AppendNewline()

	serviceAccountRef := fmt.Sprintf("confluent_service_account.%s.id", ti.ResourceNames.ServiceAccount)
	rootBody.AppendBlock(confluent.GenerateRoleBinding(
		ti.ResourceNames.SubjectResourceOwnerRoleBinding,
		fmt.Sprintf("User:${%s}", serviceAccountRef),
		"ResourceOwner",
		utils.TokensForStringTemplate(fmt.Sprintf("${data.confluent_schema_registry_cluster.%s.resource_name}/subject=*", ti.ResourceNames.SchemaRegistry)),
	))
	rootBody.AppendNewline()

	clusterRef := fmt.Sprintf("confluent_kafka_cluster.%s.rbac_crn", ti.ResourceNames.Cluster)
	rootBody.AppendBlock(confluent.GenerateRoleBinding(
		ti.ResourceNames.KafkaClusterAdminRoleBinding,
		fmt.Sprintf("User:${%s}", serviceAccountRef),
		"CloudClusterAdmin",
		utils.TokensForResourceReference(clusterRef),
	))
	rootBody.AppendNewline()

	envResourceName := confluent.GetEnvironmentResourceName(request.NeedsEnvironment)
	rootBody.AppendBlock(confluent.GenerateRoleBinding(
		ti.ResourceNames.DataStewardRoleBinding,
		fmt.Sprintf("User:${%s}", serviceAccountRef),
		"DataSteward",
		utils.TokensForResourceReference(envResourceName),
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateSchemaRegistryAPIKey(
		ti.ResourceNames.SchemaRegistryAPIKey,
		request.EnvironmentName,
		ti.ResourceNames.ServiceAccount,
		ti.ResourceNames.SchemaRegistry,
		request.NeedsEnvironment,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateKafkaAPIKey(
		ti.ResourceNames.KafkaAPIKey,
		request.EnvironmentName,
		ti.ResourceNames.ServiceAccount,
		ti.ResourceNames.Cluster,
		ti.ResourceNames.KafkaClusterAdminRoleBinding,
		request.NeedsEnvironment,
	))
	rootBody.AppendNewline()

	if request.NeedsPrivateLink {
		rootBody.AppendBlock(confluent.GeneratePrivateLinkAttachment(
			ti.ResourceNames.PrivateLinkAttachment,
			request.ClusterName+"_private_link_attachment",
			request.Region,
			ti.ResourceNames.Environment,
		))
		rootBody.AppendNewline()

		rootBody.AppendBlock(confluent.GeneratePrivateLinkAttachmentConnection(
			ti.ResourceNames.PrivateLinkAttachmentConnection,
			request.ClusterName+"_private_link_attachment_connection",
			ti.ResourceNames.Environment,
			ti.ResourceNames.VpcEndpoint,
			ti.ResourceNames.PrivateLinkAttachment,
		))
		rootBody.AppendNewline()

		rootBody.AppendBlock(aws.GenerateAvailabilityZonesDataSource(ti.ResourceNames.AvailabilityZones))
		rootBody.AppendNewline()

		rootBody.AppendBlock(aws.GenerateVpcEndpoint(
			ti.ResourceNames.VpcEndpoint,
			request.VpcId,
			ti.ResourceNames.PrivateLinkAttachment,
			ti.ResourceNames.SecurityGroup,
			ti.ResourceNames.SubnetPrefix,
			len(request.SubnetCidrRanges),
		)) // TODO: should we retrieve vpc id from statefile instead of asking user to pass it in?
		rootBody.AppendNewline()

		for i, subnetCidrRange := range request.SubnetCidrRanges {
			rootBody.AppendBlock(aws.GenerateSubnets(
				ti.ResourceNames.SubnetPrefix,
				request.VpcId,
				subnetCidrRange,
				ti.ResourceNames.AvailabilityZones,
				i,
			))

			if i < len(request.SubnetCidrRanges) {
				rootBody.AppendNewline()
			}
		}

		rootBody.AppendBlock(aws.GenerateRoute53Zone(
			ti.ResourceNames.Route53Zone,
			request.VpcId,
			ti.ResourceNames.PrivateLinkAttachment,
		)) // TODO: should we retrieve vpc id from statefile instead of asking user to pass it in?
		rootBody.AppendNewline()

		rootBody.AppendBlock(aws.GenerateRoute53Record(
			ti.ResourceNames.Route53Record,
			ti.ResourceNames.Route53Zone,
			ti.ResourceNames.VpcEndpoint,
		))
		rootBody.AppendNewline()

		rootBody.AppendBlock(aws.GenerateSecurityGroup(ti.ResourceNames.SecurityGroup, request.VpcId, []int{80, 443, 9092}, []int{0}))
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

// GenerateProvidersTf generates the providers.tf file content
func (ti *TargetInfraHCLService) generateProvidersTf(request types.TargetClusterWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// TODO: Retrieve the region from the statefile.
	request.Region = "eu-west-3"

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeValue("confluent", cty.ObjectVal(map[string]cty.Value{
		"source":  cty.StringVal("confluentinc/confluent"),
		"version": cty.StringVal("2.50.0"),
	}))

	if request.NeedsPrivateLink {
		requiredProvidersBody.SetAttributeValue("aws", cty.ObjectVal(map[string]cty.Value{
			"source":  cty.StringVal("hashicorp/aws"),
			"version": cty.StringVal("6.18.0"),
		}))
	}

	rootBody.AppendNewline()

	providerBlock := rootBody.AppendNewBlock("provider", []string{"confluent"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeRaw("cloud_api_key", utils.TokensForResourceReference("var.confluent_cloud_api_key"))
	providerBody.SetAttributeRaw("cloud_api_secret", utils.TokensForResourceReference("var.confluent_cloud_api_secret"))

	if request.NeedsPrivateLink {
		rootBody.AppendNewline()
		awsProviderBlock := rootBody.AppendNewBlock("provider", []string{"aws"})
		awsProviderBody := awsProviderBlock.Body()
		awsProviderBody.SetAttributeRaw("region", utils.TokensForStringTemplate(request.Region))
	}

	return string(f.Bytes())
}

// GenerateVariablesTf generates the variables.tf file content
func (ti *TargetInfraHCLService) generateVariablesTf() string {
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
