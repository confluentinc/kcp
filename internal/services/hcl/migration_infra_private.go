package hcl

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/hcl/aws"
	"github.com/confluentinc/kcp/internal/services/hcl/modules"
	"github.com/confluentinc/kcp/internal/services/hcl/other"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

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

	WriteModuleInputs(networkingModuleBody, modules.GetNetworkingVariables(), request)
	rootBody.AppendNewline()

	setupHostModuleBlock := rootBody.AppendNewBlock("module", []string{"jump_cluster_setup_host"})
	setupHostModuleBody := setupHostModuleBlock.Body()
	setupHostModuleBody.SetAttributeValue("source", cty.StringVal("./jump_cluster_setup_host"))
	setupHostModuleBody.AppendNewline()

	setupHostModuleBody.SetAttributeRaw("providers", utils.TokensForMap(map[string]hclwrite.Tokens{
		"aws": utils.TokensForResourceReference("aws"),
	}))
	setupHostModuleBody.AppendNewline()

	WriteModuleInputs(setupHostModuleBody, modules.GetJumpClusterSetupHostVariables(), request)
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

	WriteModuleInputs(jumpClustersModuleBody, modules.GetJumpClusterVariables(), request)
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

	switch request.MskJumpClusterAuthType {
	case "sasl_scram":
		credentialsSection += `
| ` + "`msk_sasl_scram_username`" + ` | SASL/SCRAM username for MSK authentication |
| ` + "`msk_sasl_scram_password`" + ` | SASL/SCRAM password for MSK authentication |`
	case "unauth_tls":
		// No additional credentials needed for unauthenticated TLS
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
		modules.VarJumpClusterSetupHostSubnetID,
		modules.VarJumpClusterSecurityGroupIDs,
		modules.VarJumpClusterSSHKeyPairName,
		"jump-cluster-setup-host-user-data.tpl",
		true,
		map[string]hclwrite.Tokens{
			"broker_ips":  utils.TokensForVarReference(modules.VarJumpClusterInstancesPrivateDNS),
			"private_key": utils.TokensForVarReference(modules.VarPrivateKey),
		},
		nil,
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostUserDataTpl(request types.MigrationWizardRequest) string {
	switch request.MskJumpClusterAuthType {
	case "sasl_scram", "unauth_tls":
		return aws.GenerateJumpClusterSaslScramSetupHostUserDataTpl()
	default:
		return aws.GenerateJumpClusterSaslIamSetupHostUserDataTpl()
	}
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostVariablesTf(request types.MigrationWizardRequest) string {
	return GenerateVariablesTf(modules.GetJumpClusterSetupHostVariableDefinitions(request))
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostVersionsTf() string {
	return GenerateVersionsTf(aws.AddRequiredProvider)
}

// ============================================================================
// Jump Cluster Module Generation (Private)
// ============================================================================

func (mi *MigrationInfraHCLService) generateJumpClustersMainTf(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(aws.GenerateAmiDataResource("red_hat_linux_ami", "309956199498", true, map[string]string{
		"name":                "RHEL-9.6.0_HVM_GA-*",
		"state":               "available",
		"architecture":        "x86_64",
		"virtualization-type": "hvm",
	}))
	rootBody.AppendNewline()

	commonUserDataArgs := map[string]hclwrite.Tokens{
		"confluent_cloud_cluster_id":                 utils.TokensForVarReference(modules.VarConfluentCloudClusterID),
		"confluent_cloud_cluster_bootstrap_endpoint": utils.TokensForVarReference(modules.VarConfluentCloudClusterBootstrapEndpoint),
		"confluent_cloud_cluster_rest_endpoint":      utils.TokensForVarReference(modules.VarConfluentCloudClusterRestEndpoint),
		"confluent_cloud_cluster_key":                utils.TokensForVarReference(modules.VarConfluentCloudClusterAPIKey),
		"confluent_cloud_cluster_secret":             utils.TokensForVarReference(modules.VarConfluentCloudClusterAPISecret),
		"msk_cluster_id":                             utils.TokensForVarReference(modules.VarMSKClusterID),
		"msk_cluster_bootstrap_brokers":              utils.TokensForVarReference(modules.VarMSKClusterBootstrapBrokers),
		"cluster_link_name":                          utils.TokensForVarReference(modules.VarClusterLinkName),
	}

	optionalBlocks := aws.OptionalBlocksConfig{
		"root_block_device": {
			"volume_size": utils.TokensForVarReference(modules.VarJumpClusterBrokerStorage),
			"volume_type": cty.StringVal("gp3"),
		},
		"metadata_options": {
			"http_tokens":                 cty.StringVal("required"),
			"http_put_response_hop_limit": cty.NumberIntVal(10),
		},
	}

	switch request.MskJumpClusterAuthType {
	case "sasl_scram":
		commonUserDataArgs["msk_sasl_scram_username"] = utils.TokensForVarReference(modules.VarMSKSaslScramUsername)
		commonUserDataArgs["msk_sasl_scram_password"] = utils.TokensForVarReference(modules.VarMSKSaslScramPassword)
		rootBody.AppendBlock(aws.GenerateEc2UserDataInstanceResourceWithForEach(
			"jump_cluster",
			"data.aws_ami.red_hat_linux_ami.id",
			modules.VarJumpClusterInstanceType,
			modules.VarJumpClusterBrokerSubnetIDs,
			modules.VarJumpClusterSecurityGroupIDs,
			modules.VarJumpClusterSSHKeyPairName,
			"jump-cluster-with-cluster-links-user-data.tpl",
			"",
			false,
			commonUserDataArgs,
			optionalBlocks,
		))
	case "unauth_tls":
		rootBody.AppendBlock(aws.GenerateEc2UserDataInstanceResourceWithForEach(
			"jump_cluster",
			"data.aws_ami.red_hat_linux_ami.id",
			modules.VarJumpClusterInstanceType,
			modules.VarJumpClusterBrokerSubnetIDs,
			modules.VarJumpClusterSecurityGroupIDs,
			modules.VarJumpClusterSSHKeyPairName,
			"jump-cluster-with-cluster-links-user-data.tpl",
			"",
			false,
			commonUserDataArgs,
			optionalBlocks,
		))
	default: // iam
		rootBody.AppendBlock(aws.GenerateEc2UserDataInstanceResourceWithForEach(
			"jump_cluster",
			"data.aws_ami.red_hat_linux_ami.id",
			modules.VarJumpClusterInstanceType,
			modules.VarJumpClusterBrokerSubnetIDs,
			modules.VarJumpClusterSecurityGroupIDs,
			modules.VarJumpClusterSSHKeyPairName,
			"jump-cluster-with-cluster-links-user-data.tpl",
			modules.VarJumpClusterIAMAuthRoleName,
			false,
			commonUserDataArgs,
			optionalBlocks,
		))
	}
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateJumpClusterClusterLinksUserDataTpl(authType string) string {
	switch authType {
	case "sasl_scram":
		return aws.GenerateJumpClusterWithSaslScramClusterLinksUserDataTpl()
	case "unauth_tls":
		return aws.GenerateJumpClusterWithUnauthTlsClusterLinksUserDataTpl()
	default:
		return aws.GenerateJumpClusterWithIamClusterLinksUserDataTpl()
	}
}

func (mi *MigrationInfraHCLService) generateJumpClustersVariablesTf(request types.MigrationWizardRequest) string {
	return GenerateVariablesTf(modules.GetJumpClusterModuleVariableDefinitions(request))
}

func (mi *MigrationInfraHCLService) generateJumpClustersOutputsTf() string {
	return GenerateOutputsTf(modules.GetJumpClusterModuleOutputDefinitions())
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

	if request.HasExistingInternetGateway {
		rootBody.AppendBlock(aws.GenerateInternetGatewayDataSource("internet_gateway", modules.VarVpcID))
	} else {
		rootBody.AppendBlock(aws.GenerateInternetGatewayResource("internet_gateway", modules.VarVpcID))
	}
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateAvailabilityZonesDataSource("available"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSecurityGroup("security_group", []int{22, 9091, 9092, 9093, 8090, 8081}, []int{0}, modules.VarVpcID))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSubnetResourceWithCount(
		"jump_cluster_broker_subnets",
		modules.VarJumpClusterBrokerSubnetCidrs,
		"data.aws_availability_zones.available",
		modules.VarVpcID,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSubnetResource(
		"jump_cluster_setup_host_subnet",
		modules.VarJumpClusterSetupHostSubnetCidr,
		"data.aws_availability_zones.available.names[0]",
		modules.VarVpcID,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateEIPResource("nat_eip"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateNATGatewayResource("nat_gw", "aws_eip.nat_eip.id", "aws_subnet.jump_cluster_setup_host_subnet.id"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableResource("jump_cluster_setup_host_public_rt", aws.GetInternetGatewayReference(request.HasExistingInternetGateway, "internet_gateway"), modules.VarVpcID))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableAssociationResource("jump_cluster_setup_host_public_rt_association", aws.GenerateSubnetResourceReference("jump_cluster_setup_host_subnet"), "aws_route_table.jump_cluster_setup_host_public_rt.id"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableResource("private_subnet_rt", "aws_nat_gateway.nat_gw.id", modules.VarVpcID))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableAssociationResourceWithCount("jump_cluster_broker_route_table_assoc", aws.GenerateSubnetResourceReference("jump_cluster_broker_subnets"), "aws_route_table.private_subnet_rt.id"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateVpcEndpointDataSource("existing_vpce", modules.VarExistingPrivateLinkVpceID))
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
	return GenerateVariablesTf(modules.GetNetworkingModuleVariableDefinitions(request))
}

func (mi *MigrationInfraHCLService) generateNetworkingOutputsTf() string {
	return GenerateOutputsTf(modules.GetNetworkingModuleOutputDefinitions())
}

func (mi *MigrationInfraHCLService) generateNetworkingVersionsTf() string {
	return GenerateVersionsTf(aws.AddRequiredProvider)
}

