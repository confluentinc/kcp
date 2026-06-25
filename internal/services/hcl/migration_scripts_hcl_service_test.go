//go:build terraform_validation

package hcl

import (
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/hcl/hclrequests"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Migrate Topics tests — file-per-topic layout for both --mode mirror and new.
// ============================================================================

func TestGenerateMirrorTopicsFiles_FilePerTopicLayout(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := hclrequests.MirrorTopicsRequest{
		Topics: []types.TopicDetails{
			{Name: "orders"},
			{Name: "events"},
			{Name: "users"},
		},
		ClusterLinkName:           "msk-to-cc-link",
		TargetClusterId:           "lkc-xyz789",
		TargetClusterRestEndpoint: "https://pkc.cc.example.com:443",
		Mode:                      hclrequests.MigrateTopicsModeMirror,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	require.NoError(t, err)
	require.Len(t, project.Folders, 1)

	folder := project.Folders[0]
	assert.NotEmpty(t, folder.ProvidersTf, "providers.tf should be populated")
	assert.NotEmpty(t, folder.VariablesTf, "variables.tf should be populated")
	assert.Empty(t, folder.MainTf, "main.tf should not be emitted (per-topic layout)")

	// Expect exactly 3 per-topic files, named after sanitized topic names.
	assert.Len(t, folder.AdditionalFiles, 3)
	for _, name := range []string{"orders.tf", "events.tf", "users.tf"} {
		assert.Contains(t, folder.AdditionalFiles, name, "missing per-topic file %s", name)
	}

	// Each file contains exactly one mirror-topic resource block.
	for filename, content := range folder.AdditionalFiles {
		count := strings.Count(content, `resource "confluent_kafka_mirror_topic"`)
		assert.Equal(t, 1, count, "%s should contain exactly one mirror-topic resource", filename)
	}
}

func TestGenerateMirrorTopicsFiles_SanitizesFilenames(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := hclrequests.MirrorTopicsRequest{
		Topics: []types.TopicDetails{
			{Name: "orders.payments"},
			{Name: "events-stream"},
		},
		ClusterLinkName:           "link",
		TargetClusterId:           "lkc-xyz",
		TargetClusterRestEndpoint: "https://cc.example.com:443",
		Mode:                      hclrequests.MigrateTopicsModeMirror,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	require.NoError(t, err)
	folder := project.Folders[0]

	assert.Contains(t, folder.AdditionalFiles, "orders_payments.tf")
	assert.Contains(t, folder.AdditionalFiles, "events_stream.tf")
}

func TestGenerateMirrorTopicsFiles_FilenameCollisionIsHardError(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	// These two names both sanitize to "orders_dlq.tf" via FormatHclResourceName.
	request := hclrequests.MirrorTopicsRequest{
		Topics: []types.TopicDetails{
			{Name: "orders.dlq"},
			{Name: "orders_dlq"},
		},
		ClusterLinkName:           "link",
		TargetClusterId:           "lkc-xyz",
		TargetClusterRestEndpoint: "https://cc.example.com:443",
		Mode:                      hclrequests.MigrateTopicsModeMirror,
	}

	_, err := service.GenerateMirrorTopicsFiles(request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filename collisions detected")
	assert.Contains(t, err.Error(), "orders_dlq.tf")
}

func TestGenerateMirrorTopicsFiles_ZeroTopicsProducesSharedFilesOnly(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := hclrequests.MirrorTopicsRequest{
		Topics:                    nil,
		ClusterLinkName:           "link",
		TargetClusterId:           "lkc-xyz",
		TargetClusterRestEndpoint: "https://cc.example.com:443",
		Mode:                      hclrequests.MigrateTopicsModeMirror,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	require.NoError(t, err)
	require.Len(t, project.Folders, 1)
	folder := project.Folders[0]

	assert.NotEmpty(t, folder.ProvidersTf)
	assert.NotEmpty(t, folder.VariablesTf)
	assert.Empty(t, folder.AdditionalFiles)
}

func TestGenerateMirrorTopicsFiles_FallsBackToSelectedTopicsWhenTopicsEmpty(t *testing.T) {
	t.Parallel()

	// Simulates the UI handler path: only SelectedTopics is populated.
	service := NewMigrationScriptsHCLService()
	request := hclrequests.MirrorTopicsRequest{
		SelectedTopics:            []string{"orders", "events"},
		ClusterLinkName:           "link",
		TargetClusterId:           "lkc-xyz",
		TargetClusterRestEndpoint: "https://cc.example.com:443",
		Mode:                      hclrequests.MigrateTopicsModeMirror,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	require.NoError(t, err)
	folder := project.Folders[0]
	assert.Len(t, folder.AdditionalFiles, 2)
	assert.Contains(t, folder.AdditionalFiles, "orders.tf")
	assert.Contains(t, folder.AdditionalFiles, "events.tf")
}

func TestGenerateMirrorTopicsFiles_InvalidModeIsRejected(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := hclrequests.MirrorTopicsRequest{
		Topics: []types.TopicDetails{{Name: "orders"}},
		Mode:   "bogus",
	}

	_, err := service.GenerateMirrorTopicsFiles(request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mode")
}

// ============================================================================
// New-mode end-to-end coverage (U4).
// ============================================================================

func mustStrPtr(s string) *string { return &s }

func TestGenerateMirrorTopicsFiles_NewMode_BasicShape(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := hclrequests.MirrorTopicsRequest{
		Topics: []types.TopicDetails{
			{
				Name:       "orders",
				Partitions: 6,
				Configurations: map[string]*string{
					"cleanup.policy":                 mustStrPtr("compact"),
					"retention.ms":                   mustStrPtr("604800000"),
					"unclean.leader.election.enable": mustStrPtr("true"),
					"replication.factor":             mustStrPtr("3"),
				},
			},
		},
		TargetClusterId:           "lkc-xyz",
		TargetClusterRestEndpoint: "https://cc.example.com:443",
		Mode:                      hclrequests.MigrateTopicsModeNew,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	require.NoError(t, err)
	require.Len(t, project.Folders, 1)
	folder := project.Folders[0]
	require.Len(t, folder.AdditionalFiles, 1)

	content, ok := folder.AdditionalFiles["orders.tf"]
	require.True(t, ok, "expected per-topic file orders.tf")

	// Header comment is present at the top of the file.
	assert.True(t, strings.HasPrefix(content, "# Generated by kcp create-asset migrate-topics --mode new"), "header comment missing from new-mode file")
	assert.Contains(t, content, CCSupportedTopicConfigsDocsURL)

	// Allow-listed configs preserved with source values.
	assert.Contains(t, content, `"cleanup.policy"`)
	assert.Contains(t, content, `"compact"`)
	assert.Contains(t, content, `"retention.ms"`)
	assert.Contains(t, content, `"604800000"`)

	// Non-allow-listed configs and replication.factor never emitted.
	assert.NotContains(t, content, "unclean.leader.election.enable")
	assert.NotContains(t, content, "replication.factor")

	// partitions_count preserved verbatim.
	assert.Contains(t, content, "partitions_count")
	assert.Contains(t, content, "6")

	// Resource type is the plain topic, not the mirror topic.
	assert.Contains(t, content, `resource "confluent_kafka_topic"`)
	assert.NotContains(t, content, `resource "confluent_kafka_mirror_topic"`)
}

func TestGenerateMirrorTopicsFiles_NewMode_FilePerTopic(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := hclrequests.MirrorTopicsRequest{
		Topics: []types.TopicDetails{
			{Name: "orders", Partitions: 6},
			{Name: "events", Partitions: 3},
			{Name: "users", Partitions: 1},
		},
		TargetClusterId:           "lkc-xyz",
		TargetClusterRestEndpoint: "https://cc.example.com:443",
		Mode:                      hclrequests.MigrateTopicsModeNew,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	require.NoError(t, err)
	folder := project.Folders[0]

	assert.Len(t, folder.AdditionalFiles, 3)
	for _, name := range []string{"orders.tf", "events.tf", "users.tf"} {
		assert.Contains(t, folder.AdditionalFiles, name)
	}

	// Each new-mode file has the header comment.
	for filename, content := range folder.AdditionalFiles {
		assert.True(t, strings.HasPrefix(content, "# Generated by kcp create-asset migrate-topics --mode new"), "%s missing new-mode header", filename)
	}
}

func TestGenerateMirrorTopicsFiles_NewMode_NoReplicationFactorEver(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := hclrequests.MirrorTopicsRequest{
		Topics: []types.TopicDetails{
			{
				Name:       "orders",
				Partitions: 6,
				Configurations: map[string]*string{
					"replication.factor": mustStrPtr("3"),
				},
			},
		},
		TargetClusterId:           "lkc-xyz",
		TargetClusterRestEndpoint: "https://cc.example.com:443",
		Mode:                      hclrequests.MigrateTopicsModeNew,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	require.NoError(t, err)
	folder := project.Folders[0]
	for _, content := range folder.AdditionalFiles {
		assert.NotContains(t, content, "replication.factor")
	}
}

func TestGenerateMirrorTopicsFiles_NewMode_PreservesPartitionsCount(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := hclrequests.MirrorTopicsRequest{
		Topics: []types.TopicDetails{
			{Name: "orders", Partitions: 12},
		},
		TargetClusterId:           "lkc-xyz",
		TargetClusterRestEndpoint: "https://cc.example.com:443",
		Mode:                      hclrequests.MigrateTopicsModeNew,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	require.NoError(t, err)
	content := project.Folders[0].AdditionalFiles["orders.tf"]
	// hclwrite aligns `=` signs; just check both the key and the value appear in the file.
	assert.Contains(t, content, "partitions_count")
	assert.Contains(t, content, " = 12")
}

func TestGenerateMirrorTopicsFiles_MirrorModeUnchangedByNewModeWiring(t *testing.T) {
	t.Parallel()

	// Regression check: U4's branching does not alter mirror-mode output.
	service := NewMigrationScriptsHCLService()
	request := hclrequests.MirrorTopicsRequest{
		Topics: []types.TopicDetails{
			{Name: "orders", Partitions: 6, Configurations: map[string]*string{
				"cleanup.policy": mustStrPtr("compact"),
			}},
		},
		ClusterLinkName:           "link",
		TargetClusterId:           "lkc-xyz",
		TargetClusterRestEndpoint: "https://cc.example.com:443",
		Mode:                      hclrequests.MigrateTopicsModeMirror,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	require.NoError(t, err)
	content := project.Folders[0].AdditionalFiles["orders.tf"]

	// Mirror mode emits the mirror-topic resource, never partitions/configs.
	assert.Contains(t, content, `resource "confluent_kafka_mirror_topic"`)
	assert.NotContains(t, content, `resource "confluent_kafka_topic"`)
	assert.NotContains(t, content, "partitions_count")
	assert.NotContains(t, content, "cleanup.policy")

	// And no new-mode header on mirror output.
	assert.False(t, strings.HasPrefix(content, "#"), "mirror-mode output should not carry the new-mode header")
}

func TestGenerateMigrateAclsFiles_OperationMapping(t *testing.T) {
	t.Parallel()

	request := hclrequests.MigrateAclsRequest{
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
	t.Parallel()

	request := hclrequests.MigrateAclsRequest{
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
	t.Parallel()

	request := hclrequests.MigrateAclsRequest{
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
	t.Parallel()

	request := hclrequests.MigrateAclsRequest{
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
	t.Parallel()

	request := hclrequests.MigrateAclsRequest{
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
	t.Parallel()

	request := hclrequests.MigrateAclsRequest{
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
	t.Parallel()

	request := hclrequests.MigrateAclsRequest{
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

func TestGenerateMigrateAclsFiles_IAMSourcedOperations(t *testing.T) {
	t.Parallel()

	// Use operation values exactly as they appear in types.AclMap
	request := hclrequests.MigrateAclsRequest{
		SelectedPrincipals:        []string{"iam_user"},
		TargetClusterId:           "lkc-abc123",
		TargetClusterRestEndpoint: "https://test.confluent.cloud:443",
		AclsByPrincipal: map[string][]types.Acls{
			"iam_user": {
				{
					ResourceType:        "Cluster",
					ResourceName:        "kafka-cluster",
					ResourcePatternType: "LITERAL",
					Principal:           "iam_user",
					Host:                "*",
					Operation:           "AlterConfigs",
					PermissionType:      "ALLOW",
				},
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "iam_user",
					Host:                "*",
					Operation:           "DescribeConfigs",
					PermissionType:      "ALLOW",
				},
				{
					ResourceType:        "Cluster",
					ResourceName:        "kafka-cluster",
					ResourcePatternType: "LITERAL",
					Principal:           "iam_user",
					Host:                "*",
					Operation:           "IdempotentWrite",
					PermissionType:      "ALLOW",
				},
				{
					ResourceType:        "TransactionalId",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "iam_user",
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

	content := files.PerPrincipalTf["iam_user.tf"]

	// Verify all IAM-sourced operations are correctly converted
	assert.Contains(t, content, `"ALTER_CONFIGS"`)
	assert.Contains(t, content, `"DESCRIBE_CONFIGS"`)
	assert.Contains(t, content, `"IDEMPOTENT_WRITE"`)
	assert.Contains(t, content, `"DESCRIBE"`)

	// Verify no unconverted camelCase operations leaked through
	assert.NotContains(t, content, `"AlterConfigs"`)
	assert.NotContains(t, content, `"DescribeConfigs"`)
	assert.NotContains(t, content, `"IdempotentWrite"`)

	// Verify TransactionalId resource type is supported and included
	assert.Contains(t, content, `"TRANSACTIONAL_ID"`)
}

// Edge case test: Principal names with special characters
func TestGenerateMigrateAclsFiles_PrincipalWithSpecialCharacters(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := hclrequests.MigrateAclsRequest{
		SelectedPrincipals:        []string{"user@example.com", "service.account-123"},
		TargetClusterId:           "lkc-abc123",
		TargetClusterRestEndpoint: "https://test.confluent.cloud:443",
		AclsByPrincipal: map[string][]types.Acls{
			"user@example.com": {
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:user@example.com",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
			},
			"service.account-123": {
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:service.account-123",
					Host:                "*",
					Operation:           "Write",
					PermissionType:      "ALLOW",
				},
			},
		},
	}

	files, err := service.GenerateMigrateAclsFiles(request)
	require.NoError(t, err)

	// Files should be created (@ and . might be sanitized in filenames)
	assert.NotEmpty(t, files.PerPrincipalTf)

	// Validate generated Terraform
	fileMap := terraformFilesToMap(files)
	validateTerraformProject(t, fileMap)
}
