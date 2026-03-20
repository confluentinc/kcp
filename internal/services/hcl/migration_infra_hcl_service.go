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
	if request.HasPublicEndpoints {
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
					"jump-cluster-with-cluster-links-user-data.tpl": mi.generateJumpClusterClusterLinksUserDataTpl(request.JumpClusterAuthType),
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
	ccClusterKeyVarName := modules.GetModuleVariableName("cluster_link", "confluent_cloud_cluster_api_key")
	ccClusterSecretVarName := modules.GetModuleVariableName("cluster_link", "confluent_cloud_cluster_api_secret")
	sourceClusterIdVarName := modules.GetModuleVariableName("cluster_link", "source_cluster_id")
	targetClusterIdVarName := modules.GetModuleVariableName("cluster_link", "target_cluster_id")
	targetClusterRestEndpointVarName := modules.GetModuleVariableName("cluster_link", "target_cluster_rest_endpoint")
	clusterLinkVarName := modules.GetModuleVariableName("cluster_link", "cluster_link_name")
	sourceSaslScramBootstrapServersVarName := modules.GetModuleVariableName("cluster_link", "source_sasl_scram_bootstrap_servers")
	sourceSaslScramMechanismVarName := modules.GetModuleVariableName("cluster_link", "source_sasl_scram_mechanism")
	sourceSaslScramUsernameVarName := modules.GetModuleVariableName("cluster_link", "source_sasl_scram_username")
	sourceSaslScramPasswordVarName := modules.GetModuleVariableName("cluster_link", "source_sasl_scram_password")

	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(confluent.GenerateClusterLinkLocals(
		ccClusterKeyVarName,
		ccClusterSecretVarName,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateClusterLinkResource(
		"confluent_cluster_link",
		sourceClusterIdVarName,
		targetClusterIdVarName,
		targetClusterRestEndpointVarName,
		clusterLinkVarName,
		sourceSaslScramBootstrapServersVarName,
		sourceSaslScramMechanismVarName,
		sourceSaslScramUsernameVarName,
		sourceSaslScramPasswordVarName,
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

	rootBody.AppendBlock(aws.GenerateProviderBlockWithVar())
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

	if request.JumpClusterAuthType == "sasl_scram" {
		credentialsSection += `
| ` + "`source_sasl_scram_username`" + ` | SASL/SCRAM username for source Kafka cluster authentication |
| ` + "`source_sasl_scram_password`" + ` | SASL/SCRAM password for source Kafka cluster authentication |`
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
	jumpClusterSetupHostSubnetIdVarName := modules.GetModuleVariableName("jump_cluster_setup_host", "jump_cluster_setup_host_subnet_id")
	securityGroupIdsVarName := modules.GetModuleVariableName("jump_cluster_setup_host", "jump_cluster_security_group_ids")
	jumpClusterSshKeyPairNameVarName := modules.GetModuleVariableName("jump_cluster_setup_host", "jump_cluster_ssh_key_pair_name")
	jumpClusterInstancesPrivateDnsVarName := modules.GetModuleVariableName("jump_cluster_setup_host", "jump_cluster_instances_private_dns")
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
			"broker_ips":  utils.TokensForVarReference(jumpClusterInstancesPrivateDnsVarName),
			"private_key": utils.TokensForVarReference(privateKeyVarName),
		},
		nil,
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostUserDataTpl(request types.MigrationWizardRequest) string {
	if request.JumpClusterAuthType == "sasl_scram" {
		return aws.GenerateJumpClusterSaslScramSetupHostUserDataTpl()
	} else {
		return aws.GenerateJumpClusterSaslIamSetupHostUserDataTpl()
	}
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
	jumpClusterBrokerSubnetIdsVarName := modules.GetModuleVariableName("jump_cluster", "jump_cluster_broker_subnet_ids")
	securityGroupIdsVarName := modules.GetModuleVariableName("jump_cluster", "jump_cluster_security_group_ids")
	jumpClusterSshKeyPairNameVarName := modules.GetModuleVariableName("jump_cluster", "jump_cluster_ssh_key_pair_name")
	jumpClusterInstanceTypeVarName := modules.GetModuleVariableName("jump_cluster", "jump_cluster_instance_type")
	jumpClusterBrokerStorageVarName := modules.GetModuleVariableName("jump_cluster", "jump_cluster_broker_storage")
	targetClusterIdVarName := modules.GetModuleVariableName("jump_cluster", "confluent_cloud_cluster_id")
	targetBootstrapEndpointVarName := modules.GetModuleVariableName("jump_cluster", "confluent_cloud_cluster_bootstrap_endpoint")
	targetRestEndpointVarName := modules.GetModuleVariableName("jump_cluster", "confluent_cloud_cluster_rest_endpoint")
	targetApiKeyVarName := modules.GetModuleVariableName("jump_cluster", "confluent_cloud_cluster_api_key")
	targetApiSecretVarName := modules.GetModuleVariableName("jump_cluster", "confluent_cloud_cluster_api_secret")
	sourceClusterIdVarName := modules.GetModuleVariableName("jump_cluster", "source_cluster_id")
	sourceBootstrapBrokersVarName := modules.GetModuleVariableName("jump_cluster", "source_cluster_bootstrap_brokers")
	clusterLinkNameVarName := modules.GetModuleVariableName("jump_cluster", "cluster_link_name")

	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(aws.GenerateAmiDataResource("red_hat_linux_ami", "309956199498", true, map[string]string{
		"name":                "RHEL-9.6.0_HVM_GA-*",
		"state":               "available",
		"architecture":        "x86_64",
		"virtualization-type": "hvm",
	}))
	rootBody.AppendNewline()

	if request.JumpClusterAuthType == "sasl_scram" {
		sourceSaslScramMechanismVarName := modules.GetModuleVariableName("jump_cluster", "source_sasl_scram_mechanism")
		sourceSaslScramUsernameVarName := modules.GetModuleVariableName("jump_cluster", "source_sasl_scram_username")
		sourceSaslScramPasswordVarName := modules.GetModuleVariableName("jump_cluster", "source_sasl_scram_password")

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
				"source_cluster_id":                          utils.TokensForVarReference(sourceClusterIdVarName),
				"source_cluster_bootstrap_brokers":           utils.TokensForVarReference(sourceBootstrapBrokersVarName),
				"source_sasl_scram_mechanism":                utils.TokensForVarReference(sourceSaslScramMechanismVarName),
				"source_sasl_scram_username":                 utils.TokensForVarReference(sourceSaslScramUsernameVarName),
				"source_sasl_scram_password":                 utils.TokensForVarReference(sourceSaslScramPasswordVarName),
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
		jumpClusterIamAuthRoleNameVarName := modules.GetModuleVariableName("jump_cluster", "jump_cluster_iam_auth_role_name")

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
				"source_cluster_id":                          utils.TokensForVarReference(sourceClusterIdVarName),
				"source_cluster_bootstrap_brokers":           utils.TokensForVarReference(sourceBootstrapBrokersVarName),
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

	existingVpceIdVarName := modules.GetModuleVariableName("networking", "existing_private_link_vpce_id")
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
// Root-Level Generation - Private Migration - External Outbound Cluster Link
// ============================================================================

func (mi *MigrationInfraHCLService) generateRootMainTfForExternalOutboundClusterLinkingInfrastructure(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	//
	// Private Cluster Link Module
	//
	namePrefix := "msk"
	if request.SourceType == "osk" {
		namePrefix = "osk"
	}
	moduleName := "cluster-linking-aws-" + namePrefix + "-private-link"

	privateLinkModuleBlock := rootBody.AppendNewBlock("module", []string{moduleName})
	privateLinkModuleBody := privateLinkModuleBlock.Body()

	// https://github.com/confluentinc/cc-terraform-module-clusterlinking-outbound-private
	privateLinkModuleBody.SetAttributeValue("source", cty.StringVal("git::https://github.com/confluentinc/cc-terraform-module-clusterlinking-outbound-private.git"))
	privateLinkModuleBody.AppendNewline()

	privateLinkModuleBody.SetAttributeValue("name_prefix", cty.StringVal(namePrefix))
	privateLinkModuleBody.SetAttributeValue("use_aws", cty.BoolVal(true))
	privateLinkModuleBody.AppendNewline()

	privateClusterLinkVars := modules.GetPrivateClusterLinkVariables()
	for _, varDef := range privateClusterLinkVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		privateLinkModuleBody.SetAttributeRaw(varDef.Name, utils.TokensForVarReference(varDef.Definition.Name))
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
	extOutboundClModuleBody.SetAttributeRaw("depends_on", utils.TokensForList([]string{"module." + moduleName}))

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

	rootBody.AppendBlock(aws.GenerateProviderBlockWithVar())
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateProviderBlock())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// ============================================================================
// External Outbound Cluster Linking Module Generation (Private)
// ============================================================================

func (mi *MigrationInfraHCLService) generateExternalOutboundClusterLinkMainTf() string {
	subnetIdVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "subnet_id")
	securityGroupIdVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "security_group_id")
	targetClusterApiKeyVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "target_cluster_api_key")
	targetClusterApiSecretVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "target_cluster_api_secret")
	targetClusterRestEndpointVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "target_cluster_rest_endpoint")
	targetClusterIdVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "target_cluster_id")
	clusterLinkNameVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "cluster_link_name")
	sourceClusterIdVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "source_cluster_id")
	sourceClusterBootstrapBrokersVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "source_cluster_bootstrap_servers")
	sourceSaslScramMechanismVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "source_sasl_scram_mechanism")
	sourceSaslScramUsernameVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "source_sasl_scram_username")
	sourceSaslScramPasswordVarName := modules.GetModuleVariableName("ext_outbound_cluster_link", "source_sasl_scram_password")

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
			"target_cluster_api_key":           utils.TokensForVarReference(targetClusterApiKeyVarName),
			"target_cluster_api_secret":        utils.TokensForVarReference(targetClusterApiSecretVarName),
			"target_cluster_rest_endpoint":     utils.TokensForVarReference(targetClusterRestEndpointVarName),
			"target_cluster_id":                utils.TokensForVarReference(targetClusterIdVarName),
			"cluster_link_name":                utils.TokensForVarReference(clusterLinkNameVarName),
			"source_cluster_id":                utils.TokensForVarReference(sourceClusterIdVarName),
			"source_cluster_bootstrap_brokers": utils.TokensForVarReference(sourceClusterBootstrapBrokersVarName),
			"source_sasl_scram_mechanism":      utils.TokensForVarReference(sourceSaslScramMechanismVarName),
			"source_sasl_scram_username":       utils.TokensForVarReference(sourceSaslScramUsernameVarName),
			"source_sasl_scram_password":       utils.TokensForVarReference(sourceSaslScramPasswordVarName),
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

// func (mi *MigrationInfraHCLService) generateExternalOutboundClusterLinkingVersionsTf() string {
// 	f := hclwrite.NewEmptyFile()
// 	rootBody := f.Body()

// 	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
// 	terraformBody := terraformBlock.Body()

// 	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
// 	requiredProvidersBody := requiredProvidersBlock.Body()

// 	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())

// 	return string(f.Bytes())
// }

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

	varSeenVariables := make(map[string]bool)
	for varName, value := range values {
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
		case []types.ExtOutboundClusterKafkaBroker:
			brokerObjects := make([]cty.Value, len(v))
			for i, broker := range v {
				endpoints := make([]cty.Value, len(broker.Endpoints))
				for j, endpoint := range broker.Endpoints {
					endpoints[j] = cty.ObjectVal(map[string]cty.Value{
						"host": cty.StringVal(endpoint.Host),
						"port": cty.NumberIntVal(int64(endpoint.Port)),
						"ip":   cty.StringVal(endpoint.IP),
					})
				}
				brokerObjects[i] = cty.ObjectVal(map[string]cty.Value{
					"id":        cty.StringVal(broker.ID),
					"subnet_id": cty.StringVal(broker.SubnetID),
					"endpoints": cty.ListVal(endpoints),
				})
			}
			rootBody.SetAttributeValue(varName, cty.ListVal(brokerObjects))
		}
	}

	return string(f.Bytes())
}
