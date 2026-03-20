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
	// SSHKeySuffix overrides the random suffix used in SSH key pair names.
	// When empty, a random 5-character string is generated.
	SSHKeySuffix string
	// DeploymentID overrides the random deployment identifier in AWS provider tags.
	// When empty, a random 8-character string is generated.
	DeploymentID string
}

func NewMigrationInfraHCLService() *MigrationInfraHCLService {
	return &MigrationInfraHCLService{}
}

func (mi *MigrationInfraHCLService) GenerateTerraformModules(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	if request.HasPublicMskEndpoints {
		return mi.handlePublicMigrationInfrastructure(request)
	}

	if request.UseJumpClusters {
		return mi.handlePrivateMigrationInfrastructure(request)
	}

	return mi.handleExternalOutboundClusterLinkingInfrastructure(request)
}

func (mi *MigrationInfraHCLService) handlePublicMigrationInfrastructure(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	// Use GetRootLevelVariableDefinitions to get only root-level variable definitions
	requiredVariables := modules.GetMigrationInfraRootVariableDefinitions(request)

	return types.MigrationInfraTerraformProject{
		MainTf:           mi.generateRootMainTfForPublicMigrationInfrastructure(request),
		ProvidersTf:      mi.generateRootProvidersTfForClusterLink(),
		VariablesTf:      mi.generateVariablesTf(requiredVariables),
		InputsAutoTfvars: mi.generateInputsAutoTfvars(request),
		Modules: []types.MigrationInfraTerraformModule{
			{
				Name:        "cluster_link",
				MainTf:      mi.generateClusterLinkMainTf(),
				VariablesTf: mi.generateClusterLinkVariablesTf(request),
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
		ReadmeMd:         mi.generateJumpClusterReadmeMd(request),
		InputsAutoTfvars: mi.generateInputsAutoTfvars(request),
		Modules: []types.MigrationInfraTerraformModule{
			{
				Name:        "jump_cluster_setup_host",
				MainTf:      mi.generateJumpClusterSetupHostMainTf(),
				VariablesTf: mi.generateJumpClusterSetupHostVariablesTf(request),
				VersionsTf:  mi.generateJumpClusterSetupHostVersionsTf(),
				AdditionalFiles: map[string]string{
					"jump-cluster-setup-host-user-data.tpl": mi.generateJumpClusterSetupHostUserDataTpl(request),
				},
			},
			{
				Name:        "jump_cluster",
				MainTf:      mi.generateJumpClustersMainTf(request),
				VariablesTf: mi.generateJumpClustersVariablesTf(request),
				OutputsTf:   mi.generateJumpClustersOutputsTf(),
				VersionsTf:  mi.generateJumpClustersVersionsTf(),
				AdditionalFiles: map[string]string{
					"jump-cluster-with-cluster-links-user-data.tpl": mi.generateJumpClusterClusterLinksUserDataTpl(request.MskJumpClusterAuthType),
				},
			},
			{
				Name:        "networking",
				MainTf:      mi.generateNetworkingMainTf(request),
				VariablesTf: mi.generateNetworkingVariablesTf(request),
				OutputsTf:   mi.generateNetworkingOutputsTf(),
				VersionsTf:  mi.generateNetworkingVersionsTf(),
			},
		},
	}
}

func (mi *MigrationInfraHCLService) handleExternalOutboundClusterLinkingInfrastructure(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	// Use GetRootLevelVariableDefinitions to get only root-level variable definitions
	requiredVariables := modules.GetMigrationInfraRootVariableDefinitions(request)

	return types.MigrationInfraTerraformProject{
		MainTf:           mi.generateRootMainTfForExternalOutboundClusterLinkingInfrastructure(request),
		ProvidersTf:      mi.generateRootProvidersTfForExternalOutboundClusterLinkingInfrastructure(),
		VariablesTf:      mi.generateVariablesTf(requiredVariables),
		InputsAutoTfvars: mi.generateInputsAutoTfvars(request),
		Modules: []types.MigrationInfraTerraformModule{
			{
				Name:        "external_outbound_cluster_link",
				MainTf:      mi.generateExternalOutboundClusterLinkMainTf(),
				VariablesTf: mi.generateExternalOutboundClusterLinkVariablesTf(request),
				AdditionalFiles: map[string]string{
					"create-external-outbound-cluster-link.tpl": mi.generateCreateExternalOutboundClusterLinkTpl(),
				},
			},
		},
	}
}

// ============================================================================
// Root-Level Generation - Public Migration
// ============================================================================

func (mi *MigrationInfraHCLService) generateRootMainTfForPublicMigrationInfrastructure(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	moduleBlock := rootBody.AppendNewBlock("module", []string{"cluster_link"})
	moduleBody := moduleBlock.Body()

	moduleBody.SetAttributeValue("source", cty.StringVal("./cluster_link"))
	moduleBody.AppendNewline()

	clusterLinkVars := modules.GetClusterLinkVariables()
	for _, varDef := range clusterLinkVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		if varDef.FromModuleOutput != "" || varDef.ValueExtractor == nil {
			// Use FromModuleOutput to determine which module this comes from
			moduleBody.SetAttributeRaw(varDef.Name, utils.TokensForModuleOutput(varDef.FromModuleOutput, varDef.Name))
		} else {
			moduleBody.SetAttributeRaw(varDef.Name, utils.TokensForVarReference(varDef.Name))
		}
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

func (mi *MigrationInfraHCLService) generateClusterLinkMainTf() string {
	ccClusterKeyVarName := modules.VarConfluentCloudClusterAPIKey
	ccClusterSecretVarName := modules.VarConfluentCloudClusterAPISecret
	mskClusterIdVarName := modules.VarMSKClusterID
	targetClusterIdVarName := modules.VarTargetClusterID
	targetClusterRestEndpointVarName := modules.VarTargetClusterRestEndpoint
	clusterLinkVarName := modules.VarClusterLinkName
	mskSaslScramBootstrapServersVarName := modules.VarMSKSaslScramBootstrapServers
	mskSaslScramUsernameVarName := modules.VarMSKSaslScramUsername
	mskSaslScramPasswordVarName := modules.VarMSKSaslScramPassword

	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(confluent.GenerateClusterLinkLocals(
		ccClusterKeyVarName,
		ccClusterSecretVarName,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateClusterLinkResource(
		"confluent_cluster_link",
		mskClusterIdVarName,
		targetClusterIdVarName,
		targetClusterRestEndpointVarName,
		clusterLinkVarName,
		mskSaslScramBootstrapServersVarName,
		mskSaslScramUsernameVarName,
		mskSaslScramPasswordVarName,
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateClusterLinkVariablesTf(request types.MigrationWizardRequest) string {
	requiredVariables := modules.GetClusterLinkModuleVariableDefinitions(request)
	return mi.generateVariablesTf(requiredVariables)
}

// ============================================================================
// Root-Level Generation - Private Migration - Jump Clusters
// ============================================================================

func (mi *MigrationInfraHCLService) generateRootMainTfForPrivateMigrationInfrastructure(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	networkingModuleBlock := rootBody.AppendNewBlock("module", []string{"networking"})
	networkingModuleBody := networkingModuleBlock.Body()
	networkingModuleBody.SetAttributeValue("source", cty.StringVal("./networking"))
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
	setupHostModuleBody.SetAttributeValue("source", cty.StringVal("./jump_cluster_setup_host"))
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
	setupHostModuleBody.SetAttributeRaw("depends_on", utils.TokensForList([]string{"module.jump_cluster"}))

	jumpClustersModuleBlock := rootBody.AppendNewBlock("module", []string{"jump_cluster"})
	jumpClustersModuleBody := jumpClustersModuleBlock.Body()
	jumpClustersModuleBody.SetAttributeValue("source", cty.StringVal("./jump_cluster"))
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

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateRootProvidersTfForPrivateMigrationInfrastructure() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateProviderBlockWithVarAndDeploymentID(mi.DeploymentID))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// ============================================================================
// README Generation (Private - Jump Clusters)
// ============================================================================

func (mi *MigrationInfraHCLService) generateJumpClusterReadmeMd(request types.MigrationWizardRequest) string {
	credentialsSection := `
You will be prompted for the following credentials during ` + "`terraform apply`" + `:

| Variable | Description |
|----------|-------------|
| ` + "`confluent_cloud_api_key`" + ` | Confluent Cloud API key (Cloud Resource Management) |
| ` + "`confluent_cloud_api_secret`" + ` | Confluent Cloud API secret (Cloud Resource Management) |
| ` + "`confluent_cloud_cluster_api_key`" + ` | API key for the Confluent Cloud cluster |
| ` + "`confluent_cloud_cluster_api_secret`" + ` | API secret for the Confluent Cloud cluster |`

	if request.MskJumpClusterAuthType == "sasl_scram" {
		credentialsSection += `
| ` + "`msk_sasl_scram_username`" + ` | SASL/SCRAM username for MSK authentication |
| ` + "`msk_sasl_scram_password`" + ` | SASL/SCRAM password for MSK authentication |`
	}

	return `# Migration Infrastructure - Jump Cluster Setup

## Prerequisites

- [Terraform](https://developer.hashicorp.com/terraform/install) installed
- AWS credentials configured (via environment variables, AWS CLI profile, or IAM role)
- Confluent Cloud API key and secret (Cloud Resource Management)
- Confluent Cloud cluster API key and secret
- Private Link setup between the AWS VPC (` + request.VpcId + `) and Confluent Cloud

## Required Credentials
` + credentialsSection + `

## Usage

1. Initialize Terraform:

` + "```bash" + `
terraform init
` + "```" + `

2. Review the execution plan:

` + "```bash" + `
terraform plan
` + "```" + `

3. Apply the configuration:

` + "```bash" + `
terraform apply
` + "```" + `

Terraform will prompt you for the required credentials listed above.

## What Happens

After ` + "`terraform apply`" + ` completes, the following infrastructure is provisioned:

- **Networking**: VPC subnets, security groups, NAT gateway, and SSH key pair
- **Jump cluster brokers**: Confluent Platform Kafka instances deployed on EC2
- **Setup host**: An EC2 instance that runs Ansible playbooks to configure the jump cluster and establish cluster links between MSK, the jump cluster, and Confluent Cloud

The setup host automatically orchestrates the full configuration — no manual Ansible execution is required.

Note: Due to the nature of how the cluster link is created between the jump cluster and Confluent Cloud, the deletion of the cluster link will need to be manually performed using the Confluent Cloud CLI within the VPC network.
`
}

// ============================================================================
// Jump Cluster Setup Host Module Generation (Private)
// ============================================================================

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostMainTf() string {
	jumpClusterSetupHostSubnetIdVarName := modules.VarJumpClusterSetupHostSubnetID
	securityGroupIdsVarName := modules.VarJumpClusterSecurityGroupIDs
	jumpClusterSshKeyPairNameVarName := modules.VarJumpClusterSSHKeyPairName
	jumpClusterInstancesPrivateDnsVarName := modules.VarJumpClusterInstancesPrivateDNS
	privateKeyVarName := modules.VarPrivateKey

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
			"broker_ips":  utils.TokensForVarReference(jumpClusterInstancesPrivateDnsVarName),
			"private_key": utils.TokensForVarReference(privateKeyVarName),
		},
		nil,
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostUserDataTpl(request types.MigrationWizardRequest) string {
	if request.MskJumpClusterAuthType == "sasl_scram" {
		return aws.GenerateJumpClusterSaslScramSetupHostUserDataTpl()
	} else {
		return aws.GenerateJumpClusterSaslIamSetupHostUserDataTpl()
	}
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostVariablesTf(request types.MigrationWizardRequest) string {
	return mi.generateVariablesTf(modules.GetJumpClusterSetupHostVariableDefinitions(request))
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostVersionsTf() string {
	return GenerateVersionsTf(aws.AddRequiredProvider)
}

// ============================================================================
// Jump Cluster Module Generation (Private)
// ============================================================================

func (mi *MigrationInfraHCLService) generateJumpClustersMainTf(request types.MigrationWizardRequest) string {
	jumpClusterBrokerSubnetIdsVarName := modules.VarJumpClusterBrokerSubnetIDs
	securityGroupIdsVarName := modules.VarJumpClusterSecurityGroupIDs
	jumpClusterSshKeyPairNameVarName := modules.VarJumpClusterSSHKeyPairName
	jumpClusterInstanceTypeVarName := modules.VarJumpClusterInstanceType
	jumpClusterBrokerStorageVarName := modules.VarJumpClusterBrokerStorage
	targetClusterIdVarName := modules.VarConfluentCloudClusterID
	targetBootstrapEndpointVarName := modules.VarConfluentCloudClusterBootstrapEndpoint
	targetRestEndpointVarName := modules.VarConfluentCloudClusterRestEndpoint
	targetApiKeyVarName := modules.VarConfluentCloudClusterAPIKey
	targetApiSecretVarName := modules.VarConfluentCloudClusterAPISecret
	mskClusterIdVarName := modules.VarMSKClusterID
	mskBootstrapBrokersVarName := modules.VarMSKClusterBootstrapBrokers
	clusterLinkNameVarName := modules.VarClusterLinkName

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
		mskSaslScramUsernameVarName := modules.VarMSKSaslScramUsername
		mskSaslScramPasswordVarName := modules.VarMSKSaslScramPassword

		rootBody.AppendBlock(aws.GenerateEc2UserDataInstanceResourceWithForEach(
			"jump_cluster",
			"data.aws_ami.red_hat_linux_ami.id",
			jumpClusterInstanceTypeVarName,
			jumpClusterBrokerSubnetIdsVarName,
			securityGroupIdsVarName,
			jumpClusterSshKeyPairNameVarName,
			"jump-cluster-with-cluster-links-user-data.tpl",
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
				"cluster_link_name":                          utils.TokensForVarReference(clusterLinkNameVarName),
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
		jumpClusterIamAuthRoleNameVarName := modules.VarJumpClusterIAMAuthRoleName

		rootBody.AppendBlock(aws.GenerateEc2UserDataInstanceResourceWithForEach(
			"jump_cluster",
			"data.aws_ami.red_hat_linux_ami.id",
			jumpClusterInstanceTypeVarName,
			jumpClusterBrokerSubnetIdsVarName,
			securityGroupIdsVarName,
			jumpClusterSshKeyPairNameVarName,
			"jump-cluster-with-cluster-links-user-data.tpl",
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
				"cluster_link_name":                          utils.TokensForVarReference(clusterLinkNameVarName),
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

func (mi *MigrationInfraHCLService) generateJumpClustersVariablesTf(request types.MigrationWizardRequest) string {
	requiredVariables := modules.GetJumpClusterModuleVariableDefinitions(request)
	return mi.generateVariablesTf(requiredVariables)
}

func (mi *MigrationInfraHCLService) generateJumpClustersOutputsTf() string {
	outputs := modules.GetJumpClusterModuleOutputDefinitions()
	return mi.generateOutputsTf(outputs)
}

func (mi *MigrationInfraHCLService) generateJumpClustersVersionsTf() string {
	return GenerateVersionsTf(aws.AddRequiredProvider)
}

// ============================================================================
// Networking Module Generation (Private)
// ============================================================================

func (mi *MigrationInfraHCLService) generateNetworkingMainTf(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Get variable names from module definitions
	vpcIdVarName := modules.VarVpcID
	jumpClusterBrokerSubnetCidrsVarName := modules.VarJumpClusterBrokerSubnetCidrs
	jumpClusterSetupHostSubnetCidrVarName := modules.VarJumpClusterSetupHostSubnetCidr

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

	rootBody.AppendBlock(aws.GenerateRouteTableResource("jump_cluster_setup_host_public_rt", aws.GetInternetGatewayReference(request.HasExistingInternetGateway, "internet_gateway"), vpcIdVarName))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableAssociationResource("jump_cluster_setup_host_public_rt_association", aws.GenerateSubnetResourceReference("jump_cluster_setup_host_subnet"), "aws_route_table.jump_cluster_setup_host_public_rt.id"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableResource("private_subnet_rt", "aws_nat_gateway.nat_gw.id", vpcIdVarName))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableAssociationResourceWithCount("jump_cluster_broker_route_table_assoc", aws.GenerateSubnetResourceReference("jump_cluster_broker_subnets"), "aws_route_table.private_subnet_rt.id"))
	rootBody.AppendNewline()

	existingVpceIdVarName := modules.VarExistingPrivateLinkVpceID
	rootBody.AppendBlock(aws.GenerateVpcEndpointDataSource("existing_vpce", existingVpceIdVarName))
	rootBody.AppendNewline()

	for _, port := range []int{80, 443, 9092} {
		rootBody.AppendBlock(aws.GenerateSecurityGroupIngressRule(
			fmt.Sprintf("vpce_ingress_from_jump_cluster_%d", port),
			port,
			"aws_security_group.security_group.id",
			"tolist(data.aws_vpc_endpoint.existing_vpce.security_group_ids)[0]",
		))
		rootBody.AppendNewline()
	}

	rootBody.AppendBlock(other.GenerateTLSPrivateKeyResource("jump_cluster_ssh_key", "RSA", 4096))
	rootBody.AppendNewline()

	rootBody.AppendBlock(other.GenerateLocalFileResource("jump_cluster_ssh_key_private_key", "tls_private_key.jump_cluster_ssh_key.private_key_pem", "./.ssh/jump_cluster_ssh_key_private_key_rsa", "400"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(other.GenerateLocalFileResource("jump_cluster_ssh_key_public_key", "tls_private_key.jump_cluster_ssh_key.public_key_openssh", "./.ssh/jump_cluster_ssh_key_public_key.pub", "400"))
	rootBody.AppendNewline()

	sshKeySuffix := mi.SSHKeySuffix
	if sshKeySuffix == "" {
		sshKeySuffix = utils.RandomString(5)
	}
	rootBody.AppendBlock(aws.GenerateKeyPairResource("jump_cluster_ssh_key", fmt.Sprintf("jump_cluster_ssh_key_%s", sshKeySuffix), "tls_private_key.jump_cluster_ssh_key.public_key_openssh"))
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
	return GenerateVersionsTf(aws.AddRequiredProvider)
}

// ============================================================================
// Root-Level Generation - Private Migration - External Outbound Cluster Link
// ============================================================================

func (mi *MigrationInfraHCLService) generateRootMainTfForExternalOutboundClusterLinkingInfrastructure(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	//
	// MSK Private Cluster Link Module
	//
	mskClusterLinkPrivateLinkModuleBlock := rootBody.AppendNewBlock("module", []string{"cluster-linking-aws-msk-private-link"})
	mskClusterLinkPrivateLinkModuleBody := mskClusterLinkPrivateLinkModuleBlock.Body()

	// https://github.com/confluentinc/cc-terraform-module-clusterlinking-outbound-private
	mskClusterLinkPrivateLinkModuleBody.SetAttributeValue("source", cty.StringVal("git::https://github.com/confluentinc/cc-terraform-module-clusterlinking-outbound-private.git"))
	mskClusterLinkPrivateLinkModuleBody.AppendNewline()

	mskClusterLinkPrivateLinkModuleBody.SetAttributeValue("name_prefix", cty.StringVal("msk"))
	mskClusterLinkPrivateLinkModuleBody.SetAttributeValue("use_aws", cty.BoolVal(true))
	mskClusterLinkPrivateLinkModuleBody.AppendNewline()

	mskClusterLinkPrivateLinkVars := modules.GetMskPrivateClusterLinkVariables()
	for _, varDef := range mskClusterLinkPrivateLinkVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		mskClusterLinkPrivateLinkModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForVarReference(varDef.Definition.Name))
	}
	rootBody.AppendNewline()

	//
	// External Outbound Cluster Link Module
	//
	extOutboundClModuleBlock := rootBody.AppendNewBlock("module", []string{"external_outbound_cluster_link"})
	extOutboundClModuleBody := extOutboundClModuleBlock.Body()

	extOutboundClModuleBody.SetAttributeValue("source", cty.StringVal("./external_outbound_cluster_link"))
	extOutboundClModuleBody.AppendNewline()

	externalOutboundClusterLinkVars := modules.GetExternalOutboundClusterLinkingVariables()
	for _, varDef := range externalOutboundClusterLinkVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		extOutboundClModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForVarReference(varDef.Name))
	}
	rootBody.AppendNewline()

	extOutboundClModuleBody.AppendNewline()
	extOutboundClModuleBody.SetAttributeRaw("depends_on", utils.TokensForList([]string{"module.cluster-linking-aws-msk-private-link"}))

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateRootProvidersTfForExternalOutboundClusterLinkingInfrastructure() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())
	requiredProvidersBody.SetAttributeRaw(confluent.GenerateRequiredProviderTokens())
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateProviderBlockWithVarAndDeploymentID(mi.DeploymentID))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateProviderBlock())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// ============================================================================
// External Outbound Cluster Linking Module Generation (Private)
// ============================================================================

func (mi *MigrationInfraHCLService) generateExternalOutboundClusterLinkMainTf() string {
	subnetIdVarName := modules.VarSubnetID
	securityGroupIdVarName := modules.VarSecurityGroupID
	targetClusterApiKeyVarName := modules.VarTargetClusterAPIKey
	targetClusterApiSecretVarName := modules.VarTargetClusterAPISecret
	targetClusterRestEndpointVarName := modules.VarTargetClusterRestEndpoint
	targetClusterIdVarName := modules.VarTargetClusterID
	clusterLinkNameVarName := modules.VarClusterLinkName
	mskClusterIdVarName := modules.VarMSKClusterID
	mskClusterBootstrapBrokersVarName := modules.VarMSKClusterBootstrapServers
	mskSaslScramUsernameVarName := modules.VarMSKSaslScramUsername
	mskSaslScramPasswordVarName := modules.VarMSKSaslScramPassword

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
		"external_outbound_cluster_link",
		"data.aws_ami.amzn_linux_ami.id",
		"t2.medium",
		subnetIdVarName,
		securityGroupIdVarName,
		"", // No keypair needed as user will never need to access instance.
		"create-external-outbound-cluster-link.tpl",
		false,
		map[string]hclwrite.Tokens{
			"target_cluster_api_key":        utils.TokensForVarReference(targetClusterApiKeyVarName),
			"target_cluster_api_secret":     utils.TokensForVarReference(targetClusterApiSecretVarName),
			"target_cluster_rest_endpoint":  utils.TokensForVarReference(targetClusterRestEndpointVarName),
			"target_cluster_id":             utils.TokensForVarReference(targetClusterIdVarName),
			"cluster_link_name":             utils.TokensForVarReference(clusterLinkNameVarName),
			"msk_cluster_id":                utils.TokensForVarReference(mskClusterIdVarName),
			"msk_cluster_bootstrap_brokers": utils.TokensForVarReference(mskClusterBootstrapBrokersVarName),
			"msk_sasl_scram_username":       utils.TokensForVarReference(mskSaslScramUsernameVarName),
			"msk_sasl_scram_password":       utils.TokensForVarReference(mskSaslScramPasswordVarName),
		},
		nil,
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateExternalOutboundClusterLinkVariablesTf(request types.MigrationWizardRequest) string {
	return mi.generateVariablesTf(modules.GetExternalOutboundClusterLinkingModuleVariableDefinitions(request))
}

func (mi *MigrationInfraHCLService) generateCreateExternalOutboundClusterLinkTpl() string {
	return aws.GenerateCreateExternalOutboundClusterLinkTpl()
}

// ============================================================================
// Shared/Utility Functions
// ============================================================================

func (mi *MigrationInfraHCLService) generateVariablesTf(tfVariables []types.TerraformVariable) string {
	return GenerateVariablesTf(tfVariables)
}

func (mi *MigrationInfraHCLService) generateOutputsTf(tfOutputs []types.TerraformOutput) string {
	return GenerateOutputsTf(tfOutputs)
}

func (mi *MigrationInfraHCLService) generateInputsAutoTfvars(request types.MigrationWizardRequest) string {
	values := modules.GetMigrationInfraRootVariableValues(request)
	return GenerateInputsAutoTfvarsWithBrokers(values)
}
