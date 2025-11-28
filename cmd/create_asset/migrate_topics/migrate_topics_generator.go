package migrate_topics

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrateTopicsOpts struct {
	MirrorTopics              []string
	TargetClusterId           string
	TargetClusterRestEndpoint string
	ClusterLinkName           string
	OutputDir                 string
}

type MigrateTopicsAssetGenerator struct {
	opts MigrateTopicsOpts
}

func NewMigrateTopicsAssetGenerator(opts MigrateTopicsOpts) *MigrateTopicsAssetGenerator {
	return &MigrateTopicsAssetGenerator{
		opts: opts,
	}
}

func (mt *MigrateTopicsAssetGenerator) Run() error {
	slog.Info("🏁 generating Terraform files for mirror topics!")

	outputDir := mt.opts.OutputDir
	if outputDir == "" {
		outputDir = "migrate_topics"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create MirrorTopicsRequest from opts
	request := types.MirrorTopicsRequest{
		SelectedTopics:            mt.opts.MirrorTopics,
		ClusterLinkName:           mt.opts.ClusterLinkName,
		TargetClusterId:           mt.opts.TargetClusterId,
		TargetClusterRestEndpoint: mt.opts.TargetClusterRestEndpoint,
	}

	// Generate Terraform files using HCL service
	hclService := hcl.NewMigrationScriptsHCLService()
	terraformFiles, err := hclService.GenerateMirrorTopicsFiles(request)
	if err != nil {
		return fmt.Errorf("failed to generate Terraform files: %w", err)
	}

	// Write Terraform files to disk
	if err := mt.writeTerraformFiles(outputDir, terraformFiles); err != nil {
		return fmt.Errorf("failed to write Terraform files: %w", err)
	}

	slog.Info("✅ migrate topics Terraform files generated", "directory", outputDir, "topics", len(mt.opts.MirrorTopics))

	return nil
}

func (mt *MigrateTopicsAssetGenerator) writeTerraformFiles(outputDir string, files types.TerraformFiles) error {
	if files.MainTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "main.tf"), []byte(files.MainTf), 0644); err != nil {
			return fmt.Errorf("failed to write main.tf: %w", err)
		}
		slog.Info("✅ wrote main.tf")
	}

	if files.ProvidersTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "providers.tf"), []byte(files.ProvidersTf), 0644); err != nil {
			return fmt.Errorf("failed to write providers.tf: %w", err)
		}
		slog.Info("✅ wrote providers.tf")
	}

	if files.VariablesTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "variables.tf"), []byte(files.VariablesTf), 0644); err != nil {
			return fmt.Errorf("failed to write variables.tf: %w", err)
		}
		slog.Info("✅ wrote variables.tf")
	}

	return nil
}
