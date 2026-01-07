package kafka_acls

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrateKafkaAclsOpts struct {
	ClusterName               string
	KafkaAcls                 []types.Acls
	TargetClusterId           string
	TargetClusterRestEndpoint string
	OutputDir                 string
	SkipAuditReport           bool
}

type KafkaAclsGenerator struct {
	opts MigrateKafkaAclsOpts
}

func NewKafkaAclsGenerator(opts MigrateKafkaAclsOpts) *KafkaAclsGenerator {
	return &KafkaAclsGenerator{
		opts: opts,
	}
}

func (kg *KafkaAclsGenerator) Run() error {
	slog.Info("🏁 generating Terraform files for Kafka ACLs!")

	outputDir := kg.opts.OutputDir
	if outputDir == "" {
		outputDir = fmt.Sprintf("%s_kafka_acls", kg.opts.ClusterName)
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	aclsByPrincipal := make(map[string][]types.Acls)
	for _, acl := range kg.opts.KafkaAcls {
		principal := cleanPrincipalName(acl.Principal)
		aclsByPrincipal[principal] = append(aclsByPrincipal[principal], acl)
	}

	principalNames := make([]string, 0, len(aclsByPrincipal))
	for principal := range aclsByPrincipal {
		principalNames = append(principalNames, principal)
	}
	
	request := types.MigrateAclsRequest{
		SelectedPrincipals:        principalNames,
		TargetClusterId:           kg.opts.TargetClusterId,
		TargetClusterRestEndpoint: kg.opts.TargetClusterRestEndpoint,
		AclsByPrincipal:           aclsByPrincipal,
	}

	hclService := hcl.NewMigrationScriptsHCLService()
	terraformFiles, err := hclService.GenerateMigrateAclsFiles(request)
	if err != nil {
		return fmt.Errorf("failed to generate Terraform files: %w", err)
	}

	if err := kg.writeTerraformFiles(outputDir, terraformFiles); err != nil {
		return fmt.Errorf("failed to write Terraform files: %w", err)
	}

	if !kg.opts.SkipAuditReport {
		reportPath := filepath.Join(outputDir, "migrated-acls-report.md")
		if err := kg.generateKafkaAuditReport(aclsByPrincipal, reportPath); err != nil {
			return fmt.Errorf("failed to generate audit report: %w", err)
		}
		slog.Info("📝 generated audit report", "path", reportPath)
	}

	totalAcls := 0
	for _, acls := range aclsByPrincipal {
		totalAcls += len(acls)
	}

	slog.Info("✅ Kafka ACLs Terraform files generated", "directory", outputDir, "principals", len(aclsByPrincipal), "acls", totalAcls)

	return nil
}

func (kg *KafkaAclsGenerator) writeTerraformFiles(outputDir string, files types.TerraformFiles) error {
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

	if files.InputsAutoTfvars != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "inputs.auto.tfvars"), []byte(files.InputsAutoTfvars), 0644); err != nil {
			return fmt.Errorf("failed to write inputs.auto.tfvars: %w", err)
		}
		slog.Info("✅ wrote inputs.auto.tfvars")
	}

	return nil
}

// cleanPrincipalName cleans the principal name for use in Terraform resources
func cleanPrincipalName(principal string) string {
	name := strings.TrimPrefix(principal, "User:")

	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, "@", "_")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")

	return strings.ToLower(name)
}

func (kg *KafkaAclsGenerator) generateKafkaAuditReport(aclsByPrincipal map[string][]types.Acls, filePath string) error {
	md := markdown.New()

	md.AddHeading("Kafka ACLs Audit Report", 1)
	md.AddParagraph("This report highlights the ACLs that will be migrated using the generated Terraform assets.")

	// Sort principals for consistent output
	var principals []string
	for principal := range aclsByPrincipal {
		principals = append(principals, principal)
	}
	sort.Strings(principals)

	for _, principal := range principals {
		acls := aclsByPrincipal[principal]
		md.AddHeading(fmt.Sprintf("Principal: %s", principal), 2)
		addAclSectionForKafkaPrincipal(md, acls)
	}

	return md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: filePath})
}

func addAclSectionForKafkaPrincipal(md *markdown.Markdown, acls []types.Acls) {
	type aclEntry struct {
		ResourceType   string
		ResourceName   string
		PatternType    string
		Operation      string
		PermissionType string
	}

	var aclEntries []aclEntry

	for _, acl := range acls {
		entry := aclEntry{
			ResourceType:   acl.ResourceType,
			ResourceName:   acl.ResourceName,
			PatternType:    acl.ResourcePatternType,
			Operation:      acl.Operation,
			PermissionType: acl.PermissionType,
		}
		aclEntries = append(aclEntries, entry)
	}

	if len(aclEntries) == 0 {
		md.AddParagraph("No ACLs found.")
		return
	}

	sort.Slice(aclEntries, func(i, j int) bool {
		if aclEntries[i].ResourceType != aclEntries[j].ResourceType {
			return aclEntries[i].ResourceType < aclEntries[j].ResourceType
		}

		if aclEntries[i].ResourceName != aclEntries[j].ResourceName {
			return aclEntries[i].ResourceName < aclEntries[j].ResourceName
		}
		return aclEntries[i].Operation < aclEntries[j].Operation
	})

	headers := []string{"Resource Type", "Resource Name", "Pattern Type", "Operation", "Permission Type"}

	var tableData [][]string

	for _, entry := range aclEntries {
		row := []string{
			entry.ResourceType,
			entry.ResourceName,
			entry.PatternType,
			entry.Operation,
			entry.PermissionType,
		}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)
}

