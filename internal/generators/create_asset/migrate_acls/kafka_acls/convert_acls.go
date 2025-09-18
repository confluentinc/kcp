package kafka_acls

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/confluentinc/kcp/internal/generators/create_asset/migrate_acls"
	"github.com/confluentinc/kcp/internal/types"
)

//go:embed assets
var assetsFS embed.FS

var (
	clusterName     string
	kafkaAcls       []types.Acls
	outputDir       string
	skipAuditReport bool
)

type TemplateData struct {
	Principal string
	Acls      []types.Acls
}

func RunConvertKafkaAcls(userClusterName string, userKafkaAcls []types.Acls, userOutputDir string, userSkipAuditReport bool) error {
	clusterName = userClusterName
	kafkaAcls = userKafkaAcls
	outputDir = userOutputDir
	skipAuditReport = userSkipAuditReport

	if outputDir == "" {
		outputDir = fmt.Sprintf("%s_acls", clusterName)
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	aclsByPrincipal := make(map[string][]types.Acls)
	for _, acl := range kafkaAcls {
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
		filepath := filepath.Join(outputDir, filename)

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

	if !skipAuditReport {
		reportPath := filepath.Join(outputDir, "migrated-acls-report.md")

		if err := migrate_acls.GenerateKafkaAuditReport(aclsByPrincipal, reportPath); err != nil {
			return fmt.Errorf("failed to generate audit report: %w", err)
		}

		fmt.Printf("\n📝 Generated audit report: %s\n", reportPath)
	}

	fmt.Printf("\n✅ Successfully generated ACL files for %d principals in %s\n", len(aclsByPrincipal), outputDir)
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
