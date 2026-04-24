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

	WriteModuleInputs(moduleBody, modules.GetClusterLinkVariables(), request)

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
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(confluent.GenerateClusterLinkLocals(
		modules.VarConfluentCloudClusterAPIKey,
		modules.VarConfluentCloudClusterAPISecret,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateClusterLinkResource(
		"confluent_cluster_link",
		modules.VarMSKClusterID,
		modules.VarTargetClusterID,
		modules.VarTargetClusterRestEndpoint,
		modules.VarClusterLinkName,
		modules.VarMSKSaslScramBootstrapServers,
		modules.VarMSKSaslScramMechanism,
		modules.VarMSKSaslScramUsername,
		modules.VarMSKSaslScramPassword,
	))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateClusterLinkVariablesTf(request types.MigrationWizardRequest) string {
	return GenerateVariablesTf(modules.GetClusterLinkModuleVariableDefinitions(request))
}
