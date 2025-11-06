package hcl

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/hcl/aws"
	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
	"github.com/confluentinc/kcp/internal/services/hcl/other"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type MigrationInfraHCLService struct {
}

func NewMigrationInfraHCLService() *MigrationInfraHCLService {
	return &MigrationInfraHCLService{}
}

func (mi *MigrationInfraHCLService) GenerateTerraformModules(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	if request.HasPublicCcEndpoints {
		return mi.handleClusterLink(request)
	}
	return mi.handlePrivateLink(request)
}

func (mi *MigrationInfraHCLService) handleClusterLink(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	return types.MigrationInfraTerraformProject{
		MainTf:      mi.generateRootMainTfForClusterLink(),
		ProvidersTf: mi.generateRootProvidersTfForClusterLink(),
		VariablesTf: mi.generateVariablesTf(confluent.ClusterLinkVariables),
		Modules: []types.MigrationInfraTerraformModule{
			{
				Name:        "cluster_link",
				MainTf:      mi.generateClusterLinkMainTf(request),
				VariablesTf: mi.generateClusterLinkVariablesTf(),
			},
		},
	}
}

func (mi *MigrationInfraHCLService) handlePrivateLink(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	// gather up all the variables required for everything to work ie modules and providers
	providerVariables := append(confluent.ConfluentProviderVariables, aws.AwsProviderVariables...)
	requiredVariables := append(aws.JumpClusterSetupHostVariables, providerVariables...)

	return types.MigrationInfraTerraformProject{
		MainTf:      mi.generateRootMainTfForPrivateLink(),
		ProvidersTf: mi.generateRootProvidersTfForPrivateLink(),
		VariablesTf: mi.generateVariablesTf(requiredVariables),
		Modules: []types.MigrationInfraTerraformModule{
			{
				Name:        "jump_cluster_setup_host",
				MainTf:      mi.generateJumpClusterSetupHostMainTf(),
				VariablesTf: mi.generateJumpClusterSetupHostVariablesTf(),
				VersionsTf:  mi.generateJumpClusterSetupHostVersionsTf(),
				AdditionalFiles: map[string]string{
					"jump-cluster-setup-host-user-data.tpl": mi.generateJumpClusterSetupHostUserDataTpl(),
				},
			},
			{
				Name:        "confluent_platform_broker_instances",
				MainTf:      mi.generateConfluentPlatformBrokerInstancesMainTf(),
				VariablesTf: mi.generateConfluentPlatformBrokerInstancesVariablesTf(),
				OutputsTf:   mi.generateConfluentPlatformBrokerInstancesOutputsTf(),
			},
			{
				Name:        "networking",
				MainTf:      mi.generateNetworkingMainTf(request),
				VariablesTf: mi.generateNetworkingVariablesTf(),
				OutputsTf:   mi.generateNetworkingOutputsTf(),
			},
			{
				Name:        "private_link_connection",
				MainTf:      mi.generatePrivateLinkConnectionMainTf(),
				VariablesTf: mi.generatePrivateLinkConnectionVariablesTf(),
				OutputsTf:   mi.generatePrivateLinkConnectionOutputsTf(),
			},
		},
	}
}

// ============================================================================
// Root-Level Generation - Cluster Link
// ============================================================================

func (mi *MigrationInfraHCLService) generateRootMainTfForClusterLink() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	moduleBlock := rootBody.AppendNewBlock("module", []string{"cluster_link"})
	moduleBody := moduleBlock.Body()

	moduleBody.SetAttributeValue("source", cty.StringVal("./cluster_link"))
	moduleBody.AppendNewline()

	// Pass all variables to the cluster_link module
	for _, v := range confluent.ClusterLinkVariables {
		moduleBody.SetAttributeRaw(v.Name, utils.TokensForVarReference(v.Name))
	}

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateRootProvidersTfForClusterLink() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(confluent.GenerateRequiredProviderTokens())
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateProviderBlock())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// ============================================================================
// Root-Level Generation - Private Link
// ============================================================================

func (mi *MigrationInfraHCLService) generateRootMainTfForPrivateLink() string {
	// TODO: Implement main.tf generation for private link
	return ""
}

func (mi *MigrationInfraHCLService) generateRootProvidersTfForPrivateLink() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	// Add confluent provider
	requiredProvidersBody.SetAttributeRaw(confluent.GenerateRequiredProviderTokens())
	// Add aws provider
	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())
	rootBody.AppendNewline()

	// Add confluent provider block
	rootBody.AppendBlock(confluent.GenerateProviderBlock())
	rootBody.AppendNewline()

	// Add aws provider block with variable reference
	rootBody.AppendBlock(aws.GenerateProviderBlockWithVar())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// ============================================================================
// Cluster Link Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generateClusterLinkMainTf(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(confluent.GenerateClusterLinkLocals())
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateClusterLinkResource(request))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateClusterLinkVariablesTf() string {
	return mi.generateVariablesTf(confluent.ClusterLinkVariables)
}

// ============================================================================
// Jump Cluster Setup Host Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostMainTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(aws.GenerateAmazonLinuxAMI())
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateJumpClusterSetupHost())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostUserDataTpl() string {
	return aws.GenerateJumpClusterSetupHostUserDataTpl()
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostVariablesTf() string {
	return mi.generateVariablesTf(aws.JumpClusterSetupHostVariables)
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostVersionsTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())

	return string(f.Bytes())
}

// ============================================================================
// Confluent Platform Broker Instances Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generateConfluentPlatformBrokerInstancesMainTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generateConfluentPlatformBrokerInstancesVariablesTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generateConfluentPlatformBrokerInstancesOutputsTf() string {
	return ""
}

// ============================================================================
// Networking Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generateNetworkingMainTf(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	if request.HasExistingInternetGateway {
		rootBody.AppendBlock(aws.GenerateInternetGatewayDataSource("internet_gateway"))
	} else {
		rootBody.AppendBlock(aws.GenerateInternetGatewayResource("internet_gateway"))
	}
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateAvailabilityZonesDataSource("this"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSecurityGroup("security_group", []int{22, 9091, 9092, 9093, 8090, 8081}, []int{0}))
	rootBody.AppendNewline()

	subnetRefs := make([]string, len(request.JumpClusterBrokerSubnetCidr))
	for i, subnetCidrRange := range request.JumpClusterBrokerSubnetCidr {
		subnetTfName := fmt.Sprintf("jump_cluster_broker_subnet_%d", i)
		availabilityZoneRef := fmt.Sprintf("data.aws_availability_zones.this.names[%d]", i)
		rootBody.AppendBlock(aws.GenerateSubnetResource(
			subnetTfName,
			request.VpcId,
			subnetCidrRange,
			availabilityZoneRef,
		))
		subnetRefs[i] = fmt.Sprintf("aws_subnet.%s.id", subnetTfName)

		if i < len(request.JumpClusterBrokerSubnetCidr) {
			rootBody.AppendNewline()
		}
	}

	rootBody.AppendBlock(aws.GenerateSubnetResource(
		"jump_cluster_setup_host_subnet",
		request.VpcId,
		request.JumpClusterSetupHostSubnetCidr,
		"data.aws_availability_zones.available.names[0]",
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateEIPResource("nat_eip"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateNATGatewayResource("nat_gw", "aws_eip.nat_eip.id", "aws_subnet.jump_cluster_setup_host_subnet.id"))
	rootBody.AppendNewline()

	if request.HasExistingInternetGateway {
		rootBody.AppendBlock(aws.GenerateRouteTableResource("jump_cluster_setup_host_public_rt", request.VpcId, aws.GetInternetGatewayReference(request.HasExistingInternetGateway, "internet_gateway")))
		rootBody.AppendNewline()
	} else {
		rootBody.AppendBlock(aws.GenerateRouteTableResource("jump_cluster_setup_host_public_rt", request.VpcId, aws.GetInternetGatewayReference(request.HasExistingInternetGateway, "internet_gateway")))
		rootBody.AppendNewline()
	}

	rootBody.AppendBlock(aws.GenerateRouteTableAssociationResource("jump_cluster_setup_host_public_rt_association", aws.GenerateSubnetResourceReference("jump_cluster_setup_host_subnet"), "aws_route_table.jump_cluster_setup_host_public_rt.id"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableResource("private_subnet_rt", request.VpcId, "aws_nat_gateway.nat_gw.id"))
	rootBody.AppendNewline()

	for i := range request.JumpClusterBrokerSubnetCidr {
		rootBody.AppendBlock(aws.GenerateRouteTableAssociationResource(fmt.Sprintf("jump_cluster_broker_route_table_assoc_%d", i), aws.GenerateSubnetResourceReference(fmt.Sprintf("jump_cluster_broker_subnet_%d", i)), "aws_route_table.private_subnet_rt.id"))
		if i < len(request.JumpClusterBrokerSubnetCidr) {
			rootBody.AppendNewline()
		}
	}

	rootBody.AppendBlock(aws.GenerateSecurityGroup("private_link_security_group", []int{80, 443, 9092}, []int{0}))
	rootBody.AppendNewline()

	rootBody.AppendBlock(other.GenerateTLSPrivateKeyResource("jump_cluster_ssh_key", "RSA", 4096))
	rootBody.AppendNewline()

	rootBody.AppendBlock(other.GenerateLocalFileResource("jump_cluster_ssh_key_private_key", "tls_private_key.jump_cluster_ssh_key.private_key_pem", "./.ssh/jump_cluster_ssh_key_private_key_rsa", "400"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(other.GenerateLocalFileResource("jump_cluster_ssh_key_public_key", "tls_private_key.jump_cluster_ssh_key.public_key_openssh", "./.ssh/jump_cluster_ssh_key_public_key.pub", "400"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateKeyPairResource("jump_cluster_ssh_key", fmt.Sprintf("jump_cluster_ssh_key_%s", utils.RandomString(5)), "tls_private_key.jump_cluster_ssh_key.public_key_openssh"))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateNetworkingVariablesTf() string {
	requiredVariables := append(aws.InternetGatewayVariables, aws.SecurityGroupVariables...)

	seenVariables := make(map[string]types.TerraformVariable)
	for _, v := range requiredVariables {
		if _, exists := seenVariables[v.Name]; !exists {
			seenVariables[v.Name] = v
		}
	}

	var uniqueVariables []types.TerraformVariable
	for _, v := range seenVariables {
		uniqueVariables = append(uniqueVariables, v)
	}

	return mi.generateVariablesTf(uniqueVariables)
}

func (mi *MigrationInfraHCLService) generateNetworkingOutputsTf() string {
	return ""
}

// ============================================================================
// Private Link Connection Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionMainTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionVariablesTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionOutputsTf() string {
	return ""
}

// ============================================================================
// Shared/Utility Functions
// ============================================================================

func (mi *MigrationInfraHCLService) generateVariablesTf(tfVariables []types.TerraformVariable) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	for _, v := range tfVariables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.Name})
		variableBody := variableBlock.Body()
		// Use Type field if specified, otherwise default to "string"
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
