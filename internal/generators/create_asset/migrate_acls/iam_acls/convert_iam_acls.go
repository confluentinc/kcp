package iam_acls

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/generators/create_asset/migrate_acls"
	iamservice "github.com/confluentinc/kcp/internal/services/iam"
	"github.com/confluentinc/kcp/internal/types"
)

//go:embed assets
var assetsFS embed.FS

type TemplateData struct {
	Principal string
	Acls      []types.Acls
}

var (
	principalArns   []string
	outputDir       string
	skipAuditReport bool
)

func RunConvertIamAcls(userPrincipalArns []string, userOutputDir string, userSkipAuditReport bool) error {
	ctx := context.Background()

	principalArns = userPrincipalArns
	outputDir = userOutputDir
	skipAuditReport = userSkipAuditReport

	slog.Info("🚀 Converting IAM ACLs for principals", "principals", principalArns)

	iamClient, err := client.NewIAMClient()
	if err != nil {
		return fmt.Errorf("failed to create IAM client: %v", err)
	}

	for _, principal := range principalArns {
		slog.Info("📋 Retrieving IAM policies for principal", "principal", principal)
		policies, err := iamservice.GetPrincipalPolicies(ctx, iamClient, principal)
		if err != nil {
			return fmt.Errorf("failed to get principal policies: %v", err)
		}

		extractedACLs, err := extractKafkaPermissionsFromPrincipalPolicies(principal, policies)
		if err != nil {
			return fmt.Errorf("failed to extract Kafka permissions: %v", err)
		}

		if len(extractedACLs) == 0 {
			fmt.Println("No `kafka-cluster` permissions found in the specified principal's policies so therefore nothing to convert.")
			return nil
		}

		if outputDir == "" {
			principal := cleanPrincipalName(getPrincipalFromArn(principal))
			outputDir = fmt.Sprintf("%s_iam_acls", principal)
		}

		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}

		aclsByPrincipal := make(map[string][]types.Acls)
		for _, acl := range extractedACLs {
			principal := acl.Principal
			aclsByPrincipal[principal] = append(aclsByPrincipal[principal], acl)
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

		principal := cleanPrincipalName(getPrincipalFromArn(principal))

		if !skipAuditReport {
			aclAuditReportPath := filepath.Join(outputDir, "migrated-acls-report.md")
			if err := migrate_acls.GenerateIamAuditReport(principal, extractedACLs, aclAuditReportPath, "iam"); err != nil {
				return fmt.Errorf("failed to generate audit report: %w", err)
			}
		}
	}

	for _, principal := range principalArns {
		principal := cleanPrincipalName(getPrincipalFromArn(principal))
		slog.Info("✅ Successfully generated ACL files", "principal", principal, "outputDir", outputDir)
	}

	slog.Info(fmt.Sprintf("✅ Successfully generated ACL files for %d principals in %s", len(principalArns), outputDir))

	return nil
}

func extractKafkaPermissionsFromPrincipalPolicies(principalArn string, policies *iamservice.PrincipalPolicies) ([]types.Acls, error) {
	var extractedACLs []types.Acls

	for _, policy := range policies.AttachedPolicies {
		fmt.Printf("📝 Processing attached policy: %s\n", policy.PolicyName)
		acls := processPolicy(principalArn, policy.PolicyDocument)
		extractedACLs = append(extractedACLs, acls...)
	}

	for _, policy := range policies.InlinePolicies {
		fmt.Printf("📝 Processing inline policy: %s\n", policy.PolicyName)
		acls := processPolicy(principalArn, policy.PolicyDocument)
		extractedACLs = append(extractedACLs, acls...)
	}

	return extractedACLs, nil
}

func processPolicy(principalArn string, policyDocument map[string]any) []types.Acls {
	var extractedACLs []types.Acls

	for _, statement := range policyDocument["Statement"].([]any) {
		statementMap := statement.(map[string]any)

		var effect string
		if effectVal, ok := statementMap["Effect"]; ok {
			effect = strings.ToUpper(effectVal.(string))
		}

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
				if action == "kafka-cluster:*" {
					// If wildcard on action - apply all ACL mappings.
					for _, mapping := range types.AclMap {
						acl := createACLFromMapping(principalArn, mapping, effect, resources)
						extractedACLs = append(extractedACLs, acl)
					}
				} else {
					mapping, found := TranslateIAMToKafkaACL(action)
					if found {
						acl := createACLFromMapping(principalArn, mapping, effect, resources)
						extractedACLs = append(extractedACLs, acl)
					} else {
						continue
					}
				}
			}
		}
	}

	return extractedACLs
}

func TranslateIAMToKafkaACL(iamPermission string) (types.ACLMapping, bool) {
	normalizedPermission := strings.TrimSpace(iamPermission)
	acl, found := types.AclMap[normalizedPermission]
	return acl, found
}

func createACLFromMapping(principalArn string, mapping types.ACLMapping, effect string, resources []string) types.Acls {
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
		Principal:           cleanPrincipalName(getPrincipalFromArn(principalArn)),
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

func getPrincipalFromArn(principalArn string) string {
	parts := strings.Split(principalArn, "/")[1]
	return fmt.Sprintf("User:%s", parts)
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
