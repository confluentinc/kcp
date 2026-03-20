package hcl

import (
	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
	"github.com/confluentinc/kcp/internal/services/hcl/modules"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

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
			SetModuleRef(moduleBody, varDef.Name, varDef.FromModuleOutput, varDef.Name)
		} else {
			SetVarRef(moduleBody, varDef.Name, varDef.Name)
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
