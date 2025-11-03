package hcl

import (
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

func (mi *MigrationInfraHCLService) GenerateTerraformModules(request types.MigrationWizardRequest) types.TerraformModules {
	if request.HasPublicCCEndpoints {
		return mi.handleClusterLink(request)
	}
	return mi.handlePrivateLink(request)
}

func (mi *MigrationInfraHCLService) handleClusterLink(request types.MigrationWizardRequest) types.TerraformModules {
	return types.TerraformModules{
		"root": {
			MainTf:      mi.generateRootMainTf(),
			ProvidersTf: mi.generateRootProvidersTf(),
			VariablesTf: mi.generateRootVariablesTf(),
		},
		"cluster_link": {
			MainTf:      mi.generateClusterLinkMainTf(request),
			VariablesTf: mi.generateClusterLinkVariablesTf(),
		},
	}
}

func (mi *MigrationInfraHCLService) handlePrivateLink(request types.MigrationWizardRequest) types.TerraformModules {
	return types.TerraformModules{
		"root": {
			MainTf:      "",
			ProvidersTf: "",
			VariablesTf: "",
		},
		"ansible_control_node_instance": {
			MainTf:      "",
			VariablesTf: "",
			OutputsTf:   "",
		},
		"confluent_platform_broker_instances": {
			MainTf:      "",
			VariablesTf: "",
			OutputsTf:   "",
		},
		"networking": {
			MainTf:      "",
			VariablesTf: "",
			OutputsTf:   "",
		},
		"private_link_connection": {
			MainTf:      "",
			VariablesTf: "",
			OutputsTf:   "",
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
