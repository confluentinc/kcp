package hcl

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/hcl/aws"
	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
	"github.com/confluentinc/kcp/internal/services/hcl/modules"
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
		return mi.handlePublicMigrationInfrastructure(request)
	}
	return mi.handlePrivateMigrationInfrastructure(request)
}

func (mi *MigrationInfraHCLService) handlePublicMigrationInfrastructure(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	return types.MigrationInfraTerraformProject{
		MainTf:      mi.generateRootMainTfForPublicMigrationInfrastructure(),
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

func (mi *MigrationInfraHCLService) handlePrivateMigrationInfrastructure(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	// Use GetRootLevelVariableDefinitions to get only root-level variable definitions
	requiredVariables := modules.GetMigrationInfraRootVariableDefinitions(request)

	return types.MigrationInfraTerraformProject{
		MainTf:           mi.generateRootMainTfForPrivateMigrationInfrastructure(request),
		ProvidersTf:      mi.generateRootProvidersTfForPrivateMigrationInfrastructure(),
		VariablesTf:      mi.generateVariablesTf(requiredVariables),
		InputsAutoTfvars: mi.generateInputsAutoTfvars(request),
		Modules: []types.MigrationInfraTerraformModule{
			{
				Name:        "jump_cluster_setup_host",
				MainTf:      mi.generateJumpClusterSetupHostMainTf(),
				VariablesTf: mi.generateJumpClusterSetupHostVariablesTf(request),
				VersionsTf:  mi.generateJumpClusterSetupHostVersionsTf(),
				AdditionalFiles: map[string]string{
					"jump-cluster-setup-host-user-data.tpl": mi.generateJumpClusterSetupHostUserDataTpl(),
				},
			},
			{
				Name:        "jump_clusters",
				MainTf:      mi.generateJumpClustersMainTf(request),
				VariablesTf: mi.generateJumpClustersVariablesTf(request),
				OutputsTf:   mi.generateJumpClustersOutputsTf(),
				VersionsTf:  mi.generateJumpClustersVersionsTf(),
				AdditionalFiles: func() map[string]string {
					additionalFiles := make(map[string]string)
					additionalFiles["jump-cluster-with-cluster-links-user-data.tpl"] = mi.generateJumpClusterClusterLinksUserDataTpl(request.MskJumpClusterAuthType)
					additionalFiles["jump-cluster-user-data.tpl"] = mi.generateJumpClusterUserDataTpl()

					return additionalFiles
				}(),
			},
			{
				Name:        "networking",
				MainTf:      mi.generateNetworkingMainTf(request),
				VariablesTf: mi.generateNetworkingVariablesTf(request),
				OutputsTf:   mi.generateNetworkingOutputsTf(),
				VersionsTf:  mi.generateNetworkingVersionsTf(),
			},
			{
				Name:        "private_link_connection",
				MainTf:      mi.generatePrivateLinkConnectionMainTf(),
				VariablesTf: mi.generatePrivateLinkConnectionVariablesTf(request),
				VersionsTf:  mi.generatePrivateLinkConnectionVersionsTf(),
			},
		},
	}
}

// ============================================================================
// Root-Level Generation - Public Migration
// ============================================================================

func (mi *MigrationInfraHCLService) generateRootMainTfForPublicMigrationInfrastructure() string {
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
// Cluster Link Module Generation (Public)
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
// Root-Level Generation - Private Migration
// ============================================================================

func (mi *MigrationInfraHCLService) generateRootMainTfForPrivateMigrationInfrastructure(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	networkingModuleBlock := rootBody.AppendNewBlock("module", []string{"networking"})
	networkingModuleBody := networkingModuleBlock.Body()
	networkingModuleBody.SetAttributeValue("source", cty.StringVal("./modules/networking"))
	networkingModuleBody.AppendNewline()

	networkingModuleBody.SetAttributeRaw("providers", utils.TokensForMap(map[string]hclwrite.Tokens{
		"aws": utils.TokensForResourceReference("aws"),
	}))
	networkingModuleBody.AppendNewline()

	networkingVars := modules.GetNetworkingVariables()
	for _, varDef := range networkingVars {
		if varDef.Condition == nil || varDef.Condition(request) {
			networkingModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForVarReference(varDef.Name))
		}
	}
	rootBody.AppendNewline()

	setupHostModuleBlock := rootBody.AppendNewBlock("module", []string{"jump_cluster_setup_host"})
	setupHostModuleBody := setupHostModuleBlock.Body()
	setupHostModuleBody.SetAttributeValue("source", cty.StringVal("./modules/jump_cluster_setup_host"))
	setupHostModuleBody.AppendNewline()

	setupHostModuleBody.SetAttributeRaw("providers", utils.TokensForMap(map[string]hclwrite.Tokens{
		"aws": utils.TokensForResourceReference("aws"),
	}))
	setupHostModuleBody.AppendNewline()

	setupHostVars := modules.GetJumpClusterSetupHostVariables()
	for _, varDef := range setupHostVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		if varDef.FromModuleOutput != "" || varDef.ValueExtractor == nil {
			// Use FromModuleOutput to determine which module this comes from
			setupHostModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForModuleOutput(varDef.FromModuleOutput, varDef.Name))
		} else {
			setupHostModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForVarReference(varDef.Name))
		}
	}
	rootBody.AppendNewline()

	setupHostModuleBody.AppendNewline()
	setupHostModuleBody.SetAttributeRaw("depends_on", utils.TokensForList([]string{"module.jump_clusters"}))

	jumpClustersModuleBlock := rootBody.AppendNewBlock("module", []string{"jump_clusters"})
	jumpClustersModuleBody := jumpClustersModuleBlock.Body()
	jumpClustersModuleBody.SetAttributeValue("source", cty.StringVal("./modules/jump_clusters"))
	jumpClustersModuleBody.AppendNewline()

	jumpClustersModuleBody.SetAttributeRaw("providers", utils.TokensForMap(map[string]hclwrite.Tokens{
		"aws": utils.TokensForResourceReference("aws"),
	}))
	jumpClustersModuleBody.AppendNewline()

	jumpClusterVars := modules.GetJumpClusterVariables()
	for _, varDef := range jumpClusterVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		if varDef.FromModuleOutput != "" || varDef.ValueExtractor == nil {
			// Use FromModuleOutput to determine which module this comes from
			jumpClustersModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForModuleOutput(varDef.FromModuleOutput, varDef.Name))
		} else {
			jumpClustersModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForVarReference(varDef.Name))
		}
	}
	rootBody.AppendNewline()

	privateLinkModuleBlock := rootBody.AppendNewBlock("module", []string{"private_link_connection"})
	privateLinkModuleBody := privateLinkModuleBlock.Body()
	privateLinkModuleBody.SetAttributeValue("source", cty.StringVal("./modules/private_link_connection"))
	privateLinkModuleBody.AppendNewline()

	privateLinkModuleBody.SetAttributeRaw("providers", utils.TokensForMap(map[string]hclwrite.Tokens{
		"aws":       utils.TokensForResourceReference("aws"),
		"confluent": utils.TokensForResourceReference("confluent"),
	}))
	privateLinkModuleBody.AppendNewline()

	privateLinkVars := modules.GetMigrationInfraPrivateLinkVariables()
	for _, varDef := range privateLinkVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		if varDef.FromModuleOutput != "" || varDef.ValueExtractor == nil {
			// Use FromModuleOutput to determine which module this comes from
			if varDef.Name == "security_group_id" {
				privateLinkModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForModuleOutput(varDef.FromModuleOutput, "private_link_security_group_id"))
			} else {
				privateLinkModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForModuleOutput(varDef.FromModuleOutput, varDef.Name))
			}
		} else {
			privateLinkModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForVarReference(varDef.Name))
		}
	}

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateRootProvidersTfForPrivateMigrationInfrastructure() string {
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

// ============================================================================
// Jump Cluster Setup Host Module Generation (Private)
// ============================================================================

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostMainTf() string {
	jumpClusterSetupHostSubnetIdVarName := modules.GetModuleVariableName("jump_cluster_setup_host", "jump_cluster_setup_host_subnet_id")
	securityGroupIdsVarName := modules.GetModuleVariableName("jump_cluster_setup_host", "jump_cluster_security_group_ids")
	jumpClusterSshKeyPairNameVarName := modules.GetModuleVariableName("jump_cluster_setup_host", "jump_cluster_ssh_key_pair_name")
	jumpClusterBrokerSubnetIdsVarName := modules.GetModuleVariableName("jump_cluster_setup_host", "jump_cluster_broker_subnet_ids")
	privateKeyVarName := modules.GetModuleVariableName("jump_cluster_setup_host", "private_key")

	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(aws.GenerateAmiDataResource("amzn_linux_ami", "137112412989", true, map[string]string{
		"name":                "al2023-ami-2023.*-kernel-6.1-x86_64",
		"state":               "available",
		"architecture":        "x86_64",
		"virtualization-type": "hvm",
	}))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateEc2UserDataInstanceResource(
		"jump_cluster_setup_host",
		"data.aws_ami.amzn_linux_ami.id",
		"t2.medium",
		jumpClusterSetupHostSubnetIdVarName,
		securityGroupIdsVarName,
		jumpClusterSshKeyPairNameVarName,
		"jump-cluster-setup-host-user-data.tpl",
		true,
		map[string]hclwrite.Tokens{
			"broker_ips":  utils.TokensForVarReference(jumpClusterBrokerSubnetIdsVarName),
			"private_key": utils.TokensForVarReference(privateKeyVarName),
		},
		nil,
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostUserDataTpl() string {
	return aws.GenerateJumpClusterSetupHostUserDataTpl()
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostVariablesTf(request types.MigrationWizardRequest) string {
	return mi.generateVariablesTf(modules.GetJumpClusterSetupHostVariableDefinitions(request))
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
// Jump Cluster Module Generation (Private)
// ============================================================================

func (mi *MigrationInfraHCLService) generateJumpClustersMainTf(request types.MigrationWizardRequest) string {
	jumpClusterBrokerSubnetIdsVarName := modules.GetModuleVariableName("jump_clusters", "jump_cluster_broker_subnet_ids")
	securityGroupIdsVarName := modules.GetModuleVariableName("jump_clusters", "jump_cluster_security_group_ids")
	jumpClusterSshKeyPairNameVarName := modules.GetModuleVariableName("jump_clusters", "jump_cluster_ssh_key_pair_name")
	jumpClusterInstanceTypeVarName := modules.GetModuleVariableName("jump_clusters", "jump_cluster_instance_type")
	jumpClusterBrokerStorageVarName := modules.GetModuleVariableName("jump_clusters", "jump_cluster_broker_storage")
	targetClusterIdVarName := modules.GetModuleVariableName("jump_clusters", "confluent_cloud_cluster_id")
	targetBootstrapEndpointVarName := modules.GetModuleVariableName("jump_clusters", "confluent_cloud_cluster_bootstrap_endpoint")
	targetRestEndpointVarName := modules.GetModuleVariableName("jump_clusters", "confluent_cloud_cluster_rest_endpoint")
	targetApiKeyVarName := modules.GetModuleVariableName("jump_clusters", "confluent_cloud_cluster_api_key")
	targetApiSecretVarName := modules.GetModuleVariableName("jump_clusters", "confluent_cloud_cluster_api_secret")
	mskClusterIdVarName := modules.GetModuleVariableName("jump_clusters", "msk_cluster_id")
	mskBootstrapBrokersVarName := modules.GetModuleVariableName("jump_clusters", "msk_cluster_bootstrap_brokers")

	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(aws.GenerateAmiDataResource("red_hat_linux_ami", "309956199498", true, map[string]string{
		"name":                "RHEL-9.6.0_HVM_GA-*",
		"state":               "available",
		"architecture":        "x86_64",
		"virtualization-type": "hvm",
	}))
	rootBody.AppendNewline()

	if request.MskJumpClusterAuthType == "sasl_scram" {
		mskSaslScramUsernameVarName := modules.GetModuleVariableName("jump_clusters", "msk_sasl_scram_username")
		mskSaslScramPasswordVarName := modules.GetModuleVariableName("jump_clusters", "msk_sasl_scram_password")

		rootBody.AppendBlock(aws.GenerateEc2UserDataInstanceResourceWithForEach(
			"jump_cluster",
			"data.aws_ami.red_hat_linux_ami.id",
			jumpClusterInstanceTypeVarName,
			jumpClusterBrokerSubnetIdsVarName,
			securityGroupIdsVarName,
			jumpClusterSshKeyPairNameVarName,
			"jump-cluster-with-cluster-links-user-data.tpl",
			"jump-cluster-user-data.tpl",
			"",
			false,
			map[string]hclwrite.Tokens{
				"confluent_cloud_cluster_id":                 utils.TokensForVarReference(targetClusterIdVarName),
				"confluent_cloud_cluster_bootstrap_endpoint": utils.TokensForVarReference(targetBootstrapEndpointVarName),
				"confluent_cloud_cluster_rest_endpoint":      utils.TokensForVarReference(targetRestEndpointVarName),
				"confluent_cloud_cluster_key":                utils.TokensForVarReference(targetApiKeyVarName),
				"confluent_cloud_cluster_secret":             utils.TokensForVarReference(targetApiSecretVarName),
				"msk_cluster_id":                             utils.TokensForVarReference(mskClusterIdVarName),
				"msk_cluster_bootstrap_brokers":              utils.TokensForVarReference(mskBootstrapBrokersVarName),
				"msk_sasl_scram_username":                    utils.TokensForVarReference(mskSaslScramUsernameVarName),
				"msk_sasl_scram_password":                    utils.TokensForVarReference(mskSaslScramPasswordVarName),
			},
			aws.OptionalBlocksConfig{
				"root_block_device": {
					"volume_size": utils.TokensForVarReference(jumpClusterBrokerStorageVarName),
					"volume_type": cty.StringVal("gp3"),
				},
				"metadata_options": {
					"http_tokens":                 cty.StringVal("required"),
					"http_put_response_hop_limit": cty.NumberIntVal(10),
				},
			},
		))
	} else {
		jumpClusterIamAuthRoleNameVarName := modules.GetModuleVariableName("jump_clusters", "jump_cluster_iam_auth_role_name")

		rootBody.AppendBlock(aws.GenerateEc2UserDataInstanceResourceWithForEach(
			"jump_cluster",
			"data.aws_ami.red_hat_linux_ami.id",
			jumpClusterInstanceTypeVarName,
			jumpClusterBrokerSubnetIdsVarName,
			securityGroupIdsVarName,
			jumpClusterSshKeyPairNameVarName,
			"jump-cluster-with-cluster-links-user-data.tpl",
			"jump-cluster-user-data.tpl",
			jumpClusterIamAuthRoleNameVarName,
			false,
			map[string]hclwrite.Tokens{
				"confluent_cloud_cluster_id":                 utils.TokensForVarReference(targetClusterIdVarName),
				"confluent_cloud_cluster_bootstrap_endpoint": utils.TokensForVarReference(targetBootstrapEndpointVarName),
				"confluent_cloud_cluster_rest_endpoint":      utils.TokensForVarReference(targetRestEndpointVarName),
				"confluent_cloud_cluster_key":                utils.TokensForVarReference(targetApiKeyVarName),
				"confluent_cloud_cluster_secret":             utils.TokensForVarReference(targetApiSecretVarName),
				"msk_cluster_id":                             utils.TokensForVarReference(mskClusterIdVarName),
				"msk_cluster_bootstrap_brokers":              utils.TokensForVarReference(mskBootstrapBrokersVarName),
			},
			aws.OptionalBlocksConfig{
				"root_block_device": {
					"volume_size": utils.TokensForVarReference(jumpClusterBrokerStorageVarName),
					"volume_type": cty.StringVal("gp3"),
				},
				"metadata_options": {
					"http_tokens":                 cty.StringVal("required"),
					"http_put_response_hop_limit": cty.NumberIntVal(10),
				},
			},
		))
	}
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateJumpClusterClusterLinksUserDataTpl(authType string) string {
	if authType == "sasl_scram" {
		return aws.GenerateJumpClusterWithSaslScramClusterLinksUserDataTpl()
	} else {
		return aws.GenerateJumpClusterWithIamClusterLinksUserDataTpl()
	}
}

func (mi *MigrationInfraHCLService) generateJumpClusterUserDataTpl() string {
	return aws.GenerateJumpClusterUserDataTpl()
}

func (mi *MigrationInfraHCLService) generateJumpClustersVariablesTf(request types.MigrationWizardRequest) string {
	requiredVariables := modules.GetJumpClusterModuleVariableDefinitions(request)
	return mi.generateVariablesTf(requiredVariables)
}

func (mi *MigrationInfraHCLService) generateJumpClustersOutputsTf() string {
	outputs := modules.GetJumpClusterModuleOutputDefinitions()
	return mi.generateOutputsTf(outputs)
}

func (mi *MigrationInfraHCLService) generateJumpClustersVersionsTf() string {
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
// Networking Module Generation (Private)
// ============================================================================

func (mi *MigrationInfraHCLService) generateNetworkingMainTf(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Get variable names from module definitions
	vpcIdVarName := modules.GetModuleVariableName("networking", "vpc_id")
	jumpClusterBrokerSubnetCidrsVarName := modules.GetModuleVariableName("networking", "jump_cluster_broker_subnet_cidrs")
	jumpClusterSetupHostSubnetCidrVarName := modules.GetModuleVariableName("networking", "jump_cluster_setup_host_subnet_cidr")

	if request.HasExistingInternetGateway {
		rootBody.AppendBlock(aws.GenerateInternetGatewayDataSource("internet_gateway", vpcIdVarName))
	} else {
		rootBody.AppendBlock(aws.GenerateInternetGatewayResource("internet_gateway", vpcIdVarName))
	}
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateAvailabilityZonesDataSource("available"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSecurityGroup("security_group", []int{22, 9091, 9092, 9093, 8090, 8081}, []int{0}, vpcIdVarName))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSubnetResourceWithCount(
		"jump_cluster_broker_subnets",
		jumpClusterBrokerSubnetCidrsVarName,
		"data.aws_availability_zones.available",
		vpcIdVarName,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSubnetResource(
		"jump_cluster_setup_host_subnet",
		jumpClusterSetupHostSubnetCidrVarName,
		"data.aws_availability_zones.available.names[0]",
		vpcIdVarName,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateEIPResource("nat_eip"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateNATGatewayResource("nat_gw", "aws_eip.nat_eip.id", "aws_subnet.jump_cluster_setup_host_subnet.id"))
	rootBody.AppendNewline()

	if request.HasExistingInternetGateway {
		rootBody.AppendBlock(aws.GenerateRouteTableResource("jump_cluster_setup_host_public_rt", aws.GetInternetGatewayReference(request.HasExistingInternetGateway, "internet_gateway"), vpcIdVarName))
		rootBody.AppendNewline()
	} else {
		rootBody.AppendBlock(aws.GenerateRouteTableResource("jump_cluster_setup_host_public_rt", aws.GetInternetGatewayReference(request.HasExistingInternetGateway, "internet_gateway"), vpcIdVarName))
		rootBody.AppendNewline()
	}

	rootBody.AppendBlock(aws.GenerateRouteTableAssociationResource("jump_cluster_setup_host_public_rt_association", aws.GenerateSubnetResourceReference("jump_cluster_setup_host_subnet"), "aws_route_table.jump_cluster_setup_host_public_rt.id"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableResource("private_subnet_rt", "aws_nat_gateway.nat_gw.id", vpcIdVarName))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableAssociationResourceWithCount("jump_cluster_broker_route_table_assoc", aws.GenerateSubnetResourceReference("jump_cluster_broker_subnets"), "aws_route_table.private_subnet_rt.id"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSecurityGroup("private_link_security_group", []int{80, 443, 9092}, []int{0}, vpcIdVarName))
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

func (mi *MigrationInfraHCLService) generateNetworkingVariablesTf(request types.MigrationWizardRequest) string {
	requiredVariables := modules.GetNetworkingModuleVariableDefinitions(request)
	return mi.generateVariablesTf(requiredVariables)
}

func (mi *MigrationInfraHCLService) generateNetworkingOutputsTf() string {
	outputs := modules.GetNetworkingModuleOutputDefinitions()
	return mi.generateOutputsTf(outputs)
}

func (mi *MigrationInfraHCLService) generateNetworkingVersionsTf() string {
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
// Private Link Connection Module Generation (Private)
// ============================================================================

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionMainTf() string {
	awsRegionVarName := modules.GetModuleVariableName("private_link_connection", "aws_region")
	targetEnvironmentIdVarName := modules.GetModuleVariableName("private_link_connection", "target_environment_id")
	vpcIdVarName := modules.GetModuleVariableName("private_link_connection", "vpc_id")
	jumpClusterBrokerSubnetIdsVarName := modules.GetModuleVariableName("private_link_connection", "jump_cluster_broker_subnet_ids")
	securityGroupIdVarName := modules.GetModuleVariableName("private_link_connection", "security_group_id")

	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(confluent.GeneratePrivateLinkAttachmentResource(
		"jump_cluster_private_link_attachment",
		"jump_cluster_private_link_attachment",
		awsRegionVarName,
		targetEnvironmentIdVarName,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateVpcEndpointResource(
		"jump_cluster_vpc_endpoint",
		vpcIdVarName,
		"confluent_private_link_attachment.jump_cluster_private_link_attachment.aws[0].vpc_endpoint_service_name",
		securityGroupIdVarName,
		jumpClusterBrokerSubnetIdsVarName,
		[]string{"confluent_private_link_attachment.jump_cluster_private_link_attachment"},
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GeneratePrivateLinkAttachmentConnectionResource(
		"jump_cluster_private_link_connection",
		"jump_cluster_private_link_connection",
		targetEnvironmentIdVarName,
		"aws_vpc_endpoint.jump_cluster_vpc_endpoint.id",
		"confluent_private_link_attachment.jump_cluster_private_link_attachment.id",
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRoute53ZoneResourceNew(
		"jump_cluster_private_link_zone",
		vpcIdVarName,
		"confluent_private_link_attachment.jump_cluster_private_link_attachment.dns_domain",
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRoute53RecordResourceNew(
		"jump_cluster_private_link_record",
		"aws_route53_zone.jump_cluster_private_link_zone.zone_id",
		"aws_vpc_endpoint.jump_cluster_vpc_endpoint.dns_entry[0].dns_name",
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionVariablesTf(request types.MigrationWizardRequest) string {
	requiredVariables := modules.GetMigrationInfraPrivateLinkModuleVariableDefinitions(request)
	return mi.generateVariablesTf(requiredVariables)
}

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionVersionsTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())
	requiredProvidersBody.SetAttributeRaw(confluent.GenerateRequiredProviderTokens())

	return string(f.Bytes())
}

// ============================================================================
// Shared/Utility Functions
// ============================================================================

func (mi *MigrationInfraHCLService) generateVariablesTf(tfVariables []types.TerraformVariable) string {
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

func (mi *MigrationInfraHCLService) generateOutputsTf(tfOutputs []types.TerraformOutput) string {
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

func (mi *MigrationInfraHCLService) generateInputsAutoTfvars(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Use GetRootLevelVariableValues to get only root-level variables (not from module outputs)
	values := modules.GetMigrationInfraRootVariableValues(request)

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
