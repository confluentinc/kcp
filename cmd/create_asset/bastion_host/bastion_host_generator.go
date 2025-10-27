package bastion_host

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed assets
var assetsFS embed.FS

// struct to hold the options for the bastion host asset generator
type BastionHostOpts struct {
	Region           string
	VPCId            string
	PublicSubnetCidr string
	CreateIGW        bool
	SecurityGroupIds []string
}

type BastionHostAssetGenerator struct {
	region           string
	vpcId            string
	publicSubnetCidr string
	createIGW        bool
	securityGroupIds []string
}

func NewBastionHostAssetGenerator(opts BastionHostOpts) *BastionHostAssetGenerator {
	return &BastionHostAssetGenerator{
		region:           opts.Region,
		vpcId:            opts.VPCId,
		publicSubnetCidr: opts.PublicSubnetCidr,
		createIGW:        opts.CreateIGW,
		securityGroupIds: opts.SecurityGroupIds,
	}
}

func (bh *BastionHostAssetGenerator) Run() error {
	slog.Info("🏁 generating bastion host environment assets")

	outputDir := filepath.Join("bastion_host")
	slog.Info("📁 creating bastion host directory", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create bastion host directory: %w", err)
	}

	assetsDir := "assets"
	slog.Info("📋 copying assets to target directory", "from", assetsDir, "to", outputDir)
	if err := bh.copyFiles(assetsDir, outputDir); err != nil {
		return fmt.Errorf("failed to copy bastion host files: %w", err)
	}

	if err := bh.generateTfvarsFiles(outputDir); err != nil {
		return fmt.Errorf("failed to generate tfvars files: %w", err)
	}

	slog.Info("✅ bastion host environment assets generated successfully", "directory", outputDir)

	return nil
}

func (bh *BastionHostAssetGenerator) copyFiles(sourceDir, destDir string) error {
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

func (bh *BastionHostAssetGenerator) generateTfvarsFiles(terraformDir string) error {
	if err := bh.generateInputsTfvars(terraformDir); err != nil {
		return fmt.Errorf("failed to generate inputs tfvars file: %w", err)
	}

	return nil
}

func (bh *BastionHostAssetGenerator) generateInputsTfvars(terraformDir string) error {
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
		AWSRegion        string
		PublicSubnetCIDR string
		VPCID            string
		CreateIGW        bool
		SecurityGroupIds []string
	}{
		AWSRegion:        bh.region,
		PublicSubnetCIDR: bh.publicSubnetCidr,
		VPCID:            bh.vpcId,
		CreateIGW:        bh.createIGW,
		SecurityGroupIds: bh.securityGroupIds,
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
