package hcl

import (
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateMigrateAclsFiles_OperationMapping(t *testing.T) {
	request := types.MigrateAclsRequest{
		SelectedPrincipals:        []string{"user1"},
		TargetClusterId:           "lkc-abc123",
		TargetClusterRestEndpoint: "https://test.confluent.cloud:443",
		AclsByPrincipal: map[string][]types.Acls{
			"user1": {
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user1",
					Host:                "*",
					Operation:           "DescribeConfigs",
					PermissionType:      "ALLOW",
				},
				{
					ResourceType:        "Cluster",
					ResourceName:        "kafka-cluster",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user1",
					Host:                "*",
					Operation:           "AlterConfigs",
					PermissionType:      "ALLOW",
				},
				{
					ResourceType:        "Cluster",
					ResourceName:        "kafka-cluster",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user1",
					Host:                "*",
					Operation:           "IdempotentWrite",
					PermissionType:      "ALLOW",
				},
			},
		},
	}

	service := NewMigrationScriptsHCLService()
	files, err := service.GenerateMigrateAclsFiles(request)
	require.NoError(t, err)

	// Get the principal's TF file content
	require.Contains(t, files.PerPrincipalTf, "user1.tf")
	content := files.PerPrincipalTf["user1.tf"]

	assert.Contains(t, content, `"DESCRIBE_CONFIGS"`)
	assert.NotContains(t, content, `"DESCRIBECONFIGS"`)

	assert.Contains(t, content, `"ALTER_CONFIGS"`)
	assert.NotContains(t, content, `"ALTERCONFIGS"`)

	assert.Contains(t, content, `"IDEMPOTENT_WRITE"`)
	assert.NotContains(t, content, `"IDEMPOTENTWRITE"`)
}

func TestGenerateMigrateAclsFiles_NoDuplicateResourceNames(t *testing.T) {
	request := types.MigrateAclsRequest{
		SelectedPrincipals:        []string{"user_a", "user_b"},
		TargetClusterId:           "lkc-abc123",
		TargetClusterRestEndpoint: "https://test.confluent.cloud:443",
		AclsByPrincipal: map[string][]types.Acls{
			"user_a": {
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user_a",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
			},
			"user_b": {
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user_b",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
			},
		},
	}

	service := NewMigrationScriptsHCLService()
	files, err := service.GenerateMigrateAclsFiles(request)
	require.NoError(t, err)

	// Each principal should have its own file
	require.Contains(t, files.PerPrincipalTf, "user_a.tf")
	require.Contains(t, files.PerPrincipalTf, "user_b.tf")

	// Resource names should include the principal name to avoid collisions
	contentA := files.PerPrincipalTf["user_a.tf"]
	contentB := files.PerPrincipalTf["user_b.tf"]

	assert.Contains(t, contentA, `"user_a_allow_topic_read_0"`)
	assert.Contains(t, contentB, `"user_b_allow_topic_read_0"`)
}

func TestGenerateMigrateAclsFiles_PerPrincipalFiles(t *testing.T) {
	request := types.MigrateAclsRequest{
		SelectedPrincipals:        []string{"alice", "bob", "charlie"},
		TargetClusterId:           "lkc-abc123",
		TargetClusterRestEndpoint: "https://test.confluent.cloud:443",
		AclsByPrincipal: map[string][]types.Acls{
			"alice": {
				{
					ResourceType:        "Topic",
					ResourceName:        "orders",
					ResourcePatternType: "LITERAL",
					Principal:           "User:alice",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
			},
			"bob": {
				{
					ResourceType:        "Topic",
					ResourceName:        "events",
					ResourcePatternType: "LITERAL",
					Principal:           "User:bob",
					Host:                "*",
					Operation:           "Write",
					PermissionType:      "ALLOW",
				},
			},
			"charlie": {
				{
					ResourceType:        "Group",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:charlie",
					Host:                "*",
					Operation:           "Describe",
					PermissionType:      "ALLOW",
				},
			},
		},
	}

	service := NewMigrationScriptsHCLService()
	files, err := service.GenerateMigrateAclsFiles(request)
	require.NoError(t, err)

	// MainTf should be empty - all content in per-principal files
	assert.Empty(t, files.MainTf)

	// Should have exactly 3 per-principal files
	assert.Len(t, files.PerPrincipalTf, 3)
	assert.Contains(t, files.PerPrincipalTf, "alice.tf")
	assert.Contains(t, files.PerPrincipalTf, "bob.tf")
	assert.Contains(t, files.PerPrincipalTf, "charlie.tf")

	// Each file should contain the service account and ACLs for that principal only
	aliceContent := files.PerPrincipalTf["alice.tf"]
	assert.Contains(t, aliceContent, "confluent_service_account")
	assert.Contains(t, aliceContent, "alice")
	assert.NotContains(t, aliceContent, "bob")
	assert.NotContains(t, aliceContent, "charlie")

	// Shared files should still exist
	assert.NotEmpty(t, files.ProvidersTf)
	assert.NotEmpty(t, files.VariablesTf)
	assert.NotEmpty(t, files.InputsAutoTfvars)
}

func TestGenerateMigrateAclsFiles_FiltersUnsupportedResourceTypes(t *testing.T) {
	request := types.MigrateAclsRequest{
		SelectedPrincipals:        []string{"user1"},
		TargetClusterId:           "lkc-abc123",
		TargetClusterRestEndpoint: "https://test.confluent.cloud:443",
		AclsByPrincipal: map[string][]types.Acls{
			"user1": {
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user1",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
				{
					ResourceType:        "DelegationToken",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user1",
					Host:                "*",
					Operation:           "Describe",
					PermissionType:      "ALLOW",
				},
				{
					ResourceType:        "Group",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user1",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
			},
		},
	}

	service := NewMigrationScriptsHCLService()
	files, err := service.GenerateMigrateAclsFiles(request)
	require.NoError(t, err)

	content := files.PerPrincipalTf["user1.tf"]

	// DelegationToken ACLs should be filtered out
	assert.NotContains(t, content, "DELEGATION_TOKEN")
	assert.NotContains(t, content, "DelegationToken")
	assert.NotContains(t, content, "delegationtoken")

	// Supported ACLs should still be present
	assert.Contains(t, content, `"TOPIC"`)
	assert.Contains(t, content, `"GROUP"`)

	// Should only have 2 ACL resources (DelegationToken filtered out)
	resourceCount := strings.Count(content, `resource "confluent_kafka_acl"`)
	assert.Equal(t, 2, resourceCount)
}

func TestGenerateMigrateAclsFiles_PreventDestroyTrue(t *testing.T) {
	request := types.MigrateAclsRequest{
		SelectedPrincipals:        []string{"user1"},
		TargetClusterId:           "lkc-abc123",
		TargetClusterRestEndpoint: "https://test.confluent.cloud:443",
		PreventDestroy:            true,
		AclsByPrincipal: map[string][]types.Acls{
			"user1": {
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user1",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
			},
		},
	}

	service := NewMigrationScriptsHCLService()
	files, err := service.GenerateMigrateAclsFiles(request)
	require.NoError(t, err)

	content := files.PerPrincipalTf["user1.tf"]
	assert.Contains(t, content, "prevent_destroy = true")
}

func TestGenerateMigrateAclsFiles_PreventDestroyFalse(t *testing.T) {
	request := types.MigrateAclsRequest{
		SelectedPrincipals:        []string{"user1"},
		TargetClusterId:           "lkc-abc123",
		TargetClusterRestEndpoint: "https://test.confluent.cloud:443",
		PreventDestroy:            false,
		AclsByPrincipal: map[string][]types.Acls{
			"user1": {
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user1",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
			},
		},
	}

	service := NewMigrationScriptsHCLService()
	files, err := service.GenerateMigrateAclsFiles(request)
	require.NoError(t, err)

	content := files.PerPrincipalTf["user1.tf"]
	assert.NotContains(t, content, "prevent_destroy = true")
	assert.Contains(t, content, "prevent_destroy = false")
}

func TestGenerateMigrateAclsFiles_ResourceNameIncludesPrincipal(t *testing.T) {
	request := types.MigrateAclsRequest{
		SelectedPrincipals:        []string{"my_service"},
		TargetClusterId:           "lkc-abc123",
		TargetClusterRestEndpoint: "https://test.confluent.cloud:443",
		AclsByPrincipal: map[string][]types.Acls{
			"my_service": {
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:my_service",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
				{
					ResourceType:        "Topic",
					ResourceName:        "orders",
					ResourcePatternType: "LITERAL",
					Principal:           "User:my_service",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:my_service",
					Host:                "*",
					Operation:           "DescribeConfigs",
					PermissionType:      "ALLOW",
				},
			},
		},
	}

	service := NewMigrationScriptsHCLService()
	files, err := service.GenerateMigrateAclsFiles(request)
	require.NoError(t, err)

	content := files.PerPrincipalTf["my_service.tf"]

	// Each resource name should be unique and contain the principal
	assert.Contains(t, content, `"my_service_allow_topic_read_0"`)
	assert.Contains(t, content, `"my_service_allow_topic_read_1"`)
	assert.Contains(t, content, `"my_service_allow_topic_describe_configs_2"`)

	// Count resource blocks - should have exactly 3
	resourceCount := strings.Count(content, `resource "confluent_kafka_acl"`)
	assert.Equal(t, 3, resourceCount)
}
