package hcl

import (
	"github.com/confluentinc/kcp/internal/services/hcl/modules"
	"github.com/confluentinc/kcp/internal/types"
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
	requiredVariables := modules.GetMigrationInfraRootVariableDefinitions(request)

	return types.MigrationInfraTerraformProject{
		MainTf:           mi.generateRootMainTfForPublicMigrationInfrastructure(request),
		ProvidersTf:      mi.generateRootProvidersTfForClusterLink(),
		VariablesTf:      GenerateVariablesTf(requiredVariables),
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
	requiredVariables := modules.GetMigrationInfraRootVariableDefinitions(request)

	return types.MigrationInfraTerraformProject{
		MainTf:           mi.generateRootMainTfForPrivateMigrationInfrastructure(request),
		ProvidersTf:      mi.generateRootProvidersTfForPrivateMigrationInfrastructure(),
		VariablesTf:      GenerateVariablesTf(requiredVariables),
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
	requiredVariables := modules.GetMigrationInfraRootVariableDefinitions(request)

	return types.MigrationInfraTerraformProject{
		MainTf:           mi.generateRootMainTfForExternalOutboundClusterLinkingInfrastructure(request),
		ProvidersTf:      mi.generateRootProvidersTfForExternalOutboundClusterLinkingInfrastructure(),
		VariablesTf:      GenerateVariablesTf(requiredVariables),
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

func (mi *MigrationInfraHCLService) generateInputsAutoTfvars(request types.MigrationWizardRequest) string {
	return GenerateInputsAutoTfvars(modules.GetMigrationInfraRootVariableValues(request))
}
