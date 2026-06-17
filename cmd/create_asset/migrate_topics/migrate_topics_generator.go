package migrate_topics

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/services/hcl/hcltypes"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

const newModeCLINote = "Note: Some source topic configs are not configurable on Confluent Cloud and were dropped. See " + hcl.CCSupportedTopicConfigsDocsURL

type MigrateTopicsOpts struct {
	Topics                    []types.TopicDetails
	TargetClusterId           string
	TargetClusterRestEndpoint string
	ClusterLinkName           string
	OutputDir                 string
	Mode                      string
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
	fmt.Printf("🚀 Generating Terraform files for migrate-topics (mode=%s)\n", mt.opts.Mode)

	outputDir := mt.opts.OutputDir
	if outputDir == "" {
		outputDir = "migrate_topics"
	}
	if err := utils.ValidateOutputDir(outputDir); err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	selectedNames := make([]string, len(mt.opts.Topics))
	for i, t := range mt.opts.Topics {
		selectedNames[i] = t.Name
	}

	request := types.MirrorTopicsRequest{
		SelectedTopics:            selectedNames,
		Topics:                    mt.opts.Topics,
		ClusterLinkName:           mt.opts.ClusterLinkName,
		TargetClusterId:           mt.opts.TargetClusterId,
		TargetClusterRestEndpoint: mt.opts.TargetClusterRestEndpoint,
		Mode:                      mt.opts.Mode,
	}

	hclService := hcl.NewMigrationScriptsHCLService()
	project, err := hclService.GenerateMirrorTopicsFiles(request)
	if err != nil {
		return fmt.Errorf("failed to generate Terraform files: %w", err)
	}

	if err := mt.writeProject(outputDir, project); err != nil {
		return fmt.Errorf("failed to write Terraform files: %w", err)
	}

	fmt.Printf("✅ migrate-topics Terraform files generated: %s (%d topics, mode=%s)\n", outputDir, len(mt.opts.Topics), mt.opts.Mode)
	if mt.opts.Mode == types.MigrateTopicsModeNew {
		fmt.Println(newModeCLINote)
	}

	return nil
}

// writeProject writes the single-folder migrate-topics project flat into
// outputDir: providers.tf, variables.tf, plus one .tf per topic from
// AdditionalFiles.
func (mt *MigrateTopicsAssetGenerator) writeProject(outputDir string, project hcltypes.MigrationScriptsTerraformProject) error {
	if len(project.Folders) == 0 {
		return nil
	}
	folder := project.Folders[0]

	if folder.ProvidersTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "providers.tf"), []byte(folder.ProvidersTf), 0644); err != nil {
			return fmt.Errorf("failed to write providers.tf: %w", err)
		}
		slog.Debug("wrote providers.tf")
	}

	if folder.VariablesTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "variables.tf"), []byte(folder.VariablesTf), 0644); err != nil {
			return fmt.Errorf("failed to write variables.tf: %w", err)
		}
		slog.Debug("wrote variables.tf")
	}

	for name, content := range folder.AdditionalFiles {
		if err := os.WriteFile(filepath.Join(outputDir, name), []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
		slog.Debug("wrote per-topic file", "file", name)
	}

	return nil
}
