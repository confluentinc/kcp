package kafka_acls

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/confluentinc/kcp/internal/types"

	"github.com/spf13/cobra"
)

//go:embed assets
var assetsFS embed.FS

var (
	clusterFile string
	outputDir   string
)

type TemplateACL struct {
	Permission   string
	ResourceType string
	Operation    string
	ResourceName string
	PatternType  string
	Principal    string
	Host         string
}

type TemplateData struct {
	Principal string
	Acls      []TemplateACL
}

func NewConvertKafkaAclsCmd() *cobra.Command {
	aclsCmd := &cobra.Command{
		Use:   "kafka-acls",
		Short: "Convert Kafka ACLs to Confluent Cloud ACLs.",
		Long:  "Convert Kafka ACLs to Confluent Cloud ACLs as individual Terraform resources.",
		
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConvertKafkaAcls()
		},
	}

	aclsCmd.Flags().StringVar(&clusterFile, "cluster-file", "", "The cluster json file produced from 'scan cluster' command")
	aclsCmd.Flags().StringVar(&outputDir, "output-dir", "", "The directory to write the ACL files to")

	aclsCmd.MarkFlagRequired("cluster-file")

	return aclsCmd
}

func runConvertKafkaAcls() error {
	if clusterFile == "" {
		return fmt.Errorf("cluster-file flag is required")
	}

	// Read and parse the cluster file
	data, err := os.ReadFile(clusterFile)
	if err != nil {
		return fmt.Errorf("failed to read cluster file: %w", err)
	}

	var clusterData types.ClusterInformation
	if err := json.Unmarshal(data, &clusterData); err != nil {
		return fmt.Errorf("failed to parse cluster JSON: %w", err)
	}

	if outputDir == "" {
		outputDir = fmt.Sprintf("%s_acls", *clusterData.Cluster.ClusterName)
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	aclsByPrincipal := make(map[string][]TemplateACL)
	for _, acl := range clusterData.Acls {
		principal := cleanPrincipalName(acl.Principal)

		templateACL := TemplateACL{
			Permission:   acl.PermissionType,
			ResourceType: acl.ResourceType,
			Operation:    acl.Operation,
			ResourceName: acl.ResourceName,
			PatternType:  acl.ResourcePatternType,
			Principal:    principal,
			Host:         acl.Host,
		}

		aclsByPrincipal[principal] = append(aclsByPrincipal[principal], templateACL)
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

	// Generate a separate file for each principal
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
