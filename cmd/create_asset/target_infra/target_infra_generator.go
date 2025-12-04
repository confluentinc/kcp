package targetinfra

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/types"
)

type TargetInfraGenerator struct {
	OutputDir string
}

func NewTargetInfraGenerator(outputDir string) *TargetInfraGenerator {
	return &TargetInfraGenerator{
		OutputDir: outputDir,
	}
}

func (g *TargetInfraGenerator) BuildTerraformProject(project types.MigrationInfraTerraformProject) error {
	if project.MainTf != "" {
		if err := os.WriteFile(filepath.Join(g.OutputDir, "main.tf"), []byte(project.MainTf), 0644); err != nil {
			return fmt.Errorf("failed to write main.tf: %w", err)
		}
		slog.Info("✅ wrote root main.tf")
	}

	if project.ProvidersTf != "" {
		if err := os.WriteFile(filepath.Join(g.OutputDir, "providers.tf"), []byte(project.ProvidersTf), 0644); err != nil {
			return fmt.Errorf("failed to write providers.tf: %w", err)
		}
		slog.Info("✅ wrote root providers.tf")
	}

	if project.VariablesTf != "" {
		if err := os.WriteFile(filepath.Join(g.OutputDir, "variables.tf"), []byte(project.VariablesTf), 0644); err != nil {
			return fmt.Errorf("failed to write variables.tf: %w", err)
		}
		slog.Info("✅ wrote root variables.tf")
	}

	if project.InputsAutoTfvars != "" {
		if err := os.WriteFile(filepath.Join(g.OutputDir, "inputs.auto.tfvars"), []byte(project.InputsAutoTfvars), 0644); err != nil {
			return fmt.Errorf("failed to write inputs.auto.tfvars: %w", err)
		}
		slog.Info("✅ wrote root inputs.auto.tfvars")
	}

	for _, module := range project.Modules {
		moduleDir := filepath.Join(g.OutputDir, module.Name)
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
				return fmt.Errorf("failed to write module %s file %s: %w", module.Name, filename, err)
			}
		}

		slog.Info("✅ wrote module", "module", module.Name)
	}

	return nil
}

