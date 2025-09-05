package reverse_proxy

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/confluentinc/kcp/internal/types"
)

//go:embed assets
var assetsFS embed.FS

type ReverseProxyOpts struct {
	Region           string
	PublicSubnetCidr string
	VPCId            string
	TerraformOutput  types.TerraformOutput
	SecurityGroupIds []string
}

type ReverseProxyAssetGenerator struct {
	region           string
	publicSubnetCidr string
	vpcId            string
	terraformOutput  types.TerraformOutput
	securityGroupIds []string
}

func NewReverseProxyAssetGenerator(opts ReverseProxyOpts) *ReverseProxyAssetGenerator {
	return &ReverseProxyAssetGenerator{
		region:           opts.Region,
		publicSubnetCidr: opts.PublicSubnetCidr,
		vpcId:            opts.VPCId,
		terraformOutput:  opts.TerraformOutput,
		securityGroupIds: opts.SecurityGroupIds,
	}
}

func (rp *ReverseProxyAssetGenerator) Run() error {
	slog.Info("🏁 generating reverse proxy assets")

	targetDir := "reverse_proxy"
	slog.Info("📁 creating reverse proxy directory", "directory", targetDir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create reverse proxy directory: %w", err)
	}

	assetsDir := "assets"
	slog.Info("📋 copying assets to target directory", "from", assetsDir, "to", targetDir)
	if err := rp.copyFiles(assetsDir, targetDir); err != nil {
		return fmt.Errorf("failed to copy reverse proxy files: %w", err)
	}

	if err := rp.generateTfvarsFiles(targetDir); err != nil {
		return fmt.Errorf("failed to generate tfvars files: %w", err)
	}

	slog.Info("✅ reverse proxy assets generated", "directory", targetDir)

	return nil
}

func (rp *ReverseProxyAssetGenerator) copyFiles(sourceDir, destDir string) error {
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

func (rp *ReverseProxyAssetGenerator) generateTfvarsFiles(terraformDir string) error {
	if err := rp.generateInputsTfvars(terraformDir); err != nil {
		return fmt.Errorf("failed to generate inputs tfvars file: %w", err)
	}

	return nil
}

func (rp *ReverseProxyAssetGenerator) generateInputsTfvars(terraformDir string) error {
	templatePath := "assets/inputs.auto.tfvars.go.tmpl"
	templateContent, err := assetsFS.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("tfvars").Parse(string(templateContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	templateData := struct {
		AWSRegion                              string
		PublicSubnetCIDR                       string
		VPCID                                  string
		ConfluentCloudClusterBootstrapEndpoint string
		SecurityGroupIds                       []string
	}{
		AWSRegion:                              rp.region,
		PublicSubnetCIDR:                       rp.publicSubnetCidr,
		VPCID:                                  rp.vpcId,
		ConfluentCloudClusterBootstrapEndpoint: rp.terraformOutput.ConfluentCloudClusterBootstrapEndpoint.Value.(string),
		SecurityGroupIds:                       rp.securityGroupIds,
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, templateData); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	tfvarsPath := filepath.Join(terraformDir, "inputs.auto.tfvars")
	if err := os.WriteFile(tfvarsPath, []byte(buf.String()), 0644); err != nil {
		return fmt.Errorf("failed to write tfvars file: %w", err)
	}

	slog.Info("✅ generated inputs tfvars file from template", "file", tfvarsPath)
	return nil
}
