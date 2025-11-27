package hcl

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/hcl/aws"
	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
	"github.com/confluentinc/kcp/internal/services/hcl/modules"
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
	SubnetName        string
}

type TargetInfraHCLService struct {
	ResourceNames TerraformResourceNames
}

//
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
		SubnetName:        "cflt_private_link_subnet",
	}
}

func NewTargetInfraHCLService() *TargetInfraHCLService {
	return &TargetInfraHCLService{
		ResourceNames: NewTerraformResourceNames(),
	}
}

func (ti *TargetInfraHCLService) GenerateTerraformFiles(request types.TargetClusterWizardRequest) types.MigrationInfraTerraformProject {
	requiredModules := []types.MigrationInfraTerraformModule{
		{
			Name:        "confluent_cloud",
			MainTf:      ti.generateConfluentCloudModuleMainTf(request),
			VariablesTf: ti.generateConfluentCloudModuleVariablesTf(request),
			OutputsTf:   ti.generateConfluentCloudModuleOutputsTf(request),
			VersionsTf:  ti.generateConfluentCloudModuleVersionsTf(),
		},
	}

	if request.NeedsPrivateLink {
		requiredModules = append(requiredModules, types.MigrationInfraTerraformModule{
			Name:        "private_link",
			MainTf:      ti.generatePrivateLinkModuleMainTf(request),
			VariablesTf: ti.generatePrivateLinkModuleVariablesTf(request),
			VersionsTf:  ti.generatePrivateLinkModuleVersionsTf(),
		})
	}

	return types.MigrationInfraTerraformProject{
		MainTf:           ti.generateRootMainTf(request),
		ProvidersTf:      ti.generateRootProvidersTf(),
		VariablesTf:      ti.generateVariablesTf(modules.GetTargetClusterModuleVariableDefinitions(request)),
		InputsAutoTfvars: ti.generateInputsAutoTfvars(request),
		Modules:          requiredModules,
	}
}

// ============================================================================
// Root-Level Generation
// ============================================================================

func (ti *TargetInfraHCLService) generateRootMainTf(request types.TargetClusterWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	confluentCloudBlock := rootBody.AppendNewBlock("module", []string{"confluent_cloud"})
	confluentCloudBody := confluentCloudBlock.Body()

	confluentCloudBody.SetAttributeValue("source", cty.StringVal("./modules/confluent_cloud"))
	confluentCloudBody.AppendNewline()

	confluentCloudBody.SetAttributeRaw("providers", utils.TokensForMap(map[string]hclwrite.Tokens{"confluent": utils.TokensForResourceReference("confluent")}))
	confluentCloudBody.AppendNewline()

	confluentCloudVars := modules.GetConfluentCloudVariables()
	for _, varDef := range confluentCloudVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		confluentCloudBody.SetAttributeRaw(varDef.Name, utils.TokensForVarReference(varDef.Name))
	}

	if request.NeedsPrivateLink {
		rootBody.AppendNewline()
		privateLinkBlock := rootBody.AppendNewBlock("module", []string{"private_link"})
		privateLinkBody := privateLinkBlock.Body()
		privateLinkBody.SetAttributeValue("source", cty.StringVal("./modules/private_link"))
		privateLinkBody.AppendNewline()

		privateLinkBody.SetAttributeRaw("providers", utils.TokensForMap(map[string]hclwrite.Tokens{"aws": utils.TokensForResourceReference("aws"), "confluent": utils.TokensForResourceReference("confluent")}))
		privateLinkBody.AppendNewline()

		privateLinkVars := modules.GetTargetClusterPrivateLinkVariables()
		for _, varDef := range privateLinkVars {
			if varDef.Condition != nil && !varDef.Condition(request) {
				continue
			}

			if varDef.ValueExtractor == nil && varDef.Name == "environment_id" {
				privateLinkBody.SetAttributeRaw(varDef.Name, utils.TokensForModuleOutput("confluent_cloud", "environment_id"))
			} else {
				privateLinkBody.SetAttributeRaw(varDef.Name, utils.TokensForVarReference(varDef.Name))
			}
		}
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

func (ti *TargetInfraHCLService) generateRootProvidersTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(confluent.GenerateRequiredProviderTokens())
	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateProviderBlock())
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateProviderBlockWithVar())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (ti *TargetInfraHCLService) generateVariablesTf(tfVariables []types.TerraformVariable) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	varSeenVariables := make(map[string]bool)

	for _, v := range tfVariables {
		if varSeenVariables[v.Name] {
			continue
		}
		varSeenVariables[v.Name] = true

		variableBlock := rootBody.AppendNewBlock("variable", []string{v.Name})
		variableBody := variableBlock.Body()
		variableBody.SetAttributeRaw("type", utils.TokensForResourceReference(v.Type))

		if v.Description != "" {
			variableBody.SetAttributeValue("description", cty.StringVal(v.Description))
		}

		if v.Sensitive {
			variableBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

func (ti *TargetInfraHCLService) generateInputsAutoTfvars(request types.TargetClusterWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	values := modules.GetTargetClusterModuleVariableValues(request)

	for varName, value := range values {
		varSeenVariables := make(map[string]bool)
		if varSeenVariables[varName] {
			continue
		}
		varSeenVariables[varName] = true

		switch v := value.(type) {
		case string:
			rootBody.SetAttributeValue(varName, cty.StringVal(v))
		case []string:
			ctyValues := make([]cty.Value, len(v))
			for i, s := range v {
				ctyValues[i] = cty.StringVal(s)
			}
			rootBody.SetAttributeValue(varName, cty.ListVal(ctyValues))
		case bool:
			rootBody.SetAttributeValue(varName, cty.BoolVal(v))
		case int:
			rootBody.SetAttributeValue(varName, cty.NumberIntVal(int64(v)))
		}
	}

	return string(f.Bytes())
}

func (ti *TargetInfraHCLService) generateOutputsTf(tfOutputs []types.TerraformOutput) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	for _, output := range tfOutputs {
		outputBlock := rootBody.AppendNewBlock("output", []string{output.Name})
		outputBody := outputBlock.Body()
		outputBody.SetAttributeRaw("value", utils.TokensForResourceReference(output.Value))

		if output.Description != "" {
			outputBody.SetAttributeValue("description", cty.StringVal(output.Description))
		}
		outputBody.SetAttributeValue("sensitive", cty.BoolVal(output.Sensitive))
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

// ============================================================================
// Confluent Cloud Module
// ============================================================================

func (ti *TargetInfraHCLService) generateConfluentCloudModuleMainTf(request types.TargetClusterWizardRequest) string {
	envVarName := modules.GetModuleVariableName("confluent_cloud", "environment_name")
	envIdVarName := modules.GetModuleVariableName("confluent_cloud", "environment_id")
	clusterVarName := modules.GetModuleVariableName("confluent_cloud", "cluster_name")
	regionVarName := modules.GetModuleVariableName("confluent_cloud", "aws_region")

	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Add environment (create or use data source if user states an environment already exists).
	if request.NeedsEnvironment {
		rootBody.AppendBlock(confluent.GenerateEnvironmentResource(ti.ResourceNames.Environment, envVarName))
		rootBody.AppendNewline()
	} else {
		rootBody.AppendBlock(confluent.GenerateEnvironmentDataSource(ti.ResourceNames.Environment, envIdVarName))
		rootBody.AppendNewline()
	}

	envIdRef := confluent.GetEnvironmentReference(request.NeedsEnvironment, ti.ResourceNames.Environment)

	// Add Kafka cluster (create or use data source if user states a cluster already exists).
	if request.NeedsCluster || request.NeedsEnvironment {
		rootBody.AppendBlock(confluent.GenerateKafkaClusterResource(ti.ResourceNames.Cluster, clusterVarName, request.ClusterType, regionVarName, envIdRef))
		rootBody.AppendNewline()
	}

	rootBody.AppendBlock(confluent.GenerateSchemaRegistryDataSource(
		ti.ResourceNames.SchemaRegistry,
		envIdRef,
		fmt.Sprintf("confluent_api_key.%s", ti.ResourceNames.KafkaAPIKey),
	))
	rootBody.AppendNewline()

	description := fmt.Sprintf("Service account to manage the %s environment.", envVarName)
	serviceAccountName := fmt.Sprintf("app-manager-%s", request.ClusterName[0:6])
	rootBody.AppendBlock(confluent.GenerateServiceAccount(ti.ResourceNames.ServiceAccount, serviceAccountName, description))
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

	envResourceName := confluent.GetEnvironmentResourceName(request.NeedsEnvironment, ti.ResourceNames.Environment)
	rootBody.AppendBlock(confluent.GenerateRoleBinding(
		ti.ResourceNames.DataStewardRoleBinding,
		fmt.Sprintf("User:${%s}", serviceAccountRef),
		"DataSteward",
		utils.TokensForResourceReference(envResourceName),
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateSchemaRegistryAPIKey(
		ti.ResourceNames.SchemaRegistryAPIKey,
		envVarName,
		fmt.Sprintf("confluent_service_account.%s.id", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("confluent_service_account.%s.api_version", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("confluent_service_account.%s.kind", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("data.confluent_schema_registry_cluster.%s.id", ti.ResourceNames.SchemaRegistry),
		fmt.Sprintf("data.confluent_schema_registry_cluster.%s.api_version", ti.ResourceNames.SchemaRegistry),
		fmt.Sprintf("data.confluent_schema_registry_cluster.%s.kind", ti.ResourceNames.SchemaRegistry),
		envIdRef,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateKafkaAPIKey(
		ti.ResourceNames.KafkaAPIKey,
		envVarName,
		fmt.Sprintf("confluent_service_account.%s.id", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("confluent_service_account.%s.api_version", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("confluent_service_account.%s.kind", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("confluent_kafka_cluster.%s.id", ti.ResourceNames.Cluster),
		fmt.Sprintf("confluent_kafka_cluster.%s.api_version", ti.ResourceNames.Cluster),
		fmt.Sprintf("confluent_kafka_cluster.%s.kind", ti.ResourceNames.Cluster),
		envIdRef,
		fmt.Sprintf("confluent_role_binding.%s", ti.ResourceNames.KafkaClusterAdminRoleBinding),
	))

	return string(f.Bytes())
}

func (ti *TargetInfraHCLService) generateConfluentCloudModuleVariablesTf(request types.TargetClusterWizardRequest) string {
	return ti.generateVariablesTf(modules.GetConfluentCloudVariableDefinitions(request))
}

func (ti *TargetInfraHCLService) generateConfluentCloudModuleOutputsTf(request types.TargetClusterWizardRequest) string {
	outputs := modules.GetConfluentCloudModuleOutputDefinitions(request, ti.ResourceNames.Environment)
	return ti.generateOutputsTf(outputs)
}

func (ti *TargetInfraHCLService) generateConfluentCloudModuleVersionsTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(confluent.GenerateRequiredProviderTokens())

	return string(f.Bytes())
}

// ============================================================================
// Private Link Module
// ============================================================================

func (ti *TargetInfraHCLService) generatePrivateLinkModuleMainTf(request types.TargetClusterWizardRequest) string {
	regionVarName := modules.GetModuleVariableName("provider_variables", "aws_region")
	vpcIdVarName := modules.GetModuleVariableName("private_link_target_cluster", "vpc_id")
	subnetCidrRangesVarName := modules.GetModuleVariableName("private_link_target_cluster", "subnet_cidr_ranges")
	environmentIdVarName := modules.GetModuleVariableName("private_link_target_cluster", "environment_id")

	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(confluent.GeneratePrivateLinkAttachmentResource(
		ti.ResourceNames.PrivateLinkAttachment,
		"kcp_private_link_attachment",
		regionVarName,
		environmentIdVarName,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateAvailabilityZonesDataSource(ti.ResourceNames.AvailabilityZones))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSecurityGroup(ti.ResourceNames.SecurityGroup, []int{80, 443, 9092}, []int{0}, vpcIdVarName))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSubnetResourceWithCount(
		ti.ResourceNames.SubnetName,
		subnetCidrRangesVarName,
		fmt.Sprintf("data.aws_availability_zones.%s", ti.ResourceNames.AvailabilityZones),
		vpcIdVarName,
	))

	rootBody.AppendBlock(aws.GenerateVpcEndpointResource(
		ti.ResourceNames.VpcEndpoint,
		vpcIdVarName,
		fmt.Sprintf("confluent_private_link_attachment.%s.aws[0].vpc_endpoint_service_name", ti.ResourceNames.PrivateLinkAttachment),
		fmt.Sprintf("aws_security_group.%s.id", ti.ResourceNames.SecurityGroup),
		fmt.Sprintf("aws_subnet.%s[*].id", ti.ResourceNames.SubnetName),
		[]string{fmt.Sprintf("confluent_private_link_attachment.%s", ti.ResourceNames.PrivateLinkAttachment)},
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GeneratePrivateLinkAttachmentConnectionResource(
		ti.ResourceNames.PrivateLinkAttachmentConnection,
		request.ClusterName+"_private_link_attachment_connection",
		environmentIdVarName,
		fmt.Sprintf("aws_vpc_endpoint.%s.id", ti.ResourceNames.VpcEndpoint),
		fmt.Sprintf("confluent_private_link_attachment.%s.id", ti.ResourceNames.PrivateLinkAttachment),
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRoute53ZoneResource(
		ti.ResourceNames.Route53Zone,
		vpcIdVarName,
		fmt.Sprintf("confluent_private_link_attachment.%s.dns_domain", ti.ResourceNames.PrivateLinkAttachment),
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRoute53RecordResource(
		ti.ResourceNames.Route53Record,
		fmt.Sprintf("aws_route53_zone.%s.zone_id", ti.ResourceNames.Route53Zone),
		"*", // TODO: we might want to consider using an actual record name versus the wildcard -- only concern is the impact of having a record name vs a wildcard.
		fmt.Sprintf("aws_vpc_endpoint.%s.dns_entry[0].dns_name", ti.ResourceNames.VpcEndpoint),
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (ti *TargetInfraHCLService) generatePrivateLinkModuleVariablesTf(request types.TargetClusterWizardRequest) string {
	return ti.generateVariablesTf(modules.GetTargetClusterPrivateLinkModuleVariableDefinitions(request))
}

func (ti *TargetInfraHCLService) generatePrivateLinkModuleVersionsTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(confluent.GenerateRequiredProviderTokens())
	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())

	return string(f.Bytes())
}
