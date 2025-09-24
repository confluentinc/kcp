package kafka_acls

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

//go:embed assets
var assetsFS embed.FS

type MigrateKafkaAclsOpts struct {
	clusterName     string
	kafkaAcls       []types.Acls
	OutputDir       string
	SkipAuditReport bool
}

type KafkaAclsMigrator struct {
	clusterName     string
	kafkaAcls       []types.Acls
	outputDir       string
	skipAuditReport bool
}

type TemplateData struct {
	Principal string
	Acls      []types.Acls
}

func NewKafkaAclsMigrator(opts MigrateKafkaAclsOpts) *KafkaAclsMigrator {
	return &KafkaAclsMigrator{
		clusterName:     opts.clusterName,
		kafkaAcls:       opts.kafkaAcls,
		outputDir:       opts.OutputDir,
		skipAuditReport: opts.SkipAuditReport,
	}
}

func (kac *KafkaAclsMigrator) Run() error {
	if kac.outputDir == "" {
		kac.outputDir = fmt.Sprintf("%s_acls", kac.clusterName)
	}

	if err := os.MkdirAll(kac.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	aclsByPrincipal := make(map[string][]types.Acls)
	for _, acl := range kac.kafkaAcls {
		principal := cleanPrincipalName(acl.Principal)

		aclsByPrincipal[principal] = append(aclsByPrincipal[principal], acl)
	}

	// Load template
	tmplContent, err := assetsFS.ReadFile("assets/acls.tmpl")
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("acls").Funcs(template.FuncMap{
		"lower": strings.ToLower,
	}).Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	for principal, acls := range aclsByPrincipal {
		filename := fmt.Sprintf("%s-acls.tf", principal)
		filepath := filepath.Join(kac.outputDir, filename)

		file, err := os.Create(filepath)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", filepath, err)
		}
		defer file.Close()

		templateData := TemplateData{
			Principal: principal,
			Acls:      acls,
		}

		if err := tmpl.Execute(file, templateData); err != nil {
			return fmt.Errorf("failed to execute template for principal %s: %w", principal, err)
		}

		fmt.Printf("📝 Generated ACL file: %s (%d ACLs)\n", filepath, len(acls))
	}

	if !kac.skipAuditReport {
		reportPath := filepath.Join(kac.outputDir, "migrated-acls-report.md")

		if err := kac.generateKafkaAuditReport(aclsByPrincipal, reportPath); err != nil {
			return fmt.Errorf("failed to generate audit report: %w", err)
		}

		fmt.Printf("\n📝 Generated audit report: %s\n", reportPath)
	}

	fmt.Printf("\n✅ Successfully generated ACL files for %d principals in %s\n", len(aclsByPrincipal), kac.outputDir)
	return nil

}

// Clean the principal name for filename (remove User: prefix and special chars).
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

func (kac *KafkaAclsMigrator) generateKafkaAuditReport(aclsByPrincipal map[string][]types.Acls, filePath string) error {
	md := markdown.New()

	md.AddHeading("Audit Report", 1)
	md.AddParagraph("This report highlights the ACLs that will be migrated using the generated Terraform assets.")

	for principal, acls := range aclsByPrincipal {
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
