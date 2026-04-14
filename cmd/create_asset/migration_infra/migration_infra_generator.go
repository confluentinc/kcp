package migration_infra

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
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
	fmt.Printf("🚀 Generating migration infrastructure (type: %v)\n", mi.migrationType)

	outputDir := mi.outputDir
	if outputDir == "" {
		outputDir = "migration-infra"
	}
	if err := utils.ValidateOutputDir(outputDir); err != nil {
		return err
	}
	slog.Debug("creating migration-infra directory", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create migration-infra directory: %w", err)
	}

	slog.Debug("generating Terraform configuration")
	hclService := hcl.NewMigrationInfraHCLService()
	project := hclService.GenerateTerraformModules(mi.MigrationWizardRequest)

	if err := hcl.WriteTerraformProject(outputDir, project); err != nil {
		return fmt.Errorf("failed to write Terraform project: %w", err)
	}

	fmt.Printf("✅ Migration infrastructure generated: %s\n", outputDir)
	return nil
}
