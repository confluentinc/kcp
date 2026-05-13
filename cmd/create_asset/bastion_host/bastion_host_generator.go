package bastion_host

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

type BastionHostOpts struct {
	Region                     string
	VPCId                      string
	PublicSubnetCidr           string
	HasExistingInternetGateway bool
	SecurityGroupIds           []string
	OutputDir                  string
}

type BastionHostAssetGenerator struct {
	opts BastionHostOpts
}

func NewBastionHostAssetGenerator(opts BastionHostOpts) *BastionHostAssetGenerator {
	return &BastionHostAssetGenerator{opts: opts}
}

func (bh *BastionHostAssetGenerator) Run() error {
	fmt.Printf("🚀 Generating bastion host environment assets\n")

	outputDir := bh.opts.OutputDir
	if outputDir == "" {
		outputDir = "bastion_host"
	}
	if err := utils.ValidateOutputDir(outputDir); err != nil {
		return err
	}
	slog.Debug("creating bastion host directory", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create bastion host directory: %w", err)
	}

	request := types.BastionHostRequest{
		Region:                     bh.opts.Region,
		VPCId:                      bh.opts.VPCId,
		PublicSubnetCidr:           bh.opts.PublicSubnetCidr,
		HasExistingInternetGateway: bh.opts.HasExistingInternetGateway,
		SecurityGroupIds:           bh.opts.SecurityGroupIds,
	}

	hclService := hcl.NewBastionHostHCLService()
	terraformFiles, err := hclService.GenerateBastionHostFiles(request)
	if err != nil {
		return fmt.Errorf("failed to generate Terraform files: %w", err)
	}

	if err := bh.writeTerraformFiles(outputDir, terraformFiles); err != nil {
		return fmt.Errorf("failed to write Terraform files: %w", err)
	}

	userDataPath := filepath.Join(outputDir, "bastion-host-user-data.tpl")
	if err := os.WriteFile(userDataPath, []byte(hclService.GenerateBastionHostUserDataTemplate()), 0644); err != nil {
		return fmt.Errorf("failed to write user-data template: %w", err)
	}
	slog.Debug("wrote bastion-host-user-data.tpl")

	fmt.Printf("✅ Bastion host environment assets generated successfully: %s\n", outputDir)
	return nil
}

func (bh *BastionHostAssetGenerator) writeTerraformFiles(outputDir string, files types.TerraformFiles) error {
	if files.MainTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "main.tf"), []byte(files.MainTf), 0644); err != nil {
			return fmt.Errorf("failed to write main.tf: %w", err)
		}
		slog.Debug("wrote main.tf")
	}

	if files.ProvidersTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "providers.tf"), []byte(files.ProvidersTf), 0644); err != nil {
			return fmt.Errorf("failed to write providers.tf: %w", err)
		}
		slog.Debug("wrote providers.tf")
	}

	if files.VariablesTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "variables.tf"), []byte(files.VariablesTf), 0644); err != nil {
			return fmt.Errorf("failed to write variables.tf: %w", err)
		}
		slog.Debug("wrote variables.tf")
	}

	if files.OutputsTf != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "outputs.tf"), []byte(files.OutputsTf), 0644); err != nil {
			return fmt.Errorf("failed to write outputs.tf: %w", err)
		}
		slog.Debug("wrote outputs.tf")
	}

	if files.InputsAutoTfvars != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "inputs.auto.tfvars"), []byte(files.InputsAutoTfvars), 0644); err != nil {
			return fmt.Errorf("failed to write inputs.auto.tfvars: %w", err)
		}
		slog.Debug("wrote inputs.auto.tfvars")
	}

	return nil
}
