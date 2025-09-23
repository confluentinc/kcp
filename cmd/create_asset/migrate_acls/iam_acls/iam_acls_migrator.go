package iam_acls

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/client"
	iamservice "github.com/confluentinc/kcp/internal/services/iam"
	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

//go:embed assets
var assetsFS embed.FS

type TemplateData struct {
	Principal string
	Acls      []types.Acls
}

type MigrateIamAclsOpts struct {
	PrincipalArns   []string
	OutputDir       string
	SkipAuditReport bool
}

type IamAclsMigrator struct {
	principalArns   []string
	outputDir       string
	skipAuditReport bool
}

func NewIamAclsMigrator(opts MigrateIamAclsOpts) *IamAclsMigrator {
	return &IamAclsMigrator{
		principalArns:   opts.PrincipalArns,
		outputDir:       opts.OutputDir,
		skipAuditReport: opts.SkipAuditReport,
	}
}

func (iac *IamAclsMigrator) Run() error {
	slog.Info("🚀 Converting IAM ACLs for principals", "principals", iac.principalArns)
	ctx := context.Background()

	iamClient, err := client.NewIAMClient()
	if err != nil {
		return fmt.Errorf("failed to create IAM client: %v", err)
	}

	for _, principal := range iac.principalArns {
		slog.Info("📋 Retrieving IAM policies for principal", "principal", principal)
		policies, err := iamservice.GetPrincipalPolicies(ctx, iamClient, principal)
		if err != nil {
			return fmt.Errorf("failed to get principal policies: %v", err)
		}

		extractedACLs, err := iac.extractKafkaPermissionsFromPrincipalPolicies(principal, policies)
		if err != nil {
			return fmt.Errorf("failed to extract Kafka permissions: %v", err)
		}

		if len(extractedACLs) == 0 {
			fmt.Println("No `kafka-cluster` permissions found in the specified principal's policies so therefore nothing to convert.")
			return nil
		}

		if iac.outputDir == "" {
			principal := iac.cleanPrincipalName(iac.getPrincipalFromArn(principal))
			iac.outputDir = fmt.Sprintf("%s_iam_acls", principal)
		}

		if err := os.MkdirAll(iac.outputDir, 0755); err != nil {
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
			filepath := filepath.Join(iac.outputDir, filename)

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

		principal := iac.cleanPrincipalName(iac.getPrincipalFromArn(principal))

		if !iac.skipAuditReport {
			aclAuditReportPath := filepath.Join(iac.outputDir, "migrated-acls-report.md")
			if err := iac.generateIamAuditReport(principal, extractedACLs, aclAuditReportPath, "iam"); err != nil {
				return fmt.Errorf("failed to generate audit report: %w", err)
			}
		}
	}

	for _, principal := range iac.principalArns {
		principal := iac.cleanPrincipalName(iac.getPrincipalFromArn(principal))
		slog.Info("✅ Successfully generated ACL files", "principal", principal, "outputDir", iac.outputDir)
	}

	slog.Info(fmt.Sprintf("✅ Successfully generated ACL files for %d principals in %s", len(iac.principalArns), iac.outputDir))

	return nil
}

func (iac *IamAclsMigrator) extractKafkaPermissionsFromPrincipalPolicies(principalArn string, policies *iamservice.PrincipalPolicies) ([]types.Acls, error) {
	var extractedACLs []types.Acls

	for _, policy := range policies.AttachedPolicies {
		fmt.Printf("📝 Processing attached policy: %s\n", policy.PolicyName)
		acls := iac.processPolicy(principalArn, policy.PolicyDocument)
		extractedACLs = append(extractedACLs, acls...)
	}

	for _, policy := range policies.InlinePolicies {
		fmt.Printf("📝 Processing inline policy: %s\n", policy.PolicyName)
		acls := iac.processPolicy(principalArn, policy.PolicyDocument)
		extractedACLs = append(extractedACLs, acls...)
	}

	return extractedACLs, nil
}

func (iac *IamAclsMigrator) processPolicy(principalArn string, policyDocument map[string]any) []types.Acls {
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
						acl := iac.createACLFromMapping(principalArn, mapping, effect, resources)
						extractedACLs = append(extractedACLs, acl)
					}
				} else {
					mapping, found := iac.translateIAMToKafkaACL(action)
					if found {
						acl := iac.createACLFromMapping(principalArn, mapping, effect, resources)
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

func (iac *IamAclsMigrator) translateIAMToKafkaACL(iamPermission string) (types.ACLMapping, bool) {
	normalizedPermission := strings.TrimSpace(iamPermission)
	acl, found := types.AclMap[normalizedPermission]
	return acl, found
}

func (iac *IamAclsMigrator) createACLFromMapping(principalArn string, mapping types.ACLMapping, effect string, resources []string) types.Acls {
	// Set defaults
	resourceName := "*"
	patternType := "LITERAL"

	if mapping.RequiresPattern && len(resources) > 0 {
		parsedResourceName, parsedPatternType := iac.parseKafkaResourceFromArn(resources[0], mapping.ResourceType)

		if parsedResourceName != "" {
			resourceName = parsedResourceName
			patternType = parsedPatternType
		}
	}

	return types.Acls{
		ResourceType:        mapping.ResourceType,
		ResourceName:        resourceName,
		ResourcePatternType: patternType,
		Principal:           iac.cleanPrincipalName(iac.getPrincipalFromArn(principalArn)),
		Host:                "*", // Unsure how we would retrieve this from the IAM policy.
		Operation:           mapping.Operation,
		PermissionType:      effect,
	}
}

func (iac *IamAclsMigrator) parseKafkaResourceFromArn(arn string, resourceType string) (string, string) {
	if arn == "*" || strings.Contains(arn, ":*") {
		return "*", "LITERAL"
	}

	switch resourceType {
	case "Topic":
		return iac.parseTopicFromArn(arn)
	case "Group":
		return iac.parseGroupFromArn(arn)
	case "TransactionalId":
		return iac.parseTransactionalIdFromArn(arn)
	case "Cluster":
		return "kafka-cluster", "LITERAL"
	}

	return "*", "LITERAL"
}

// arn:aws:kafka:region:account:topic/cluster-name/cluster-id/topic-name
// arn:aws:kafka:region:account:topic/cluster-name/cluster-id/prefix-*
func (iac *IamAclsMigrator) parseTopicFromArn(arn string) (string, string) {
	if strings.Contains(arn, ":topic/") {
		parts := strings.Split(arn, ":topic/")

		if len(parts) == 2 {
			topicPart := parts[1]
			topicSegments := strings.Split(topicPart, "/")

			if len(topicSegments) >= 3 {
				topicName := topicSegments[len(topicSegments)-1]

				return iac.determineResourceNameAndPattern(topicName)
			}
		}
	}

	return "*", "LITERAL"
}

// arn:aws:kafka:region:account:group/cluster-name/cluster-id/group-name
// arn:aws:kafka:region:account:group/cluster-name/cluster-id/prefix-*
func (iac *IamAclsMigrator) parseGroupFromArn(arn string) (string, string) {
	if strings.Contains(arn, ":group/") {
		parts := strings.Split(arn, ":group/")

		if len(parts) == 2 {
			groupPart := parts[1]
			groupSegments := strings.Split(groupPart, "/")

			if len(groupSegments) >= 3 {
				groupName := groupSegments[len(groupSegments)-1]

				return iac.determineResourceNameAndPattern(groupName)
			}
		}
	}

	return "*", "LITERAL"
}

// arn:aws:kafka:region:account:transactional-id/cluster-name/cluster-id/txn-id
func (iac *IamAclsMigrator) parseTransactionalIdFromArn(arn string) (string, string) {
	if strings.Contains(arn, ":transactional-id/") {
		parts := strings.Split(arn, ":transactional-id/")

		if len(parts) == 2 {
			txnPart := parts[1]
			txnSegments := strings.Split(txnPart, "/")

			if len(txnSegments) >= 3 {
				txnId := txnSegments[len(txnSegments)-1]

				return iac.determineResourceNameAndPattern(txnId)
			}
		}
	}

	return "*", "LITERAL"
}

func (iac *IamAclsMigrator) determineResourceNameAndPattern(resourceName string) (string, string) {
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

func (iac *IamAclsMigrator) getPrincipalFromArn(principalArn string) string {
	parts := strings.Split(principalArn, "/")[1]
	return fmt.Sprintf("User:%s", parts)
}

// Clean the principal name for filename (remove User: prefix and special chars).
func (iac *IamAclsMigrator) cleanPrincipalName(principal string) string {
	name := strings.TrimPrefix(principal, "User:")

	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, "@", "_")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")

	return strings.ToLower(name)
}

func (iac *IamAclsMigrator) generateIamAuditReport(principalName string, migratedACls []types.Acls, filePath string, aclSource string) error {
	md := markdown.New()

	md.AddHeading("Audit Report", 1)
	md.AddParagraph(fmt.Sprintf("This report highlights the ACLs that will be migrated using the generated Terraform assets for %s.", principalName))

	md.AddHeading(fmt.Sprintf("Principal: %s", principalName), 2)
	iac.addAclSectionForIamPrincipal(md, migratedACls)

	return md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: filePath})
}

func (iac *IamAclsMigrator) addAclSectionForIamPrincipal(md *markdown.Markdown, migratedACls []types.Acls) {
	type aclEntry struct {
		IamActions     string
		ResourceType   string
		ResourceName   string
		PatternType    string
		Operation      string
		PermissionType string
	}

	var aclEntries []aclEntry

	for _, acl := range migratedACls {
		var iamActions string
		actions := iac.resolveKafkaIAMActionsForACL(acl)
		if len(actions) > 0 {
			iamActions = strings.Join(actions, ", ")
		}

		entry := aclEntry{
			IamActions:     iamActions,
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

	// Sort entries by resource type, resource name, operation
	sort.Slice(aclEntries, func(i, j int) bool {
		if aclEntries[i].ResourceType != aclEntries[j].ResourceType {
			return aclEntries[i].ResourceType < aclEntries[j].ResourceType
		}

		if aclEntries[i].ResourceName != aclEntries[j].ResourceName {
			return aclEntries[i].ResourceName < aclEntries[j].ResourceName
		}
		return aclEntries[i].Operation < aclEntries[j].Operation
	})

	headers := []string{"IAM Action", "Resource Type", "Resource Name", "Pattern Type", "Operation", "Permission Type"}

	var tableData [][]string

	for _, entry := range aclEntries {
		row := []string{
			entry.IamActions,
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

// Returns the AWS kafka-cluster action(s) that translate to the given ACL's resource type and operation.
func (iac *IamAclsMigrator) resolveKafkaIAMActionsForACL(acl types.Acls) []string {
	var actions []string
	for action, mapping := range types.AclMap {
		if mapping.ResourceType == acl.ResourceType && mapping.Operation == acl.Operation {
			actions = append(actions, action)
		}
	}
	sort.Strings(actions)
	return actions
}
