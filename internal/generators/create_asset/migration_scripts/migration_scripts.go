package migration_scripts

import (
	"embed"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/confluentinc/kcp-internal/internal/types"
)

//go:embed assets
var assetsFS embed.FS

type MigrationScriptsOpts struct {
	ClusterInformation types.ClusterInformation
	TerraformOutput    types.TerraformOutput
}

type MigrationAssetGenerator struct {
	clusterInfo     types.ClusterInformation
	terraformOutput types.TerraformOutput
}

func NewMigrationAssetGenerator(opts MigrationScriptsOpts) *MigrationAssetGenerator {
	return &MigrationAssetGenerator{
		clusterInfo:     opts.ClusterInformation,
		terraformOutput: opts.TerraformOutput,
	}
}

func (ms *MigrationAssetGenerator) Run() error {
	slog.Info("🏁 generating migration assets")

	targetDir := filepath.Join("migration-scripts")
	slog.Info("📁 creating migration directory", "directory", targetDir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create migration-plan directory: %w", err)
	}

	mirrorTopics := []string{}
	for _, topic := range ms.clusterInfo.Topics {
		if !strings.HasPrefix(topic, "__") {
			mirrorTopics = append(mirrorTopics, topic)
		}
	}

	assetsDir := "assets"
	slog.Info("📋 copying assets to target directory", "from", assetsDir, "to", targetDir)
	if err := ms.copyFiles(assetsDir, targetDir); err != nil {
		return fmt.Errorf("failed to copy migration scripts files: %w", err)
	}

	if err := ms.generateFiles(targetDir, mirrorTopics); err != nil {
		return fmt.Errorf("failed to generate tfvars files: %w", err)
	}

	slog.Info("✅ migration assets generated", "directory", targetDir)

	return nil
}

func (ms *MigrationAssetGenerator) copyFiles(sourceDir, destDir string) error {
	return fs.WalkDir(assetsFS, sourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip the source directory itself
		if path == sourceDir {
			return nil
		}

		// exclude template files
		if strings.HasSuffix(path, ".tmpl") {
			return nil
		}

		// Calculate relative path from source directory
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		// Read file content from embedded filesystem
		content, err := assetsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		if err := os.WriteFile(destPath, content, 0644); err != nil {
			return fmt.Errorf("failed to write file %s: %w", destPath, err)
		}

		return nil
	})
}

func (ms *MigrationAssetGenerator) generateFiles(targetDir string, mirrorTopics []string) error {
	if err := ms.generateMskToCpMirrorTopics(targetDir, mirrorTopics); err != nil {
		return fmt.Errorf("failed to generate msk-to-cp-mirror-topics.sh: %w", err)
	}

	if err := ms.generateCpToCCMirrorTopics(targetDir, mirrorTopics); err != nil {
		return fmt.Errorf("failed to generate cp-to-cc-mirror-topics.sh: %w", err)
	}

	if err := ms.generateDestinationClusterProperties(targetDir); err != nil {
		return fmt.Errorf("failed to generate destination cluster properties: %w", err)
	}

	return nil
}

func (ms *MigrationAssetGenerator) generateMskToCpMirrorTopics(targetDir string, mirrorTopics []string) error {
	mskToCpMirrorTopicsPath := filepath.Join(targetDir, "msk-to-cp-mirror-topics.sh")

	file, err := os.Create(mskToCpMirrorTopicsPath)
	if err != nil {
		return fmt.Errorf("failed to create msk-to-cp-mirror-topics.sh file: %w", err)
	}
	defer file.Close()

	if err := ms.generateMskToCpMirrorTopicsContent(file, ms.terraformOutput, mirrorTopics); err != nil {
		return err
	}

	// Make the file executable
	if err := os.Chmod(mskToCpMirrorTopicsPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions on msk-to-cp-mirror-topics.sh: %w", err)
	}

	return nil
}

func (ms *MigrationAssetGenerator) generateMskToCpMirrorTopicsContent(w io.Writer, terraformOutput types.TerraformOutput, mirrorTopics []string) error {
	templatePath := "assets/msk-to-cp-mirror-topics.sh.go.tmpl"

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

func (ms *MigrationAssetGenerator) generateCpToCCMirrorTopics(targetDir string, mirrorTopics []string) error {
	cpToCCMirrorTopicsPath := filepath.Join(targetDir, "cp-to-cc-mirror-topics.sh")

	file, err := os.Create(cpToCCMirrorTopicsPath)
	if err != nil {
		return fmt.Errorf("failed to create cp-to-cc-mirror-topics.sh file: %w", err)
	}

	defer file.Close()

	if err := ms.generateCpToCCMirrorTopicsContent(file, ms.terraformOutput, mirrorTopics); err != nil {
		return err
	}

	// Make the file executable
	if err := os.Chmod(cpToCCMirrorTopicsPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions on cp-to-cc-mirror-topics.sh: %w", err)
	}

	return nil
}

func (ms *MigrationAssetGenerator) generateCpToCCMirrorTopicsContent(w io.Writer, terraformOutput types.TerraformOutput, mirrorTopics []string) error {
	templatePath := "assets/cp-to-cc-mirror-topics.sh.go.tmpl"
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

func (ms *MigrationAssetGenerator) generateDestinationClusterProperties(targetDir string) error {
	destinationClusterPropertiesPath := filepath.Join(targetDir, "destination-cluster.properties")

	file, err := os.Create(destinationClusterPropertiesPath)
	if err != nil {
		return fmt.Errorf("failed to create destination-cluster.properties file: %w", err)
	}
	defer file.Close()

	if err := ms.generateDestinationClusterPropertiesContent(file, ms.terraformOutput); err != nil {
		return err
	}

	slog.Info("✅ generated destination-cluster.properties file from template", "file", destinationClusterPropertiesPath)
	return nil
}

func (ms *MigrationAssetGenerator) generateDestinationClusterPropertiesContent(w io.Writer, terraformOutput types.TerraformOutput) error {
	templatePath := "assets/destination-cluster-properties.go.tmpl"

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
