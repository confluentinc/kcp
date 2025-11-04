package hcl

import (
	"github.com/confluentinc/kcp/internal/services/hcl/aws"
	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
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
	if request.HasPublicCCEndpoints {
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
	requiredVariables := append(aws.AnsibleControlNodeVariables, providerVariables...)

	return types.MigrationInfraTerraformProject{
		MainTf:      mi.generateRootMainTfForPrivateLink(),
		ProvidersTf: mi.generateRootProvidersTfForPrivateLink(),
		VariablesTf: mi.generateVariablesTf(requiredVariables),
		Modules: []types.MigrationInfraTerraformModule{
			{
				Name:        "ansible_control_node_instance",
				MainTf:      mi.generateAnsibleControlNodeMainTf(),
				VariablesTf: mi.generateAnsibleControlNodeVariablesTf(),
				VersionsTf:  mi.generateAnsibleControlNodeVersionsTf(),
				AdditionalFiles: map[string]string{
					"ansible-control-node-user-data.tpl": mi.generateAnsibleControlNodeUserDataTpl(),
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
				MainTf:      mi.generateNetworkingMainTf(),
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
// Ansible Control Node Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generateAnsibleControlNodeMainTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(aws.GenerateAmazonLinuxAMI())
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateAnsibleControlNodeInstance())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateAnsibleControlNodeUserDataTpl() string {
	return aws.GenerateAnsibleControlNodeInstanceUserDataTpl()
}

func (mi *MigrationInfraHCLService) generateAnsibleControlNodeVariablesTf() string {
	return mi.generateVariablesTf(aws.AnsibleControlNodeVariables)
}

func (mi *MigrationInfraHCLService) generateAnsibleControlNodeVersionsTf() string {
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

func (mi *MigrationInfraHCLService) generateNetworkingMainTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// rootBody.AppendBlock(aws.GenerateAmazonLinuxAMI())
	rootBody.AppendNewline()

	// rootBody.AppendBlock(aws.GenerateAnsibleControlNodeInstance())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateNetworkingVariablesTf() string {
	return ""
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
