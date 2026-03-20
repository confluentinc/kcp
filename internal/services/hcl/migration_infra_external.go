package hcl

import (
	"github.com/confluentinc/kcp/internal/services/hcl/aws"
	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
	"github.com/confluentinc/kcp/internal/services/hcl/modules"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

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
