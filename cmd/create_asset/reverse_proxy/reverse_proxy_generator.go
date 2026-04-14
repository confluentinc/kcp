package reverse_proxy

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

type ReverseProxyOpts struct {
	Region                                 string
	PublicSubnetCidr                       string
	VPCId                                  string
	ConfluentCloudClusterBootstrapEndpoint string
}

type ReverseProxyAssetGenerator struct {
	opts ReverseProxyOpts
}

func NewReverseProxyAssetGenerator(opts ReverseProxyOpts) *ReverseProxyAssetGenerator {
	return &ReverseProxyAssetGenerator{
		opts: opts,
	}
}

func (rp *ReverseProxyAssetGenerator) Run() error {
	fmt.Printf("🚀 Generating reverse proxy assets\n")

	outputDir := "reverse_proxy"
	if err := utils.ValidateOutputDir(outputDir); err != nil {
		return err
	}
	slog.Debug("creating reverse proxy directory", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create reverse proxy directory: %w", err)
	}

	// Create request from opts
	request := types.ReverseProxyRequest{
		Region:                                 rp.opts.Region,
		VPCId:                                  rp.opts.VPCId,
		PublicSubnetCidr:                       rp.opts.PublicSubnetCidr,
		ConfluentCloudClusterBootstrapEndpoint: rp.opts.ConfluentCloudClusterBootstrapEndpoint,
	}

	// Generate Terraform files using HCL service
	hclService := hcl.NewReverseProxyHCLService()
	terraformFiles, err := hclService.GenerateReverseProxyFiles(request)
	if err != nil {
		return fmt.Errorf("failed to generate Terraform files: %w", err)
	}

	// Write Terraform files to disk
	if err := rp.writeTerraformFiles(outputDir, terraformFiles); err != nil {
		return fmt.Errorf("failed to write Terraform files: %w", err)
	}

	// Write user-data template
	userDataTemplate := hclService.GenerateReverseProxyUserDataTemplate()
	userDataPath := filepath.Join(outputDir, "reverse-proxy-user-data.tpl")
	if err := os.WriteFile(userDataPath, []byte(userDataTemplate), 0644); err != nil {
		return fmt.Errorf("failed to write user-data template: %w", err)
	}
	slog.Debug("wrote reverse-proxy-user-data.tpl")

	// Write shell script from HCL service
	scriptContent := hclService.GenerateReverseProxyShellScript()
	scriptPath := filepath.Join(outputDir, "generate_dns_entries.sh")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		return fmt.Errorf("failed to write shell script: %w", err)
	}
	slog.Debug("wrote generate_dns_entries.sh")

	fmt.Printf("✅ Reverse proxy assets generated: %s\n", outputDir)

	return nil
}

func (rp *ReverseProxyAssetGenerator) writeTerraformFiles(outputDir string, files types.TerraformFiles) error {
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

	if files.InputsAutoTfvars != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "inputs.auto.tfvars"), []byte(files.InputsAutoTfvars), 0644); err != nil {
			return fmt.Errorf("failed to write inputs.auto.tfvars: %w", err)
		}
		slog.Debug("wrote inputs.auto.tfvars")
	}

	return nil
}
