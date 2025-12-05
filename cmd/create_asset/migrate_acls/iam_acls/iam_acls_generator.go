package iam_acls

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/hcl"
	iamservice "github.com/confluentinc/kcp/internal/services/iam"
	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

type MigrateIamAclsOpts struct {
	PrincipalArns             []string
	TargetClusterId           string
	TargetClusterRestEndpoint string
	OutputDir                 string
	SkipAuditReport           bool
}

type IamAclsGenerator struct {
	opts MigrateIamAclsOpts
}

func NewIamAclsGenerator(opts MigrateIamAclsOpts) *IamAclsGenerator {
	return &IamAclsGenerator{
		opts: opts,
	}
}

func (ig *IamAclsGenerator) Run() error {
	slog.Info("🏁 generating Terraform files for IAM ACLs!", "principals", ig.opts.PrincipalArns)
	ctx := context.Background()

	iamClient, err := client.NewIAMClient()
	if err != nil {
		return fmt.Errorf("failed to create IAM client: %v", err)
	}

	allAclsByPrincipal := make(map[string][]types.Acls)

	for _, principalArn := range ig.opts.PrincipalArns {
		slog.Info("📋 Retrieving IAM policies for principal", "principal", principalArn)
		policies, err := iamservice.GetPrincipalPolicies(ctx, iamClient, principalArn)
		if err != nil {
			return fmt.Errorf("failed to get principal policies: %v", err)
		}

		extractedACLs, err := ig.extractKafkaPermissionsFromPrincipalPolicies(principalArn, policies)
		if err != nil {
			return fmt.Errorf("failed to extract Kafka permissions: %v", err)
		}

		if len(extractedACLs) == 0 {
			slog.Info("⚠️ No kafka-cluster permissions found in policies", "principal", principalArn)
			continue
		}

		for _, acl := range extractedACLs {
			principal := acl.Principal
			allAclsByPrincipal[principal] = append(allAclsByPrincipal[principal], acl)
		}
	}

	if len(allAclsByPrincipal) == 0 {
		slog.Info("No `kafka-cluster` permissions found in the specified principal's policies so therefore nothing to convert.")
		return nil
	}

	outputDir := ig.opts.OutputDir
	if outputDir == "" {
		if len(ig.opts.PrincipalArns) == 1 {
			principal := cleanPrincipalName(getPrincipalFromArn(ig.opts.PrincipalArns[0]))
			outputDir = fmt.Sprintf("%s_iam_acls", principal)
		} else {
			outputDir = "iam_acls"
		}
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	request := types.MigrateAclsRequest{
		SelectedPrincipals:        allAclsByPrincipal,
		TargetClusterId:           ig.opts.TargetClusterId,
		TargetClusterRestEndpoint: ig.opts.TargetClusterRestEndpoint,
	}

	hclService := hcl.NewMigrationScriptsHCLService()
	terraformFiles, err := hclService.GenerateMigrateAclsFiles(request)
	if err != nil {
		return fmt.Errorf("failed to generate Terraform files: %w", err)
	}

	if err := ig.writeTerraformFiles(outputDir, terraformFiles); err != nil {
		return fmt.Errorf("failed to write Terraform files: %w", err)
	}

	if !ig.opts.SkipAuditReport {
		reportPath := filepath.Join(outputDir, "migrated-acls-report.md")
		if err := ig.generateIamAuditReport(allAclsByPrincipal, reportPath); err != nil {
			return fmt.Errorf("failed to generate audit report: %w", err)
		}
		slog.Info("📝 generated audit report", "path", reportPath)
	}

	totalAcls := 0
	for _, acls := range allAclsByPrincipal {
		totalAcls += len(acls)
	}

	slog.Info("✅ IAM ACLs Terraform files generated", "directory", outputDir, "principals", len(allAclsByPrincipal), "acls", totalAcls)

	return nil
}

func (ig *IamAclsGenerator) writeTerraformFiles(outputDir string, files types.TerraformFiles) error {
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

func (ig *IamAclsGenerator) extractKafkaPermissionsFromPrincipalPolicies(principalArn string, policies *iamservice.PrincipalPolicies) ([]types.Acls, error) {
	var extractedACLs []types.Acls

	for _, policy := range policies.AttachedPolicies {
		slog.Info("📝 Processing attached policy", "policy", policy.PolicyName)
		acls := ig.processPolicy(principalArn, policy.PolicyDocument)
		extractedACLs = append(extractedACLs, acls...)
	}

	for _, policy := range policies.InlinePolicies {
		slog.Info("📝 Processing inline policy", "policy", policy.PolicyName)
		acls := ig.processPolicy(principalArn, policy.PolicyDocument)
		extractedACLs = append(extractedACLs, acls...)
	}

	return extractedACLs, nil
}

func (ig *IamAclsGenerator) processPolicy(principalArn string, policyDocument map[string]any) []types.Acls {
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
						acl := ig.createACLFromMapping(principalArn, mapping, effect, resources)
						extractedACLs = append(extractedACLs, acl)
					}
				} else {
					mapping, found := ig.translateIAMToKafkaACL(action)
					if found {
						acl := ig.createACLFromMapping(principalArn, mapping, effect, resources)
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

func (ig *IamAclsGenerator) translateIAMToKafkaACL(iamPermission string) (types.ACLMapping, bool) {
	normalizedPermission := strings.TrimSpace(iamPermission)
	acl, found := types.AclMap[normalizedPermission]
	return acl, found
}

func (ig *IamAclsGenerator) createACLFromMapping(principalArn string, mapping types.ACLMapping, effect string, resources []string) types.Acls {
	// Set defaults
	resourceName := "*"
	patternType := "LITERAL"

	if mapping.RequiresPattern && len(resources) > 0 {
		parsedResourceName, parsedPatternType := ig.parseKafkaResourceFromArn(resources[0], mapping.ResourceType)

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

func (ig *IamAclsGenerator) parseKafkaResourceFromArn(arn string, resourceType string) (string, string) {
	if arn == "*" || strings.Contains(arn, ":*") {
		return "*", "LITERAL"
	}

	switch resourceType {
	case "Topic":
		return ig.parseTopicFromArn(arn)
	case "Group":
		return ig.parseGroupFromArn(arn)
	case "TransactionalId":
		return ig.parseTransactionalIdFromArn(arn)
	case "Cluster":
		return "kafka-cluster", "LITERAL"
	}

	return "*", "LITERAL"
}

// arn:aws:kafka:region:account:topic/cluster-name/cluster-id/topic-name
// arn:aws:kafka:region:account:topic/cluster-name/cluster-id/prefix-*
func (ig *IamAclsGenerator) parseTopicFromArn(arn string) (string, string) {
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
func (ig *IamAclsGenerator) parseGroupFromArn(arn string) (string, string) {
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
func (ig *IamAclsGenerator) parseTransactionalIdFromArn(arn string) (string, string) {
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
	parts := strings.Split(principalArn, "/")
	if len(parts) < 2 {
		return principalArn
	}
	return fmt.Sprintf("User:%s", parts[1])
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

func (ig *IamAclsGenerator) generateIamAuditReport(aclsByPrincipal map[string][]types.Acls, filePath string) error {
	md := markdown.New()

	md.AddHeading("IAM ACLs Audit Report", 1)
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
		ig.addAclSectionForIamPrincipal(md, acls)
	}

	return md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: filePath})
}

func (ig *IamAclsGenerator) addAclSectionForIamPrincipal(md *markdown.Markdown, migratedACLs []types.Acls) {
	type aclEntry struct {
		IamActions     string
		ResourceType   string
		ResourceName   string
		PatternType    string
		Operation      string
		PermissionType string
	}

	var aclEntries []aclEntry

	for _, acl := range migratedACLs {
		var iamActions string
		actions := resolveKafkaIAMActionsForACL(acl)
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

// resolveKafkaIAMActionsForACL returns the AWS kafka-cluster action(s) that translate to the given ACL's resource type and operation.
func resolveKafkaIAMActionsForACL(acl types.Acls) []string {
	var actions []string
	for action, mapping := range types.AclMap {
		if mapping.ResourceType == acl.ResourceType && mapping.Operation == acl.Operation {
			actions = append(actions, action)
		}
	}
	sort.Strings(actions)
	return actions
}
