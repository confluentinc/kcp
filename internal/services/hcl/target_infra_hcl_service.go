package hcl

import (
	"fmt"
	"strings"

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
	Network                         string
	SchemaRegistry                  string
	ServiceAccount                  string
	SchemaRegistryAPIKey            string
	KafkaAPIKey                     string
	PrivateLinkAttachment           string
	PrivateLinkAttachmentConnection string
	PrivateLinkAccess               string
	IngressGateway                  string
	AccessPoint                     string
	SubjectResourceOwnerRoleBinding string
	KafkaClusterAdminRoleBinding    string
	DataStewardRoleBinding          string

	AvailabilityZones string
	CallerIdentity    string
	VpcEndpoint       string
	Route53Zone       string
	Route53Record     string
	SecurityGroup     string
	SubnetName        string
}

type TargetInfraHCLService struct {
	ResourceNames TerraformResourceNames
	// DeploymentID overrides the random deployment identifier in AWS provider tags.
	// When empty, a random 8-character string is generated.
	DeploymentID string
}

func NewTerraformResourceNames() TerraformResourceNames {
	return TerraformResourceNames{
		// Confluent Resources
		Environment:                     "environment",
		Cluster:                         "cluster",
		Network:                         "network",
		SchemaRegistry:                  "schema_registry",
		ServiceAccount:                  "app-manager",
		SchemaRegistryAPIKey:            "env-manager-schema-registry-api-key",
		KafkaAPIKey:                     "app-manager-kafka-api-key",
		PrivateLinkAttachment:           "private_link_attachment",
		PrivateLinkAttachmentConnection: "private_link_attachment_connection",
		PrivateLinkAccess:               "private_link_access",
		IngressGateway:                  "ingress_gateway",
		AccessPoint:                     "access_point",
		SubjectResourceOwnerRoleBinding: "subject-resource-owner",
		KafkaClusterAdminRoleBinding:    "app-manager-kafka-cluster-admin",
		DataStewardRoleBinding:          "app-manager-kafka-data-steward",

		// AWS Resources
		AvailabilityZones: "available",
		CallerIdentity:    "current",
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
			OutputsTf:   ti.generatePrivateLinkModuleOutputsTf(),
			VersionsTf:  ti.generatePrivateLinkModuleVersionsTf(),
		})
	}

	return types.MigrationInfraTerraformProject{
		MainTf:           ti.generateRootMainTf(request),
		ProvidersTf:      ti.generateRootProvidersTf(),
		VariablesTf:      GenerateVariablesTf(modules.GetTargetClusterModuleVariableDefinitions(request)),
		OutputsTf:        ti.generateRootOutputsTf(request),
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

	confluentCloudBody.SetAttributeValue("source", cty.StringVal("./confluent_cloud"))
	confluentCloudBody.AppendNewline()

	confluentCloudBody.SetAttributeRaw("providers", utils.TokensForMap(map[string]hclwrite.Tokens{"confluent": utils.TokensForResourceReference("confluent")}))
	confluentCloudBody.AppendNewline()

	WriteModuleInputs(confluentCloudBody, modules.GetConfluentCloudVariables(), request)

	if request.NeedsPrivateLink {
		rootBody.AppendNewline()
		privateLinkBlock := rootBody.AppendNewBlock("module", []string{"private_link"})
		privateLinkBody := privateLinkBlock.Body()
		privateLinkBody.SetAttributeValue("source", cty.StringVal("./private_link"))
		privateLinkBody.AppendNewline()

		privateLinkBody.SetAttributeRaw("providers", utils.TokensForMap(map[string]hclwrite.Tokens{"aws": utils.TokensForResourceReference("aws"), "confluent": utils.TokensForResourceReference("confluent")}))
		privateLinkBody.AppendNewline()

		WriteModuleInputs(privateLinkBody, modules.GetTargetClusterPrivateLinkVariables(), request)
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

	rootBody.AppendBlock(aws.GenerateProviderBlockWithVarAndDeploymentID(ti.DeploymentID))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (ti *TargetInfraHCLService) generateRootOutputsTf(request types.TargetClusterWizardRequest) string {
	// Root outputs reference module outputs so users can see key values after terraform apply
	confluentCloudOutputs := modules.GetConfluentCloudModuleOutputDefinitions(request, modules.ConfluentCloudOutputParams{
		EnvironmentName:    ti.ResourceNames.Environment,
		NetworkName:        ti.ResourceNames.Network,
		ClusterName:        ti.ResourceNames.Cluster,
		ServiceAccountName: ti.ResourceNames.ServiceAccount,
		KafkaAPIKeyName:    ti.ResourceNames.KafkaAPIKey,
	})

	var rootOutputs []types.TerraformOutput
	for _, o := range confluentCloudOutputs {
		// Skip network-internal outputs that are only used for module-to-module wiring
		if o.Name == "network_id" || o.Name == "network_dns_domain" || o.Name == "network_private_link_endpoint_service" || o.Name == "network_zones" {
			continue
		}
		rootOutputs = append(rootOutputs, types.TerraformOutput{
			Name:        o.Name,
			Description: o.Description,
			Sensitive:   o.Sensitive,
			Value:       fmt.Sprintf("module.confluent_cloud.%s", o.Name),
		})
	}

	if request.NeedsPrivateLink {
		privateLinkOutputs := modules.GetPrivateLinkModuleOutputDefinitions(ti.ResourceNames.VpcEndpoint)
		for _, o := range privateLinkOutputs {
			rootOutputs = append(rootOutputs, types.TerraformOutput{
				Name:        o.Name,
				Description: o.Description,
				Sensitive:   o.Sensitive,
				Value:       fmt.Sprintf("module.private_link.%s", o.Name),
			})
		}
	}

	return GenerateOutputsTf(rootOutputs)
}

func (ti *TargetInfraHCLService) generateInputsAutoTfvars(request types.TargetClusterWizardRequest) string {
	return GenerateInputsAutoTfvars(modules.GetTargetClusterModuleVariableValues(request))
}

// ============================================================================
// Confluent Cloud Module
// ============================================================================

func (ti *TargetInfraHCLService) generateConfluentCloudModuleMainTf(request types.TargetClusterWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Add environment (create or use data source if user states an environment already exists).
	if request.NeedsEnvironment {
		rootBody.AppendBlock(confluent.GenerateEnvironmentResource(ti.ResourceNames.Environment, modules.VarEnvironmentName, request.PreventDestroy))
		rootBody.AppendNewline()
	} else {
		rootBody.AppendBlock(confluent.GenerateEnvironmentDataSource(ti.ResourceNames.Environment, modules.VarEnvironmentID))
		rootBody.AppendNewline()
	}

	envIdRef := confluent.GetEnvironmentReference(request.NeedsEnvironment, ti.ResourceNames.Environment)

	// For dedicated clusters with private link, create the confluent_network resource first.
	networkIdRef := ""
	if request.NeedsPrivateLink && request.ClusterType == "dedicated" {
		rootBody.AppendBlock(confluent.GenerateNetworkResource(ti.ResourceNames.Network, modules.VarAWSRegion, envIdRef, request.PreventDestroy))
		rootBody.AppendNewline()
		networkIdRef = fmt.Sprintf("confluent_network.%s.id", ti.ResourceNames.Network)
	}

	// Add Kafka cluster (create or use data source if user states a cluster already exists).
	if request.NeedsCluster || request.NeedsEnvironment {
		availability := request.ClusterAvailability
		cku := request.ClusterCku
		if request.ClusterType == "enterprise" {
			availability = "HIGH"
			cku = 0
		}
		rootBody.AppendBlock(confluent.GenerateKafkaClusterResource(ti.ResourceNames.Cluster, modules.VarClusterName, request.ClusterType, availability, cku, modules.VarAWSRegion, envIdRef, networkIdRef, request.PreventDestroy))
		rootBody.AppendNewline()
	}

	rootBody.AppendBlock(confluent.GenerateSchemaRegistryDataSource(
		ti.ResourceNames.SchemaRegistry,
		envIdRef,
		fmt.Sprintf("confluent_api_key.%s", ti.ResourceNames.KafkaAPIKey),
	))
	rootBody.AppendNewline()

	description := fmt.Sprintf("Service account to manage the %s environment.", modules.VarEnvironmentName)
	truncatedName := strings.TrimRight(request.ClusterName[:min(len(request.ClusterName), 6)], "-._:")
	serviceAccountName := fmt.Sprintf("app-manager-%s", truncatedName)
	rootBody.AppendBlock(confluent.GenerateServiceAccount(ti.ResourceNames.ServiceAccount, serviceAccountName, description, request.PreventDestroy))
	rootBody.AppendNewline()

	serviceAccountRef := fmt.Sprintf("confluent_service_account.%s.id", ti.ResourceNames.ServiceAccount)
	rootBody.AppendBlock(confluent.GenerateRoleBinding(
		ti.ResourceNames.SubjectResourceOwnerRoleBinding,
		fmt.Sprintf("User:${%s}", serviceAccountRef),
		"ResourceOwner",
		utils.TokensForStringTemplate(fmt.Sprintf("${data.confluent_schema_registry_cluster.%s.resource_name}/subject=*", ti.ResourceNames.SchemaRegistry)),
		request.PreventDestroy,
	))
	rootBody.AppendNewline()

	clusterRef := fmt.Sprintf("confluent_kafka_cluster.%s.rbac_crn", ti.ResourceNames.Cluster)
	rootBody.AppendBlock(confluent.GenerateRoleBinding(
		ti.ResourceNames.KafkaClusterAdminRoleBinding,
		fmt.Sprintf("User:${%s}", serviceAccountRef),
		"CloudClusterAdmin",
		utils.TokensForResourceReference(clusterRef),
		request.PreventDestroy,
	))
	rootBody.AppendNewline()

	envResourceName := confluent.GetEnvironmentResourceName(request.NeedsEnvironment, ti.ResourceNames.Environment)
	rootBody.AppendBlock(confluent.GenerateRoleBinding(
		ti.ResourceNames.DataStewardRoleBinding,
		fmt.Sprintf("User:${%s}", serviceAccountRef),
		"DataSteward",
		utils.TokensForResourceReference(envResourceName),
		request.PreventDestroy,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateSchemaRegistryAPIKey(
		ti.ResourceNames.SchemaRegistryAPIKey,
		modules.VarEnvironmentName,
		fmt.Sprintf("confluent_service_account.%s.id", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("confluent_service_account.%s.api_version", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("confluent_service_account.%s.kind", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("data.confluent_schema_registry_cluster.%s.id", ti.ResourceNames.SchemaRegistry),
		fmt.Sprintf("data.confluent_schema_registry_cluster.%s.api_version", ti.ResourceNames.SchemaRegistry),
		fmt.Sprintf("data.confluent_schema_registry_cluster.%s.kind", ti.ResourceNames.SchemaRegistry),
		envIdRef,
		request.PreventDestroy,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateKafkaAPIKey(
		ti.ResourceNames.KafkaAPIKey,
		modules.VarEnvironmentName,
		fmt.Sprintf("confluent_service_account.%s.id", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("confluent_service_account.%s.api_version", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("confluent_service_account.%s.kind", ti.ResourceNames.ServiceAccount),
		fmt.Sprintf("confluent_kafka_cluster.%s.id", ti.ResourceNames.Cluster),
		fmt.Sprintf("confluent_kafka_cluster.%s.api_version", ti.ResourceNames.Cluster),
		fmt.Sprintf("confluent_kafka_cluster.%s.kind", ti.ResourceNames.Cluster),
		envIdRef,
		fmt.Sprintf("confluent_role_binding.%s", ti.ResourceNames.KafkaClusterAdminRoleBinding),
		request.PreventDestroy,
	))

	return string(f.Bytes())
}

func (ti *TargetInfraHCLService) generateConfluentCloudModuleVariablesTf(request types.TargetClusterWizardRequest) string {
	return GenerateVariablesTf(modules.GetConfluentCloudVariableDefinitions(request))
}

func (ti *TargetInfraHCLService) generateConfluentCloudModuleOutputsTf(request types.TargetClusterWizardRequest) string {
	outputs := modules.GetConfluentCloudModuleOutputDefinitions(request, modules.ConfluentCloudOutputParams{
		EnvironmentName:    ti.ResourceNames.Environment,
		NetworkName:        ti.ResourceNames.Network,
		ClusterName:        ti.ResourceNames.Cluster,
		ServiceAccountName: ti.ResourceNames.ServiceAccount,
		KafkaAPIKeyName:    ti.ResourceNames.KafkaAPIKey,
	})
	return GenerateOutputsTf(outputs)
}

func (ti *TargetInfraHCLService) generatePrivateLinkModuleOutputsTf() string {
	outputs := modules.GetPrivateLinkModuleOutputDefinitions(ti.ResourceNames.VpcEndpoint)
	return GenerateOutputsTf(outputs)
}

func (ti *TargetInfraHCLService) generateConfluentCloudModuleVersionsTf() string {
	return GenerateVersionsTf(confluent.AddRequiredProvider)
}

// ============================================================================
// Private Link Module
// ============================================================================

func (ti *TargetInfraHCLService) generatePrivateLinkModuleMainTf(request types.TargetClusterWizardRequest) string {
	if request.ClusterType == "dedicated" {
		return ti.generateDedicatedPrivateLinkModuleMainTf(request)
	}
	return ti.generateEnterprisePrivateLinkModuleMainTf(request)
}

func (ti *TargetInfraHCLService) generateDedicatedPrivateLinkModuleMainTf(request types.TargetClusterWizardRequest) string {
	networkDnsDomainVarRef := "var." + modules.VarNetworkDNSDomain
	networkPlEndpointServiceVarRef := "var." + modules.VarNetworkPrivateLinkEndpointService

	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// AWS caller identity for account ID
	rootBody.AppendBlock(aws.GenerateCallerIdentityDataSource(ti.ResourceNames.CallerIdentity))
	rootBody.AppendNewline()

	// Confluent private link access (dedicated cluster pattern)
	rootBody.AppendBlock(confluent.GeneratePrivateLinkAccessResource(
		ti.ResourceNames.PrivateLinkAccess,
		"kcp-private-link-access",
		fmt.Sprintf("data.aws_caller_identity.%s.account_id", ti.ResourceNames.CallerIdentity),
		"var."+modules.VarEnvironmentID,
		"var."+modules.VarNetworkID,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSecurityGroup(ti.ResourceNames.SecurityGroup, []int{80, 443, 9092}, []int{0}, modules.VarVpcID))
	rootBody.AppendNewline()

	// Use the network's supported zone IDs (not all AZs) so subnets are in zones the VPC endpoint service supports
	rootBody.AppendBlock(aws.GenerateSubnetResourceWithCountAndZoneIds(
		ti.ResourceNames.SubnetName,
		modules.VarSubnetCidrRanges,
		modules.VarNetworkZones,
		modules.VarVpcID,
	))

	// For dedicated clusters, the VPC endpoint service name comes from the confluent_network resource (via module variable)
	rootBody.AppendBlock(aws.GenerateVpcEndpointResource(
		ti.ResourceNames.VpcEndpoint,
		modules.VarVpcID,
		networkPlEndpointServiceVarRef,
		fmt.Sprintf("aws_security_group.%s.id", ti.ResourceNames.SecurityGroup),
		fmt.Sprintf("aws_subnet.%s[*].id", ti.ResourceNames.SubnetName),
		[]string{fmt.Sprintf("confluent_private_link_access.%s", ti.ResourceNames.PrivateLinkAccess)},
	))
	rootBody.AppendNewline()

	// Route53: when using an existing zone, both the zone and its records already exist.
	// Only generate the zone and record resources when creating from scratch.
	if !request.UseExistingRoute53Zone {
		rootBody.AppendBlock(aws.GenerateRoute53ZoneResource(
			ti.ResourceNames.Route53Zone,
			modules.VarVpcID,
			networkDnsDomainVarRef,
		))
		rootBody.AppendNewline()

		rootBody.AppendBlock(aws.GenerateRoute53RecordResource(
			ti.ResourceNames.Route53Record,
			fmt.Sprintf("aws_route53_zone.%s.zone_id", ti.ResourceNames.Route53Zone),
			"*",
			fmt.Sprintf("aws_vpc_endpoint.%s.dns_entry[0].dns_name", ti.ResourceNames.VpcEndpoint),
		))
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

func (ti *TargetInfraHCLService) generateEnterprisePrivateLinkModuleMainTf(request types.TargetClusterWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Ingress Private Link Gateway (replaces confluent_private_link_attachment for enterprise clusters).
	// Each access point gets a unique dns_domain, eliminating shared-zone DNS conflicts.
	rootBody.AppendBlock(confluent.GenerateIngressGatewayResource(
		ti.ResourceNames.IngressGateway,
		"kcp_ingress_gateway",
		modules.VarAWSRegion,
		modules.VarEnvironmentID,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateAvailabilityZonesDataSource(ti.ResourceNames.AvailabilityZones))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSecurityGroup(ti.ResourceNames.SecurityGroup, []int{80, 443, 9092}, []int{0}, modules.VarVpcID))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSubnetResourceWithCount(
		ti.ResourceNames.SubnetName,
		modules.VarSubnetCidrRanges,
		fmt.Sprintf("data.aws_availability_zones.%s", ti.ResourceNames.AvailabilityZones),
		modules.VarVpcID,
	))

	rootBody.AppendBlock(aws.GenerateVpcEndpointResource(
		ti.ResourceNames.VpcEndpoint,
		modules.VarVpcID,
		fmt.Sprintf("confluent_gateway.%s.aws_ingress_private_link_gateway[0].vpc_endpoint_service_name", ti.ResourceNames.IngressGateway),
		fmt.Sprintf("aws_security_group.%s.id", ti.ResourceNames.SecurityGroup),
		fmt.Sprintf("aws_subnet.%s[*].id", ti.ResourceNames.SubnetName),
		[]string{fmt.Sprintf("confluent_gateway.%s", ti.ResourceNames.IngressGateway)},
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateAccessPointResource(
		ti.ResourceNames.AccessPoint,
		request.ClusterName+"_access_point",
		modules.VarEnvironmentID,
		fmt.Sprintf("aws_vpc_endpoint.%s.id", ti.ResourceNames.VpcEndpoint),
		fmt.Sprintf("confluent_gateway.%s.id", ti.ResourceNames.IngressGateway),
	))
	rootBody.AppendNewline()

	// Route53: Each access point gets a unique dns_domain (e.g., ap123abc.us-west-2.aws.accesspoint.confluent.cloud).
	// No shared-zone conflicts — each enterprise cluster gets its own zone.
	dnsDomainRef := fmt.Sprintf("confluent_access_point.%s.aws_ingress_private_link_endpoint[0].dns_domain", ti.ResourceNames.AccessPoint)

	rootBody.AppendBlock(aws.GenerateRoute53ZoneResource(
		ti.ResourceNames.Route53Zone,
		modules.VarVpcID,
		dnsDomainRef,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRoute53RecordResource(
		ti.ResourceNames.Route53Record,
		fmt.Sprintf("aws_route53_zone.%s.zone_id", ti.ResourceNames.Route53Zone),
		"*",
		fmt.Sprintf("aws_vpc_endpoint.%s.dns_entry[0].dns_name", ti.ResourceNames.VpcEndpoint),
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (ti *TargetInfraHCLService) generatePrivateLinkModuleVariablesTf(request types.TargetClusterWizardRequest) string {
	return GenerateVariablesTf(modules.GetTargetClusterPrivateLinkModuleVariableDefinitions(request))
}

func (ti *TargetInfraHCLService) generatePrivateLinkModuleVersionsTf() string {
	return GenerateVersionsTf(confluent.AddRequiredProvider, aws.AddRequiredProvider)
}
