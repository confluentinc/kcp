package migration_infra

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

//go:embed assets
var assetsFS embed.FS

type MigrationInfraOpts struct {
	Region                        string
	VPCId                         string
	JumpClusterBrokerSubnetConfig string
	CCEnvName                     string
	CCClusterName                 string
	CCClusterType                 string
	AnsibleControlNodeSubnetCIDR  string
	JumpClusterBrokerIAMRoleName  string
	SecurityGroupIds              string

	ClusterInfo        types.ClusterInformation
	MigrationInfraType types.MigrationInfraType
}

type MigrationInfraAssetGenerator struct {
	region                        string
	vpcId                         string
	jumpClusterBrokerSubnetConfig string
	ccEnvName                     string
	ccClusterName                 string
	ccClusterType                 string
	ansibleControlNodeSubnetCIDR  string
	jumpClusterBrokerIAMRoleName  string
	securityGroupIds              string

	clusterInfo        types.ClusterInformation
	migrationInfraType types.MigrationInfraType
}

func NewMigrationInfraAssetGenerator(opts MigrationInfraOpts) *MigrationInfraAssetGenerator {
	return &MigrationInfraAssetGenerator{
		region:                        opts.Region,
		vpcId:                         opts.VPCId,
		jumpClusterBrokerSubnetConfig: opts.JumpClusterBrokerSubnetConfig,
		ccEnvName:                     opts.CCEnvName,
		ccClusterName:                 opts.CCClusterName,
		ccClusterType:                 opts.CCClusterType,
		ansibleControlNodeSubnetCIDR:  opts.AnsibleControlNodeSubnetCIDR,
		jumpClusterBrokerIAMRoleName:  opts.JumpClusterBrokerIAMRoleName,
		clusterInfo:                   opts.ClusterInfo,
		migrationInfraType:            opts.MigrationInfraType,
		securityGroupIds:              opts.SecurityGroupIds,
	}
}

func (mi *MigrationInfraAssetGenerator) Run() error {
	slog.Info("🏁 generating target environment assets", "targetType", mi.migrationInfraType)

	outputDir := "migration_infra"
	slog.Info("📁 creating migration infra directory", "directory", outputDir)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create migration infra directory: %w", err)
	}

	assetsDir := mi.getAssetsDir()
	slog.Info("📋 copying assets to target directory", "from", assetsDir, "to", outputDir)

	if err := mi.copyFiles(assetsDir, outputDir); err != nil {
		return fmt.Errorf("failed to copy assets: %w", err)
	}

	if err := mi.generateTfvarsFiles(outputDir); err != nil {
		return fmt.Errorf("failed to generate tfvars files: %w", err)
	}

	if err := mi.generateManifest(outputDir); err != nil {
		return fmt.Errorf("failed to generate manifest: %w", err)
	}

	slog.Info("✅ migration infra assets generated", "directory", outputDir)

	return nil
}

func (mi *MigrationInfraAssetGenerator) getAssetsDir() string {
	switch mi.migrationInfraType {
	case types.MskCpCcPrivateSaslIam:
		return "assets/msk-cp-cc-private-sasl-iam"
	case types.MskCpCcPrivateSaslScram:
		return "assets/msk-cp-cc-private-sasl-scram"
	case types.MskCcPublic:
		return "assets/msk-cc-public"
	default:
		return "assets/msk-cc-public"
	}
}

func (mi *MigrationInfraAssetGenerator) copyFiles(sourceDir, destDir string) error {
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

func (mi *MigrationInfraAssetGenerator) generateTfvarsFiles(outputDir string) error {
	if err := mi.generateInputsTfvars(outputDir); err != nil {
		return fmt.Errorf("failed to generate inputs tfvars file: %w", err)
	}

	return nil
}

func (mi *MigrationInfraAssetGenerator) generateInputsTfvars(outputDir string) error {
	// Read the Go template file from embedded assets - use auth-specific template path
	templatePath := filepath.Join(mi.getAssetsDir(), "inputs.auto.tfvars.go.tmpl")
	templateContent, err := assetsFS.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	// Parse the template
	tmpl, err := template.New("tfvars").Parse(string(templateContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	// Validate and parse AWS zones
	awsZones := []utils.AWSZone{}
	if mi.migrationInfraType == types.MskCpCcPrivateSaslIam || mi.migrationInfraType == types.MskCpCcPrivateSaslScram {
		awsZones, err = utils.ValidateAWSZones(mi.jumpClusterBrokerSubnetConfig)
		if err != nil {
			return err
		}
	}

	// Get bootstrap brokers based on auth type
	var bootstrapBrokers string
	switch mi.migrationInfraType {
	case types.MskCpCcPrivateSaslIam:
		bootstrapBrokers = aws.ToString(mi.clusterInfo.BootstrapBrokers.BootstrapBrokerStringSaslIam)
	case types.MskCpCcPrivateSaslScram:
		bootstrapBrokers = aws.ToString(mi.clusterInfo.BootstrapBrokers.BootstrapBrokerStringSaslScram)
	case types.MskCcPublic:
		bootstrapBrokers = aws.ToString(mi.clusterInfo.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram)
	default:
		return fmt.Errorf("invalid target type: %d", mi.migrationInfraType)
	}

	// Prepare template data
	templateData := struct {
		ConfluentCloudProvider             string
		ConfluentCloudRegion               string
		ConfluentCloudEnvironmentName      string
		ConfluentCloudClusterName          string
		ConfluentCloudClusterType          string
		AnsibleControlNodeSubnetCIDR       string
		MSKClusterID                       string
		MSKClusterBootstrapBrokers         string
		MSKClusterARN                      string
		ConfluentPlatformBrokerIAMRoleName string
		CustomerVPCID                      string
		AWSZones                           []utils.AWSZone
		AWSRegion                          string
		SecurityGroupIds                   string
	}{
		ConfluentCloudProvider:             "AWS",
		ConfluentCloudRegion:               mi.clusterInfo.Region,
		ConfluentCloudEnvironmentName:      mi.ccEnvName,
		ConfluentCloudClusterName:          mi.ccClusterName,
		ConfluentCloudClusterType:          mi.ccClusterType,
		AnsibleControlNodeSubnetCIDR:       mi.ansibleControlNodeSubnetCIDR,
		MSKClusterID:                       mi.clusterInfo.ClusterID,
		MSKClusterBootstrapBrokers:         bootstrapBrokers,
		MSKClusterARN:                      aws.ToString(mi.clusterInfo.Cluster.ClusterArn),
		ConfluentPlatformBrokerIAMRoleName: mi.jumpClusterBrokerIAMRoleName,
		CustomerVPCID:                      mi.vpcId,
		AWSZones:                           awsZones,
		AWSRegion:                          mi.region,
		SecurityGroupIds:                   mi.securityGroupIds,
	}
	// Execute template
	var buf strings.Builder
	if err := tmpl.Execute(&buf, templateData); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Write the generated content to inputs.auto.tfvars
	tfvarsPath := filepath.Join(outputDir, "inputs.auto.tfvars")
	if err := os.WriteFile(tfvarsPath, []byte(buf.String()), 0644); err != nil {
		return fmt.Errorf("failed to write tfvars file: %w", err)
	}

	slog.Info("✅ generated inputs tfvars file from template", "file", tfvarsPath)
	return nil
}

func (mi *MigrationInfraAssetGenerator) generateManifest(outputDir string) error {
	manifest := types.Manifest{
		MigrationInfraType: mi.migrationInfraType,
	}

	manifestPath := filepath.Join(outputDir, "manifest.json")
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(manifestPath, manifestBytes, 0644); err != nil {
		return fmt.Errorf("failed to write manifest file: %w", err)
	}

	slog.Info("✅ generated manifest file", "file", manifestPath)

	return nil
}
