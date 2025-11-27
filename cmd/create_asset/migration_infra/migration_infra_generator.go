package migration_infra

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrationInfraOpts struct {
	VpcId            string
	Region           string
	MskClusterId     string
	BootstrapBrokers string

	ClusterLinkName     string
	TargetEnvironmentId string
	TargetClusterId     string
	TargetRestEndpoint  string
	SubnetId            string
	SecurityGroupId     string
	ExtOutboundBrokers  []types.ExtOutboundClusterKafkaBroker

	MigrationType types.MigrationType
}

type MigrationInfraAssetGenerator struct {
	vpcId            string
	region           string
	mskClusterId     string
	bootstrapBrokers string

	clusterLinkName     string
	targetEnvironmentId string
	targetClusterId     string
	targetRestEndpoint  string
	subnetId            string
	securityGroupId     string
	extOutboundBrokers  []types.ExtOutboundClusterKafkaBroker

	migrationType types.MigrationType
}

func NewMigrationInfraAssetGenerator(opts MigrationInfraOpts) *MigrationInfraAssetGenerator {
	return &MigrationInfraAssetGenerator{
		vpcId:               opts.VpcId,
		region:              opts.Region,
		mskClusterId:        opts.MskClusterId,
		bootstrapBrokers:    opts.BootstrapBrokers,
		clusterLinkName:     opts.ClusterLinkName,
		targetEnvironmentId: opts.TargetEnvironmentId,
		targetClusterId:     opts.TargetClusterId,
		targetRestEndpoint:  opts.TargetRestEndpoint,
		subnetId:            opts.SubnetId,
		securityGroupId:     opts.SecurityGroupId,
		extOutboundBrokers:  opts.ExtOutboundBrokers,

		migrationType: opts.MigrationType,
	}
}

func (mi *MigrationInfraAssetGenerator) Run() error {
	slog.Info("🏁 generating migration infrastructure", "targetType", mi.migrationType)

	outputDir := "migration-infra"
	slog.Info("📁 creating migration-infra directory", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create migration-infra directory: %w", err)
	}

	var request types.MigrationWizardRequest

	switch mi.migrationType {
	case 1: // PublicMskEndpoints
		request = types.MigrationWizardRequest{
			HasPublicMskEndpoints:        true,
			VpcId:                        mi.vpcId,
			MskRegion:                    mi.region,
			MskClusterId:                 mi.mskClusterId,
			MskSaslScramBootstrapServers: mi.bootstrapBrokers,
			TargetClusterId:              mi.targetClusterId,
			TargetRestEndpoint:           mi.targetRestEndpoint,
			ClusterLinkName:              mi.clusterLinkName,
		}
	case 2: // ExternalOutboundClusterLink
		request = types.MigrationWizardRequest{
			HasPublicMskEndpoints:        false,
			UseJumpClusters:              false,
			VpcId:                        mi.vpcId,
			ExtOutboundSubnetId:          mi.subnetId,
			ExtOutboundSecurityGroupId:   mi.securityGroupId,
			ExtOutboundBrokers:           mi.extOutboundBrokers,
			MskRegion:                    mi.region,
			MskClusterId:                 mi.mskClusterId,
			MskSaslScramBootstrapServers: mi.bootstrapBrokers,
			TargetEnvironmentId:          mi.targetEnvironmentId,
			TargetClusterId:              mi.targetClusterId,
			TargetRestEndpoint:           mi.targetRestEndpoint,
			ClusterLinkName:              mi.clusterLinkName,
		}
	default:
		return fmt.Errorf("unsupported migration type: %d", mi.migrationType)
	}

	slog.Info("📋 generating Terraform configuration")
	hclService := hcl.NewMigrationInfraHCLService()
	project := hclService.GenerateTerraformModules(request)

	if err := mi.buildTerraformProject(outputDir, project); err != nil {
		return fmt.Errorf("failed to write Terraform project: %w", err)
	}

	slog.Info("✅ migration infrastructure generated", "directory", outputDir)
	return nil
}

func (mi *MigrationInfraAssetGenerator) buildTerraformProject(outputDir string, project types.MigrationInfraTerraformProject) error {
	if project.MainTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "main.tf"), []byte(project.MainTf), 0644); err != nil {
			return fmt.Errorf("failed to write main.tf: %w", err)
		}
		slog.Info("✅ wrote root main.tf")
	}

	if project.ProvidersTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "providers.tf"), []byte(project.ProvidersTf), 0644); err != nil {
			return fmt.Errorf("failed to write providers.tf: %w", err)
		}
		slog.Info("✅ wrote root providers.tf")
	}

	if project.VariablesTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "variables.tf"), []byte(project.VariablesTf), 0644); err != nil {
			return fmt.Errorf("failed to write variables.tf: %w", err)
		}
		slog.Info("✅ wrote root variables.tf")
	}

	if project.InputsAutoTfvars != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "inputs.auto.tfvars"), []byte(project.InputsAutoTfvars), 0644); err != nil {
			return fmt.Errorf("failed to write inputs.auto.tfvars: %w", err)
		}
		slog.Info("✅ wrote root inputs.auto.tfvars")
	}

	for _, module := range project.Modules {
		moduleDir := filepath.Join(outputDir, module.Name)
		if err := os.MkdirAll(moduleDir, 0755); err != nil {
			return fmt.Errorf("failed to create module directory %s: %w", module.Name, err)
		}

		if module.MainTf != "" {
			if err := os.WriteFile(filepath.Join(moduleDir, "main.tf"), []byte(module.MainTf), 0644); err != nil {
				return fmt.Errorf("failed to write module %s main.tf: %w", module.Name, err)
			}
		}

		if module.VariablesTf != "" {
			if err := os.WriteFile(filepath.Join(moduleDir, "variables.tf"), []byte(module.VariablesTf), 0644); err != nil {
				return fmt.Errorf("failed to write module %s variables.tf: %w", module.Name, err)
			}
		}

		if module.OutputsTf != "" {
			if err := os.WriteFile(filepath.Join(moduleDir, "outputs.tf"), []byte(module.OutputsTf), 0644); err != nil {
				return fmt.Errorf("failed to write module %s outputs.tf: %w", module.Name, err)
			}
		}

		if module.VersionsTf != "" {
			if err := os.WriteFile(filepath.Join(moduleDir, "versions.tf"), []byte(module.VersionsTf), 0644); err != nil {
				return fmt.Errorf("failed to write module %s versions.tf: %w", module.Name, err)
			}
		}

		for filename, content := range module.AdditionalFiles {
			if err := os.WriteFile(filepath.Join(moduleDir, filename), []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to write module %s additional file %s: %w", module.Name, filename, err)
			}
		}
	}

	return nil
}
