package migration_scripts

import (
	"embed"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"text/template"

	"github.com/confluentinc/kcp/internal/types"
)

//go:embed assets
var assetsFS embed.FS

type MigrationScriptsOpts struct {
	MirrorTopics    []string
	TerraformOutput types.TerraformOutput
	Manifest        types.Manifest
}

type MigrationScriptsAssetGenerator struct {
	mirrorTopics    []string
	terraformOutput types.TerraformOutput
	manifest        types.Manifest
}

func NewMigrationAssetGenerator(opts MigrationScriptsOpts) *MigrationScriptsAssetGenerator {
	return &MigrationScriptsAssetGenerator{
		mirrorTopics:    opts.MirrorTopics,
		terraformOutput: opts.TerraformOutput,
		manifest:        opts.Manifest,
	}
}

func (ms *MigrationScriptsAssetGenerator) Run() error {
	slog.Info("🏁 generating migration scripts assets!")

	outputDir := filepath.Join("migration_scripts")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create migration-plan directory: %w", err)
	}

	switch ms.manifest.MigrationInfraType {
	case types.MskCpCcPrivateSaslIam, types.MskCpCcPrivateSaslScram:
		if err := ms.generateJumpClusterMigrationScripts(outputDir, ms.mirrorTopics); err != nil {
			return fmt.Errorf("failed to generate jump cluster migration scripts: %w", err)
		}
	case types.MskCcPublic:
		if err := ms.generateMskToCCMigrationScripts(outputDir, ms.mirrorTopics); err != nil {
			return fmt.Errorf("failed to generate msk to cc migration scripts: %w", err)
		}
	default:
		return fmt.Errorf("invalid migration infra type: %d", ms.manifest.MigrationInfraType)
	}

	slog.Info("✅ migration assets generated", "directory", outputDir)

	return nil
}

func (ms *MigrationScriptsAssetGenerator) copyREADMEfile(outputDir, assetsDir string) error {
	readmeContent, err := assetsFS.ReadFile(filepath.Join(assetsDir, "README.md"))
	if err != nil {
		return fmt.Errorf("failed to read README file: %w", err)
	}

	readmePath := filepath.Join(outputDir, "README.md")
	if err := os.WriteFile(readmePath, readmeContent, 0644); err != nil {
		return fmt.Errorf("failed to write README file: %w", err)
	}
	return nil
}

func (ms *MigrationScriptsAssetGenerator) generateMskToCCMigrationScripts(outputDir string, mirrorTopics []string) error {
	assetsDir := "assets/msk-to-cc-migration"

	if err := ms.copyREADMEfile(outputDir, assetsDir); err != nil {
		return fmt.Errorf("failed to copy README file: %w", err)
	}

	if err := ms.generateMSKToCCMirrorTopics(outputDir, mirrorTopics, assetsDir); err != nil {
		return fmt.Errorf("failed to generate msk-to-cc-mirror-topics.sh: %w", err)
	}

	return nil
}

func (ms *MigrationScriptsAssetGenerator) generateMSKToCCMirrorTopics(outputDir string, mirrorTopics []string, assetsDir string) error {
	mskToCCMirrorTopicsPath := filepath.Join(outputDir, "msk-to-cc-mirror-topics.sh")

	file, err := os.Create(mskToCCMirrorTopicsPath)
	if err != nil {
		return fmt.Errorf("failed to create msk-to-cc-mirror-topics.sh file: %w", err)
	}

	defer file.Close()

	if err := ms.generateMSKToCCMirrorTopicsContent(file, ms.terraformOutput, mirrorTopics, assetsDir); err != nil {
		return err
	}

	// Make the file executable
	if err := os.Chmod(mskToCCMirrorTopicsPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions on msk-to-cc-mirror-topics.sh: %w", err)
	}

	return nil
}

func (ms *MigrationScriptsAssetGenerator) generateMSKToCCMirrorTopicsContent(w io.Writer, terraformOutput types.TerraformOutput, mirrorTopics []string, assetsDir string) error {
	templatePath := filepath.Join(assetsDir, "msk-to-cc-mirror-topics.sh.go.tmpl")
	templateContent, err := assetsFS.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("msk-to-cc-mirror-topics").Parse(string(templateContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	apiKey := terraformOutput.ConfluentCloudClusterApiKey.Value.(string)
	apiKeySecret := terraformOutput.ConfluentCloudClusterApiKeySecret.Value.(string)
	authToken := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", apiKey, apiKeySecret))

	templateData := struct {
		MirrorTopics           []string
		ConfluentCloudEndpoint string
		ClusterId              string
		AuthToken              string
	}{
		MirrorTopics:           mirrorTopics,
		ConfluentCloudEndpoint: terraformOutput.ConfluentCloudClusterRestEndpoint.Value.(string),
		ClusterId:              terraformOutput.ConfluentCloudClusterId.Value.(string),
		AuthToken:              authToken,
	}

	if err := tmpl.Execute(w, templateData); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

func (ms *MigrationScriptsAssetGenerator) generateJumpClusterMigrationScripts(outputDir string, mirrorTopics []string) error {
	assetsDir := "assets/msk-to-cp_cp-to-cc-migration"

	if err := ms.copyREADMEfile(outputDir, assetsDir); err != nil {
		return fmt.Errorf("failed to copy README file: %w", err)
	}

	if err := ms.generateMskToCpMirrorTopics(outputDir, mirrorTopics, assetsDir); err != nil {
		return fmt.Errorf("failed to generate msk-to-cp-mirror-topics.sh: %w", err)
	}

	if err := ms.generateCpToCCMirrorTopics(outputDir, mirrorTopics, assetsDir); err != nil {
		return fmt.Errorf("failed to generate cp-to-cc-mirror-topics.sh: %w", err)
	}

	if err := ms.generateDestinationClusterProperties(outputDir, assetsDir); err != nil {
		return fmt.Errorf("failed to generate destination cluster properties: %w", err)
	}

	return nil
}

func (ms *MigrationScriptsAssetGenerator) generateMskToCpMirrorTopics(outputDir string, mirrorTopics []string, assetsDir string) error {
	mskToCpMirrorTopicsPath := filepath.Join(outputDir, "msk-to-cp-mirror-topics.sh")

	file, err := os.Create(mskToCpMirrorTopicsPath)
	if err != nil {
		return fmt.Errorf("failed to create msk-to-cp-mirror-topics.sh file: %w", err)
	}
	defer file.Close()

	if err := ms.generateMskToCpMirrorTopicsContent(file, ms.terraformOutput, mirrorTopics, assetsDir); err != nil {
		return err
	}

	// Make the file executable
	if err := os.Chmod(mskToCpMirrorTopicsPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions on msk-to-cp-mirror-topics.sh: %w", err)
	}

	return nil
}

func (ms *MigrationScriptsAssetGenerator) generateMskToCpMirrorTopicsContent(w io.Writer, terraformOutput types.TerraformOutput, mirrorTopics []string, assetsDir string) error {
	templatePath := filepath.Join(assetsDir, "msk-to-cp-mirror-topics.sh.go.tmpl")
	templateContent, err := assetsFS.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("msk-to-cp-mirror-topics").Parse(string(templateContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	templateData := struct {
		MirrorTopics     []string
		BootstrapServers string
	}{
		MirrorTopics:     mirrorTopics,
		BootstrapServers: terraformOutput.ConfluentPlatformControllerBootstrapServer.Value.(string),
	}

	if err := tmpl.Execute(w, templateData); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

func (ms *MigrationScriptsAssetGenerator) generateCpToCCMirrorTopics(outputDir string, mirrorTopics []string, assetsDir string) error {
	cpToCCMirrorTopicsPath := filepath.Join(outputDir, "cp-to-cc-mirror-topics.sh")

	file, err := os.Create(cpToCCMirrorTopicsPath)
	if err != nil {
		return fmt.Errorf("failed to create cp-to-cc-mirror-topics.sh file: %w", err)
	}

	defer file.Close()

	if err := ms.generateCpToCCMirrorTopicsContent(file, ms.terraformOutput, mirrorTopics, assetsDir); err != nil {
		return err
	}

	// Make the file executable
	if err := os.Chmod(cpToCCMirrorTopicsPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions on cp-to-cc-mirror-topics.sh: %w", err)
	}

	return nil
}

func (ms *MigrationScriptsAssetGenerator) generateCpToCCMirrorTopicsContent(w io.Writer, terraformOutput types.TerraformOutput, mirrorTopics []string, assetsDir string) error {
	templatePath := filepath.Join(assetsDir, "cp-to-cc-mirror-topics.sh.go.tmpl")
	templateContent, err := assetsFS.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("cp-to-cc-mirror-topics").Parse(string(templateContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	apiKey := terraformOutput.ConfluentCloudClusterApiKey.Value.(string)
	apiKeySecret := terraformOutput.ConfluentCloudClusterApiKeySecret.Value.(string)
	authToken := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", apiKey, apiKeySecret))

	templateData := struct {
		MirrorTopics           []string
		ConfluentCloudEndpoint string
		ClusterId              string
		AuthToken              string
	}{
		MirrorTopics:           mirrorTopics,
		ConfluentCloudEndpoint: terraformOutput.ConfluentCloudClusterRestEndpoint.Value.(string),
		ClusterId:              terraformOutput.ConfluentCloudClusterId.Value.(string),
		AuthToken:              authToken,
	}

	if err := tmpl.Execute(w, templateData); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

func (ms *MigrationScriptsAssetGenerator) generateDestinationClusterProperties(outputDir string, assetsDir string) error {
	destinationClusterPropertiesPath := filepath.Join(outputDir, "destination-cluster.properties")

	file, err := os.Create(destinationClusterPropertiesPath)
	if err != nil {
		return fmt.Errorf("failed to create destination-cluster.properties file: %w", err)
	}
	defer file.Close()

	if err := ms.generateDestinationClusterPropertiesContent(file, ms.terraformOutput, assetsDir); err != nil {
		return err
	}

	return nil
}

func (ms *MigrationScriptsAssetGenerator) generateDestinationClusterPropertiesContent(w io.Writer, terraformOutput types.TerraformOutput, assetsDir string) error {
	templatePath := filepath.Join(assetsDir, "destination-cluster-properties.go.tmpl")

	templateContent, err := assetsFS.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("destination-cluster-properties").Parse(string(templateContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	templateData := struct {
		BootstrapServers string
	}{
		BootstrapServers: terraformOutput.ConfluentPlatformControllerBootstrapServer.Value.(string),
	}

	if err := tmpl.Execute(w, templateData); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}
