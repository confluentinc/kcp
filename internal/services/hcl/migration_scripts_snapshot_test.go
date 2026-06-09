//go:build terraform_validation

package hcl

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMigrationScripts_MirrorTopics(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := types.MirrorTopicsRequest{
		SelectedTopics:            []string{"orders", "events", "users"},
		ClusterLinkName:           "msk-to-cc-link",
		TargetClusterId:           "lkc-xyz789",
		TargetClusterRestEndpoint: "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		Mode:                      types.MigrateTopicsModeMirror,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	if err != nil {
		t.Fatal(err)
	}

	fileMap := migrateTopicsProjectToFiles(project)
	validateTerraformProject(t, fileMap)
}

func TestMigrationScripts_NewTopics(t *testing.T) {
	t.Parallel()

	compactPolicy := "compact"
	retention := "604800000"
	uncleanLeader := "true" // not allow-listed; must be filtered out
	replication := "3"      // not allow-listed; must be filtered out

	service := NewMigrationScriptsHCLService()
	request := types.MirrorTopicsRequest{
		Topics: []types.TopicDetails{
			{
				Name:       "orders",
				Partitions: 6,
				Configurations: map[string]*string{
					"cleanup.policy":                 &compactPolicy,
					"retention.ms":                   &retention,
					"unclean.leader.election.enable": &uncleanLeader,
					"replication.factor":             &replication,
				},
			},
			{Name: "events", Partitions: 3},
			{Name: "users", Partitions: 1},
		},
		TargetClusterId:           "lkc-xyz789",
		TargetClusterRestEndpoint: "https://pkc-abc123.us-east-1.aws.confluent.cloud:443",
		Mode:                      types.MigrateTopicsModeNew,
	}

	project, err := service.GenerateMirrorTopicsFiles(request)
	if err != nil {
		t.Fatal(err)
	}

	fileMap := migrateTopicsProjectToFiles(project)
	validateTerraformProject(t, fileMap)
}

func TestMigrationScripts_MigrateACLs(t *testing.T) {
	t.Parallel()

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
	validateTerraformProject(t, fileMap)
}

func TestMigrationScripts_MigrateConnectors(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()

	// Simulate the output of the connector migrators: providers.tf + variables.tf
	// from the HCL service, plus a sample connector resource block.
	files := map[string]string{
		"providers.tf": service.GenerateProvidersTf(),
		"variables.tf": service.GenerateMigrateConnectorsVariablesTf(),
		"my-connector-connector.tf": `resource "confluent_connector" "my-connector" {
  environment {
    id = "env-abc123"
  }
  kafka_cluster {
    id = "lkc-xyz789"
  }

  config_nonsensitive = {
    "name"            = "my-connector"
    "connector.class" = "DatagenSource"
    "topics"          = "test-topic"
  }
}
`,
	}

	validateTerraformProject(t, files)
}

func TestMigrationScripts_MigrateSchemas(t *testing.T) {
	t.Parallel()

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
	validateTerraformProject(t, fileMap)
}

func TestMigrationScripts_MigrateGlueSchemas(t *testing.T) {
	t.Parallel()

	service := NewMigrationScriptsHCLService()
	request := types.MigrateGlueSchemasRequest{
		ConfluentCloudSchemaRegistryURL: "https://psrc-abc123.us-east-1.aws.confluent.cloud",
		GlueRegistries: []types.GlueSchemaRegistryMigrationConfig{
			{
				Migrate:      true,
				RegistryName: "my-glue-registry",
				Region:       "us-east-1",
				Schemas: []types.GlueSchema{
					{
						SchemaName: "orders-schema",
						SchemaArn:  "arn:aws:glue:us-east-1:123456789012:schema/my-glue-registry/orders-schema",
						DataFormat: "AVRO",
						Latest: &types.GlueSchemaVersion{
							SchemaDefinition: `{"type":"record","name":"Order","fields":[{"name":"id","type":"string"}]}`,
							DataFormat:       "AVRO",
						},
					},
				},
			},
		},
	}

	project, err := service.GenerateMigrateGlueSchemasFiles(request)
	if err != nil {
		t.Fatal(err)
	}

	// schemaProjectToFiles doesn't include AdditionalFiles, which is where Glue
	// schema HCL is placed. Flatten the folder directly to capture all files.
	require.Len(t, project.Folders, 1)
	folder := project.Folders[0]
	fileMap := map[string]string{}
	if folder.ProvidersTf != "" {
		fileMap["providers.tf"] = folder.ProvidersTf
	}
	if folder.VariablesTf != "" {
		fileMap["variables.tf"] = folder.VariablesTf
	}
	if folder.InputsAutoTfvars != "" {
		fileMap["inputs.auto.tfvars"] = folder.InputsAutoTfvars
	}
	for name, content := range folder.AdditionalFiles {
		fileMap[name] = content
	}
	require.NotEmpty(t, fileMap, "Glue schema output should produce Terraform files")

	validateTerraformProject(t, fileMap)
}
