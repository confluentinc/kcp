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
	RequiresPattern bool
}

// https://docs.aws.amazon.com/service-authorization/latest/reference/list_apachekafkaapisforamazonmskclusters.html
// https://docs.confluent.io/cloud/current/security/access-control/acl.html#acl-resources-and-operations-for-ccloud-summary
var aclMap = map[string]ACLMapping{
	"kafka-cluster:AlterCluster": {
		Operation:       "Alter",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:AlterClusterDynamicConfiguration": {
		Operation:       "AlterConfigs",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:AlterGroup": {
		Operation:       "Read",
		ResourceType:    "Group",
		RequiresPattern: true,
	},
	"kafka-cluster:AlterTopic": {
		Operation:       "Alter",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:AlterTopicDynamicConfiguration": {
		Operation:       "AlterConfigs",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:AlterTransactionalId": {
		Operation:       "Write",
		ResourceType:    "TransactionalId",
		RequiresPattern: true,
	},
	"kafka-cluster:CreateTopic": {
		Operation:       "Create",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DeleteGroup": {
		Operation:       "Delete",
		ResourceType:    "Group",
		RequiresPattern: true,
	},
	"kafka-cluster:DeleteTopic": {
		Operation:       "Delete",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeCluster": {
		Operation:       "Describe",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:DescribeClusterDynamicConfiguration": {
		Operation:       "DescribeConfigs",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:DescribeGroup": {
		Operation:       "Describe",
		ResourceType:    "Group",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTopic": {
		Operation:       "Describe",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTopicDynamicConfiguration": {
		Operation:       "DescribeConfigs",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTransactionalId": {
		Operation:       "Describe",
		ResourceType:    "TransactionalId",
		RequiresPattern: true,
	},
	"kafka-cluster:ReadData": {
		Operation:       "Read",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:WriteData": {
		Operation:       "Write",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:WriteDataIdempotently": {
		Operation:       "IdempotentWrite",
		ResourceType:    "Cluster",
		RequiresPattern: true,
	},
}

var (
	roleArn   string
	outputDir string
)

func RunConvertIamAcls(userRoleArn, userOutputDir string) error {
	ctx := context.Background()

	roleArn = userRoleArn
	outputDir = userOutputDir

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
		fmt.Println("No `kafka-cluster` permissions found in the specified role policies so therefore nothing to convert.")
		return nil
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	aclsByPrincipal := make(map[string][]TemplateACL)
	for _, acl := range extractedACLs {
		principal := cleanPrincipalName(getPrincipalFromRoleName(roleArn))

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
	}

	fmt.Printf("\n✅ Successfully generated ACL files for '%s' in %s\n", getPrincipalFromRoleName(roleArn), outputDir)

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

			// Extract resources from the policy statement
			var resources []string
			if resourceData, ok := statementMap["Resource"]; ok {
				switch resData := resourceData.(type) {
				case string:
					resources = append(resources, resData)
				case []any:
					for _, res := range resData {
						if resStr, ok := res.(string); ok {
							resources = append(resources, resStr)
						}
					}
				}
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
						acl := createACLFromMapping(mapping, effect, resources, roleArn)
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

func createACLFromMapping(mapping ACLMapping, effect string, resources []string, roleArn string) types.Acls {
	// Set defaults
	resourceName := "*"
	patternType := "LITERAL"

	if mapping.RequiresPattern && len(resources) > 0 {
		parsedResourceName, parsedPatternType := parseKafkaResourceFromArn(resources[0], mapping.ResourceType)

		if parsedResourceName != "" {
			resourceName = parsedResourceName
			patternType = parsedPatternType
		}
	}

	return types.Acls{
		ResourceType:        mapping.ResourceType,
		ResourceName:        resourceName,
		ResourcePatternType: patternType,
		Principal:           cleanPrincipalName(getPrincipalFromRoleName(roleArn)),
		Host:                "*", // Unsure how we would retrieve this from the IAM policy.
		Operation:           mapping.Operation,
		PermissionType:      effect,
	}
}

func parseKafkaResourceFromArn(arn string, resourceType string) (string, string) {
	if arn == "*" || strings.Contains(arn, ":*") {
		return "*", "LITERAL"
	}

	switch resourceType {
	case "Topic":
		return parseTopicFromArn(arn)
	case "Group":
		return parseGroupFromArn(arn)
	case "TransactionalId":
		return parseTransactionalIdFromArn(arn)
	case "Cluster":
		return "kafka-cluster", "LITERAL"
	}

	return "*", "LITERAL"
}

// arn:aws:kafka:region:account:topic/cluster-name/cluster-id/topic-name
// arn:aws:kafka:region:account:topic/cluster-name/cluster-id/prefix-*
func parseTopicFromArn(arn string) (string, string) {
	if strings.Contains(arn, ":topic/") {
		parts := strings.Split(arn, ":topic/")

		if len(parts) == 2 {
			topicPart := parts[1]
			topicSegments := strings.Split(topicPart, "/")

			if len(topicSegments) >= 3 {
				topicName := topicSegments[len(topicSegments)-1]

				return determineResourceNameAndPattern(topicName)
			}
		}
	}

	return "*", "LITERAL"
}

// arn:aws:kafka:region:account:group/cluster-name/cluster-id/group-name
// arn:aws:kafka:region:account:group/cluster-name/cluster-id/prefix-*
func parseGroupFromArn(arn string) (string, string) {
	if strings.Contains(arn, ":group/") {
		parts := strings.Split(arn, ":group/")

		if len(parts) == 2 {
			groupPart := parts[1]
			groupSegments := strings.Split(groupPart, "/")

			if len(groupSegments) >= 3 {
				groupName := groupSegments[len(groupSegments)-1]

				return determineResourceNameAndPattern(groupName)
			}
		}
	}

	return "*", "LITERAL"
}

// arn:aws:kafka:region:account:transactional-id/cluster-name/cluster-id/txn-id
func parseTransactionalIdFromArn(arn string) (string, string) {
	if strings.Contains(arn, ":transactional-id/") {
		parts := strings.Split(arn, ":transactional-id/")

		if len(parts) == 2 {
			txnPart := parts[1]
			txnSegments := strings.Split(txnPart, "/")

			if len(txnSegments) >= 3 {
				txnId := txnSegments[len(txnSegments)-1]

				return determineResourceNameAndPattern(txnId)
			}
		}
	}

	return "*", "LITERAL"
}

func determineResourceNameAndPattern(resourceName string) (string, string) {
	if resourceName == "*" {
		return "*", "LITERAL"
	}

	// Check for prefix patterns (ending with *).
	if strings.HasSuffix(resourceName, "*") && !strings.HasPrefix(resourceName, "*") {
		// Pattern like "retention-*" = "retention-", "PREFIXED"
		prefixName := strings.TrimSuffix(resourceName, "*")
		return prefixName, "PREFIXED"
	}

	// ACLs don't support suffix patterns. Therefore, if one is found, the pattern should be `LITERAL`.
	if strings.HasPrefix(resourceName, "*") && !strings.HasSuffix(resourceName, "*") {
		return resourceName, "LITERAL"
	}

	// Again, ACLs don't support complex wildcards, therefore the pattern should be `LITERAL`.
	if strings.Contains(resourceName, "*") {
		return resourceName, "LITERAL"
	}

	return resourceName, "LITERAL"
}

func getPrincipalFromRoleName(roleArn string) string {
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
