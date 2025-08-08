package migrate_acls

import (
	"fmt"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

func GenerateAuditReport(principalName string, migratedACls []types.Acls, filePath string, aclSource string) error {
	md := markdown.New()

	md.AddHeading("Audit Report", 1)
	md.AddParagraph(fmt.Sprintf("This report highlights the ACLs that will be migrated using the generated Terraform assets for %s.", principalName))

	switch aclSource {
	case "iam":
		md.AddHeading(fmt.Sprintf("Principal: %s", principalName), 2)
		addAclSectionForIamPrincipal(md, migratedACls)
	}

	return md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: filePath})
}

func addAclSectionForIamPrincipal(md *markdown.Markdown, migratedACls []types.Acls) {
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

// Returns the AWS kafka-cluster action(s) that translate to the given ACL's resource type and operation.
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
