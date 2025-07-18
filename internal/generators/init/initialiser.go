package init

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

//go:embed assets
var assetsFS embed.FS

type Initializer struct {
}

func NewInitializer() *Initializer {
	return &Initializer{}
}

func (i *Initializer) Run() error {

	err := i.copyREADMEfile(".")
	if err != nil {
		return fmt.Errorf("failed to copy README file: %w", err)
	}

	err = i.generateEnvVarsScript(".")
	if err != nil {
		return fmt.Errorf("failed to generate env vars script: %w", err)
	}

	slog.Info("📝 Please use the readme to help you fill in your specific configuration values")
	slog.Info("🔧 Generated set_migration_env_vars.sh script with environment variable templates")

	return nil
}

func (i *Initializer) copyREADMEfile(targetDir string) error {
	readmeContent, err := assetsFS.ReadFile("assets/README.md")
	if err != nil {
		return fmt.Errorf("failed to read README file: %w", err)
	}

	readmePath := filepath.Join(targetDir, "README.md")
	if err := os.WriteFile(readmePath, readmeContent, 0644); err != nil {
		return fmt.Errorf("failed to write README file: %w", err)
	}
	return nil
}

func (i *Initializer) generateEnvVarsScript(targetDir string) error {
	templateContent, err := assetsFS.ReadFile("assets/set_migration_env_vars.sh.go.tmpl")
	if err != nil {
		return fmt.Errorf("failed to read env vars template: %w", err)
	}

	scriptPath := filepath.Join(targetDir, "set_migration_env_vars.sh")
	if err := os.WriteFile(scriptPath, templateContent, 0755); err != nil {
		return fmt.Errorf("failed to write env vars script: %w", err)
	}

	slog.Info("Generated set_migration_env_vars.sh with executable permissions")
	return nil
}
