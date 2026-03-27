package migrate_schemas

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"

	hclservice "github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrateGlueSchemasOpts struct {
	GlueRegistry     types.GlueSchemaRegistryInformation
	CCSRRestEndpoint string
	OutputDir        string
}

type MigrateGlueSchemasAssetGenerator struct {
	glueRegistry     types.GlueSchemaRegistryInformation
	ccSRRestEndpoint string
	outputDir        string
}

func NewMigrateGlueSchemasAssetGenerator(opts MigrateGlueSchemasOpts) *MigrateGlueSchemasAssetGenerator {
	return &MigrateGlueSchemasAssetGenerator{
		glueRegistry:     opts.GlueRegistry,
		ccSRRestEndpoint: opts.CCSRRestEndpoint,
		outputDir:        opts.OutputDir,
	}
}

func (g *MigrateGlueSchemasAssetGenerator) Run() error {
	slog.Info("generating glue schema migration assets", "registry", g.glueRegistry.RegistryName)

	request := types.MigrateGlueSchemasRequest{
		ConfluentCloudSchemaRegistryURL: g.ccSRRestEndpoint,
		GlueRegistries: []types.GlueSchemaRegistryMigrationConfig{
			{
				Migrate:      true,
				RegistryName: g.glueRegistry.RegistryName,
				Region:       g.glueRegistry.Region,
				Schemas:      g.glueRegistry.Schemas,
			},
		},
	}

	service := hclservice.NewMigrationScriptsHCLService()
	project, err := service.GenerateMigrateGlueSchemasFiles(request)
	if err != nil {
		return fmt.Errorf("failed to generate terraform files: %w", err)
	}

	if err := os.MkdirAll(g.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", g.outputDir, err)
	}

	for _, folder := range project.Folders {
		// Write standard TF files (skip empty ones)
		files := map[string]string{
			"providers.tf":       folder.ProvidersTf,
			"variables.tf":       folder.VariablesTf,
			"inputs.auto.tfvars": folder.InputsAutoTfvars,
		}

		for name, content := range files {
			if content == "" {
				continue
			}
			path := filepath.Join(g.outputDir, name)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to write %s: %w", path, err)
			}
		}

		// Write per-schema .tf files and schema definition files
		absOutput, err := filepath.Abs(g.outputDir)
		if err != nil {
			return fmt.Errorf("failed to resolve output directory: %w", err)
		}
		for filePath, content := range folder.AdditionalFiles {
			fullPath := filepath.Join(g.outputDir, filePath)
			absTarget, err := filepath.Abs(fullPath)
			if err != nil {
				return fmt.Errorf("failed to resolve path %s: %w", fullPath, err)
			}
			if !strings.HasPrefix(absTarget, absOutput+string(filepath.Separator)) && absTarget != absOutput {
				return fmt.Errorf("path traversal detected: %s escapes output directory", filePath)
			}
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				return fmt.Errorf("failed to create directory for %s: %w", fullPath, err)
			}
			if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to write %s: %w", fullPath, err)
			}
		}
	}

	slog.Info("glue schema migration assets generated", "directory", g.outputDir)
	fmt.Printf("%s Glue schema migration assets generated: %s\n", color.GreenString("✅"), g.outputDir)
	return nil
}
