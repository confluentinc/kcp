package migrate_schemas

import (
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/confluentinc/kcp/internal/types"
)

//go:embed assets
var assetsFS embed.FS

type SchemaExporter struct {
	Name        string
	ContextType string
	ContextName string
	Subjects    []string
}

type MigrateSchemasOpts struct {
	SchemaRegistry types.SchemaRegistryInformation
	Exporters      []SchemaExporter
}

type MigrateSchemasAssetGenerator struct {
	schemaRegistry types.SchemaRegistryInformation
	exporters      []SchemaExporter
}

func NewMigrateSchemasAssetGenerator(opts MigrateSchemasOpts) *MigrateSchemasAssetGenerator {
	return &MigrateSchemasAssetGenerator{
		schemaRegistry: opts.SchemaRegistry,
		exporters:      opts.Exporters,
	}
}

func (ms *MigrateSchemasAssetGenerator) Run() error {
	slog.Info("🏁 generating migrate schemas assets!")

	outputDir := filepath.Join("migrate_schemas")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create migrate-schemas directory: %w", err)
	}

	assetsDir := "assets"
	if err := ms.copyFiles(assetsDir, outputDir); err != nil {
		return fmt.Errorf("failed to copy migrate schemas files: %w", err)
	}

	if err := ms.generateTfvarsFiles(outputDir); err != nil {
		return fmt.Errorf("failed to generate tfvars files: %w", err)
	}

	slog.Info("✅ migrate schemas assets generated", "directory", outputDir)

	return nil
}

func (ms *MigrateSchemasAssetGenerator) copyFiles(sourceDir, destDir string) error {
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

func (ms *MigrateSchemasAssetGenerator) generateTfvarsFiles(terraformDir string) error {
	if err := ms.generateInputsTfvars(terraformDir); err != nil {
		return fmt.Errorf("failed to generate inputs tfvars file: %w", err)
	}

	return nil
}

func randomString(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)[:length]
}

func (ms *MigrateSchemasAssetGenerator) generateInputsTfvars(terraformDir string) error {
	// Read the Go template file from embedded assets
	templatePath := "assets/inputs.auto.tfvars.go.tmpl"
	templateContent, err := assetsFS.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	// Parse the template
	tmpl, err := template.New("tfvars").Parse(string(templateContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	// Prepare template data
	templateData := struct {
		Exporters               []SchemaExporter
		SourceSchemaRegistryID  string
		SourceSchemaRegistryURL string
	}{
		Exporters: ms.exporters,
		// confluent exporter expects an id for the source schema registry
		SourceSchemaRegistryID:  randomString(5),
		SourceSchemaRegistryURL: ms.schemaRegistry.URL,
	}
	// Execute template
	var buf strings.Builder
	if err := tmpl.Execute(&buf, templateData); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Write the generated content to inputs.auto.tfvars
	tfvarsPath := filepath.Join(terraformDir, "inputs.auto.tfvars")
	if err := os.WriteFile(tfvarsPath, []byte(buf.String()), 0644); err != nil {
		return fmt.Errorf("failed to write tfvars file: %w", err)
	}

	slog.Info("✅ generated inputs tfvars file from template", "file", tfvarsPath)
	return nil
}
