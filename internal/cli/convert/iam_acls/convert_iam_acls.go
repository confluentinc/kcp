package iam_acls

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/confluentinc/kcp/internal/client"
	iamservice "github.com/confluentinc/kcp/internal/services/iam"
	"github.com/confluentinc/kcp/internal/types"

	"github.com/spf13/cobra"
)

//go:embed assets
var assetsFS embed.FS

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

type ACLMapping struct {
	Operation       string
	ResourceType    string
}

// https://docs.aws.amazon.com/service-authorization/latest/reference/list_apachekafkaapisforamazonmskclusters.html
// https://docs.confluent.io/cloud/current/security/access-control/acl.html#acl-resources-and-operations-for-ccloud-summary
var aclMap = map[string]ACLMapping{
	"kafka-cluster:AlterCluster": {
		Operation:    "Alter",
		ResourceType: "Cluster",
	},
	"kafka-cluster:AlterClusterDynamicConfiguration": {
		Operation:    "AlterConfigs",
		ResourceType: "Cluster",
	},
	"kafka-cluster:AlterGroup": {
		Operation:    "Read",
		ResourceType: "Group",
	},
	"kafka-cluster:AlterTopic": {
		Operation:    "Alter",
		ResourceType: "Topic",
	},
	"kafka-cluster:AlterTopicDynamicConfiguration": {
		Operation:    "AlterConfigs",
		ResourceType: "Topic",
	},
	"kafka-cluster:AlterTransactionalId": {
		Operation:    "Write",
		ResourceType: "TransactionalId",
	},
	"kafka-cluster:CreateTopic": {
		Operation:    "Create",
		ResourceType: "Topic",
	},
	"kafka-cluster:DeleteGroup": {
		Operation:    "Delete",
		ResourceType: "Group",
	},
	"kafka-cluster:DeleteTopic": {
		Operation:    "Delete",
		ResourceType: "Topic",
	},
	"kafka-cluster:DescribeCluster": {
		Operation:    "Describe",
		ResourceType: "Cluster",
	},
	"kafka-cluster:DescribeClusterDynamicConfiguration": {
		Operation:    "Describe", // OSK would map to DescribeConfigs.
		ResourceType: "Cluster",		
	},
	"kafka-cluster:DescribeGroup": {
		Operation:    "Describe",
		ResourceType: "Group",
	},
	"kafka-cluster:DescribeTopic": {
		Operation:    "Describe",
		ResourceType: "Topic",
	},
	"kafka-cluster:DescribeTopicDynamicConfiguration": {
		Operation:    "DescribeConfigs",
		ResourceType: "Topic",
	},
	"kafka-cluster:DescribeTransactionalId": {
		Operation:    "Describe",
		ResourceType: "TransactionalId",
	},
	"kafka-cluster:ReadData": {
		Operation:    "Read",
		ResourceType: "Topic",
	},
	"kafka-cluster:WriteData": {
		Operation:    "Write",
		ResourceType: "Topic",
	},
	"kafka-cluster:WriteDataIdempotently": {
		Operation:    "IdempotentWrite",
		ResourceType: "Cluster",
	},
}

var (
	roleArn   string
	outputDir string
)

func NewConvertIamAclsCmd() *cobra.Command {
	aclsCmd := &cobra.Command{
		Use:   "iam-acls",
		Short: "Convert IAM ACLs to Confluent Cloud IAM ACLs.",
		Long:  "Convert IAM ACLs to Confluent Cloud IAM ACLs as individual Terraform resources.",

		RunE: func(cmd *cobra.Command, args []string) error {
			roleArn, _ = cmd.Flags().GetString("role-arn")

			return runConvertIamAcls(roleArn)
		},
	}

	aclsCmd.Flags().StringP("role-arn", "r", "", "IAM Role ARN to convert ACLs from (required)")
	aclsCmd.Flags().StringVar(&outputDir, "output-dir", "", "The directory to write the ACL files to")

	aclsCmd.MarkFlagRequired("role-arn")
	aclsCmd.MarkFlagRequired("output-dir")

	return aclsCmd
}

func runConvertIamAcls(roleArn string) error {
	ctx := context.Background()

	fmt.Printf("🚀 Converting IAM ACLs for role: %s\n", roleArn)

	iamClient, err := client.NewIAMClient()
	if err != nil {
		return fmt.Errorf("failed to create IAM client: %v", err)
	}

	fmt.Printf("📋 Retrieving IAM role policies for role: %s\n", roleArn)
	policies, err := iamservice.GetRolePolicies(ctx, iamClient, roleArn)
	if err != nil {
		return fmt.Errorf("failed to get role policies: %v", err)
	}

	extractedACLs, err := extractKafkaPermissions(policies)
	if err != nil {
		return fmt.Errorf("failed to extract Kafka permissions: %v", err)
	}

	if len(extractedACLs) == 0 {
		fmt.Println("No `kafka-cluster` permissions found in the specified role policies.")
		return nil
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	aclsByPrincipal := make(map[string][]TemplateACL)
	for _, acl := range extractedACLs {
		principal := cleanPrincipalName(getPrincipalFromRoleName())

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

		fmt.Printf("Generated ACL file: %s (%d ACLs)\n", filepath, len(acls))
	}

	fmt.Printf("Successfully generated ACL files for %d principals in %s\n", len(aclsByPrincipal), outputDir)

	return nil
}

func extractKafkaPermissions(policies *iamservice.RolePolicies) ([]types.Acls, error) {
	var extractedACLs []types.Acls

	for _, policy := range policies.AttachedPolicies {
		fmt.Printf("📝 Processing policy: %s\n", policy.PolicyName)

		for _, statement := range policy.PolicyDocument["Statement"].([]any) {
			statementMap := statement.(map[string]any)

			effect := "ALLOW"
			if effectVal, ok := statementMap["Effect"]; ok {
				effect = strings.ToUpper(effectVal.(string))
			}

			// Handle JSON object of actions.
			var actions []string
			switch actionData := statementMap["Action"].(type) {
			case string:
				actions = append(actions, actionData)
			case []any:
				for _, action := range actionData {
					actions = append(actions, action.(string))
				}
			}

			for _, action := range actions {
				if strings.HasPrefix(action, "kafka-cluster:") {
					mapping, found := TranslateIAMToKafkaACL(action)
					if found {
						acl := createACLFromMapping(mapping, effect)
						extractedACLs = append(extractedACLs, acl)
					} else {
						continue
					}
				}
			}
		}
	}

	return extractedACLs, nil
}

func TranslateIAMToKafkaACL(iamPermission string) (ACLMapping, bool) {
	normalizedPermission := strings.TrimSpace(iamPermission)
	acl, found := aclMap[normalizedPermission]
	return acl, found
}

func createACLFromMapping(mapping ACLMapping, effect string) types.Acls {
	resourceName := "*"
	patternType := "LITERAL"

	switch mapping.ResourceType {
	case "Cluster":
		resourceName = "kafka-cluster"
	}

	return types.Acls{
		ResourceType:        mapping.ResourceType,
		ResourceName:        resourceName,
		ResourcePatternType: patternType,
		Principal:           cleanPrincipalName(getPrincipalFromRoleName()),
		Host:                "*",
		Operation:           mapping.Operation,
		PermissionType:      effect,
	}
}

func getPrincipalFromRoleName() string {
	principal := strings.Split(roleArn, "/")[1]

	return fmt.Sprintf("User:%s", principal)
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
