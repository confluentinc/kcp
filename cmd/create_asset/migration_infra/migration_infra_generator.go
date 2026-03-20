package migration_infra

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrationInfraOpts struct {
	MigrationWizardRequest types.MigrationWizardRequest

	OutputDir     string
	MigrationType types.MigrationType
}

type MigrationInfraAssetGenerator struct {
	MigrationWizardRequest types.MigrationWizardRequest

	outputDir     string
	migrationType types.MigrationType
}

func NewMigrationInfraAssetGenerator(opts MigrationInfraOpts) *MigrationInfraAssetGenerator {
	return &MigrationInfraAssetGenerator{
		MigrationWizardRequest: opts.MigrationWizardRequest,
		outputDir:              opts.OutputDir,
		migrationType:          opts.MigrationType,
	}
}

func (mi *MigrationInfraAssetGenerator) Run() error {
	slog.Info("🚀 generating migration infrastructure", "targetType", mi.migrationType)

	outputDir := mi.outputDir
	if outputDir == "" {
		outputDir = "migration-infra"
	}
	slog.Info("🔍 creating migration-infra directory", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create migration-infra directory: %w", err)
	}

	slog.Info("🔍 generating Terraform configuration")
	hclService := hcl.NewMigrationInfraHCLService()
	project := hclService.GenerateTerraformModules(mi.MigrationWizardRequest)

	if err := hcl.WriteTerraformProject(outputDir, project); err != nil {
		return fmt.Errorf("failed to write Terraform project: %w", err)
	}

	slog.Info("✅ migration infrastructure generated", "directory", outputDir)
	return nil
}

