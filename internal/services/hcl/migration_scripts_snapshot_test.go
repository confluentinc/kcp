package hcl

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

func TestMigrationScripts_MirrorTopics(t *testing.T) {
	service := NewMigrationScriptsHCLService()
	request := types.MirrorTopicsRequest{
		SelectedTopics:            []string{"orders", "events", "users"},
		ClusterLinkName:           "msk-to-cc-link",
		TargetClusterId:           "lkc-xyz789",
		TargetClusterRestEndpoint: "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
	}

	files, err := service.GenerateMirrorTopicsFiles(request)
	if err != nil {
		t.Fatal(err)
	}

	fileMap := terraformFilesToMap(files)
	assertMatchesGoldenFiles(t, "TestMigrationScripts_MirrorTopics", fileMap)
}

func TestMigrationScripts_MigrateACLs(t *testing.T) {
	service := NewMigrationScriptsHCLService()
	request := types.MigrateAclsRequest{
		SelectedPrincipals:        []string{"app_user"},
		TargetClusterId:           "lkc-xyz789",
		TargetClusterRestEndpoint: "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		AclsByPrincipal: map[string][]types.Acls{
			"app_user": {
				{
					ResourceType:        "Topic",
					ResourceName:        "*",
					ResourcePatternType: "LITERAL",
					Principal:           "User:app_user",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
				{
					ResourceType:        "Group",
					ResourceName:        "my-group",
					ResourcePatternType: "PREFIXED",
					Principal:           "User:app_user",
					Host:                "*",
					Operation:           "Read",
					PermissionType:      "ALLOW",
				},
			},
		},
	}

	files, err := service.GenerateMigrateAclsFiles(request)
	if err != nil {
		t.Fatal(err)
	}

	fileMap := terraformFilesToMap(files)
	assertMatchesGoldenFiles(t, "TestMigrationScripts_MigrateACLs", fileMap)
}

func TestMigrationScripts_MigrateSchemas(t *testing.T) {
	service := &MigrationScriptsHCLService{SchemaRegistryClusterID: "testcluster"}
	request := types.MigrateSchemasRequest{
		ConfluentCloudSchemaRegistryURL: "https://psrc-abc123.us-east-1.aws.confluent.cloud",
		SchemaRegistries: []types.SchemaRegistryExporterConfig{
			{
				Migrate:   true,
				Subjects:  []string{"orders-value", "events-value"},
				SourceURL: "https://schema-registry.internal.example.com:8081",
			},
		},
	}

	project, err := service.GenerateMigrateSchemasFiles(request)
	if err != nil {
		t.Fatal(err)
	}

	fileMap := schemaProjectToFiles(project)
	assertMatchesGoldenFiles(t, "TestMigrationScripts_MigrateSchemas", fileMap)
}
