package migrate_schemas

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

//go:embed assets
var assetsFS embed.FS

type MigrateSchemasOpts struct {
}

type MigrateSchemasAssetGenerator struct {
}

func NewMigrateSchemasAssetGenerator(opts MigrateSchemasOpts) *MigrateSchemasAssetGenerator {
	return &MigrateSchemasAssetGenerator{}
}

func (ms *MigrateSchemasAssetGenerator) Run() error {
	slog.Info("🏁 generating migrate schemas assets!")

	outputDir := filepath.Join("migrate_schemas")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create migrate-topics directory: %w", err)
	}

	slog.Info("✅ migrate schemas assets generated", "directory", outputDir)

	return nil
}
