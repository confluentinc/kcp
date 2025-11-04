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
		MainTf:      mi.generateRootMainTf(),
		ProvidersTf: mi.generateRootProvidersTf(),
		VariablesTf: mi.generateRootVariablesTf(),
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
	return types.MigrationInfraTerraformProject{
		MainTf:      "",
		ProvidersTf: "",
		VariablesTf: "",
		Modules: []types.MigrationInfraTerraformModule{
			{
				Name:        "ansible_control_node_instance",
				MainTf:      mi.generateAnsibleControlNodeInstanceMainTf(),
				VariablesTf: mi.generateAnsibleControlNodeInstanceVariablesTf(),
				OutputsTf:   mi.generateAnsibleControlNodeInstanceOutputsTf(),
				AdditionalFiles: map[string]string{
					// todo - how do we want to do this?
					"ansible-control-node-user-data.tpl": "",
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

func (mi *MigrationInfraHCLService) generateRootMainTf() string {
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

func (mi *MigrationInfraHCLService) generateRootProvidersTf() string {
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

func (mi *MigrationInfraHCLService) generateRootVariablesTf() string {
	return mi.generateVariablesTf(confluent.ClusterLinkVariables)
}

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

func (mi *MigrationInfraHCLService) generateVariablesTf(tfVariables []types.TerraformVariable) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	for _, v := range tfVariables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.Name})
		variableBody := variableBlock.Body()
		variableBody.SetAttributeRaw("type", utils.TokensForResourceReference("string"))
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

// temp

// ansible
func (mi *MigrationInfraHCLService) generateAnsibleControlNodeInstanceMainTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(aws.GenerateAmazonLinuxAMI())
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateAnsibleControlNodeInstance())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateAnsibleControlNodeInstanceVariablesTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generateAnsibleControlNodeInstanceOutputsTf() string {
	return ""
}

// cp

func (mi *MigrationInfraHCLService) generateConfluentPlatformBrokerInstancesMainTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generateConfluentPlatformBrokerInstancesVariablesTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generateConfluentPlatformBrokerInstancesOutputsTf() string {
	return ""
}

// networking

func (mi *MigrationInfraHCLService) generateNetworkingMainTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generateNetworkingVariablesTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generateNetworkingOutputsTf() string {
	return ""
}

// private link

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionMainTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionVariablesTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionOutputsTf() string {
	return ""
}
