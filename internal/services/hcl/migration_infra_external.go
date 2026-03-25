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

	WriteModuleInputs(mskClusterLinkPrivateLinkModuleBody, modules.GetMskPrivateClusterLinkVariables(), request)
	rootBody.AppendNewline()

	//
	// External Outbound Cluster Link Module
	//
	extOutboundClModuleBlock := rootBody.AppendNewBlock("module", []string{"external_outbound_cluster_link"})
	extOutboundClModuleBody := extOutboundClModuleBlock.Body()

	extOutboundClModuleBody.SetAttributeValue("source", cty.StringVal("./external_outbound_cluster_link"))
	extOutboundClModuleBody.AppendNewline()

	WriteModuleInputs(extOutboundClModuleBody, modules.GetExternalOutboundClusterLinkingVariables(), request)
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

func (mi *MigrationInfraHCLService) generateExternalOutboundClusterLinkMainTf(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(aws.GenerateAmiDataResource("amzn_linux_ami", "137112412989", true, map[string]string{
		"name":                "al2023-ami-2023.*-kernel-6.1-x86_64",
		"state":               "available",
		"architecture":        "x86_64",
		"virtualization-type": "hvm",
	}))
	rootBody.AppendNewline()

	templateName := "create-external-outbound-cluster-link.tpl"
	userDataArgs := map[string]hclwrite.Tokens{
		"confluent_cloud_cluster_api_key":    utils.TokensForVarReference(modules.VarConfluentCloudClusterAPIKey),
		"confluent_cloud_cluster_api_secret": utils.TokensForVarReference(modules.VarConfluentCloudClusterAPISecret),
		"target_cluster_rest_endpoint":  utils.TokensForVarReference(modules.VarTargetClusterRestEndpoint),
		"target_cluster_id":             utils.TokensForVarReference(modules.VarTargetClusterID),
		"cluster_link_name":             utils.TokensForVarReference(modules.VarClusterLinkName),
		"msk_cluster_id":                utils.TokensForVarReference(modules.VarMSKClusterID),
		"msk_cluster_bootstrap_brokers": utils.TokensForVarReference(modules.VarMSKClusterBootstrapServers),
	}

	if request.MskJumpClusterAuthType == "unauth_tls" {
		templateName = "create-external-outbound-cluster-link-unauth-tls.tpl"
	} else {
		userDataArgs["msk_sasl_scram_username"] = utils.TokensForVarReference(modules.VarMSKSaslScramUsername)
		userDataArgs["msk_sasl_scram_password"] = utils.TokensForVarReference(modules.VarMSKSaslScramPassword)
	}

	rootBody.AppendBlock(aws.GenerateEc2UserDataInstanceResource(
		"external_outbound_cluster_link",
		"data.aws_ami.amzn_linux_ami.id",
		"t2.medium",
		modules.VarSubnetID,
		modules.VarSecurityGroupID,
		"", // No keypair needed as user will never need to access instance.
		templateName,
		false,
		userDataArgs,
		nil,
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateExternalOutboundClusterLinkVariablesTf(request types.MigrationWizardRequest) string {
	return GenerateVariablesTf(modules.GetExternalOutboundClusterLinkingModuleVariableDefinitions(request))
}

func (mi *MigrationInfraHCLService) externalOutboundClusterLinkTemplateFileName(request types.MigrationWizardRequest) string {
	if request.MskJumpClusterAuthType == "unauth_tls" {
		return "create-external-outbound-cluster-link-unauth-tls.tpl"
	}
	return "create-external-outbound-cluster-link.tpl"
}

func (mi *MigrationInfraHCLService) generateCreateExternalOutboundClusterLinkTpl(request types.MigrationWizardRequest) string {
	if request.MskJumpClusterAuthType == "unauth_tls" {
		return aws.GenerateCreateExternalOutboundClusterLinkUnauthTlsTpl()
	}
	return aws.GenerateCreateExternalOutboundClusterLinkTpl()
}
