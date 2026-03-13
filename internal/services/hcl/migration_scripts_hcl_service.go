package hcl

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// Confluent Cloud supported ACL resource types
// https://docs.confluent.io/cloud/current/security/access-control/acls/overview.html#resources
var supportedACLResourceTypes = map[string]bool{
	"topic":           true,
	"group":           true,
	"cluster":         true,
	"transactionalid": true,
}

type MigrationScriptsHCLService struct {
}

func NewMigrationScriptsHCLService() *MigrationScriptsHCLService {
	return &MigrationScriptsHCLService{}
}

func (s *MigrationScriptsHCLService) GenerateMirrorTopicsFiles(request types.MirrorTopicsRequest) (types.TerraformFiles, error) {
	return types.TerraformFiles{
		MainTf:      s.generateMirrorTopicsTf(request),
		ProvidersTf: s.generateProvidersTf(),
		VariablesTf: s.generateMirrorTopicsVariablesTf(),
	}, nil
}

func (s *MigrationScriptsHCLService) GenerateMigrateAclsFiles(request types.MigrateAclsRequest) (types.TerraformFiles, error) {
	return types.TerraformFiles{
		PerPrincipalTf:   s.generatePerPrincipalACLsTf(request),
		ProvidersTf:      s.generateProvidersTf(),
		VariablesTf:      s.generateMigrateACLsVariablesTf(),
		InputsAutoTfvars: s.generateMigrateACLsInputsAutoTfvars(request),
	}, nil
}

func (s *MigrationScriptsHCLService) GenerateMigrateSchemasFiles(request types.MigrateSchemasRequest) (types.MigrationScriptsTerraformProject, error) {
	ms := types.MigrationScriptsTerraformProject{}
	folders := []types.MigrationScriptsTerraformFolder{}
	for _, schemaRegistry := range request.SchemaRegistries {
		if schemaRegistry.Migrate {
			folderName := utils.URLToFolderName(schemaRegistry.SourceURL)
			folder := types.MigrationScriptsTerraformFolder{
				Name:             folderName,
				MainTf:           s.generateMigrateSchemasMainTf(schemaRegistry),
				ProvidersTf:      s.generateMigrateSchemasProvidersTf(),
				VariablesTf:      s.generateMigrateSchemasVariablesTf(),
				InputsAutoTfvars: s.generateMigrateSchemasInputsAutoTfvars(request.ConfluentCloudSchemaRegistryURL, schemaRegistry),
			}

			folders = append(folders, folder)
		}
	}

	ms.Folders = folders

	return ms, nil
}

func (s *MigrationScriptsHCLService) generateMigrateConnectorsFiles() (types.TerraformFiles, error) {
	return types.TerraformFiles{
		MainTf:      s.generateMigrateConnectorsMainTf(),
		ProvidersTf: s.generateProvidersTf(),
		VariablesTf: s.generateMigrateConnectorsVariablesTf(),
	}, nil
}

func (s *MigrationScriptsHCLService) generateProvidersTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(confluent.GenerateRequiredProviderTokens())
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateProviderBlock())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// ============================================================================
// Migrate Topics Generation Methods
// ============================================================================

func (s *MigrationScriptsHCLService) generateMirrorTopicsTf(request types.MirrorTopicsRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	for _, topic := range request.SelectedTopics {
		tfResourceName := utils.FormatHclResourceName(topic)
		rootBody.AppendBlock(confluent.GenerateMirrorTopic(tfResourceName, topic, request.ClusterLinkName, request.TargetClusterId, request.TargetClusterRestEndpoint))
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

func (s *MigrationScriptsHCLService) generateMirrorTopicsVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	variables := []struct {
		name        string
		description string
		sensitive   bool
	}{
		{confluent.VarConfluentCloudAPIKey, "Confluent Cloud API Key", false},
		{confluent.VarConfluentCloudAPISecret, "Confluent Cloud API Secret", true},
		{"confluent_cloud_cluster_api_key", "Confluent Cloud cluster API key", false},
		{"confluent_cloud_cluster_api_secret", "Confluent Cloud cluster API secret", true},
	}

	for _, v := range variables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.name})
		variableBody := variableBlock.Body()
		variableBody.SetAttributeRaw("type", utils.TokensForResourceReference("string"))
		if v.description != "" {
			variableBody.SetAttributeValue("description", cty.StringVal(v.description))
		}
		if v.sensitive {
			variableBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

// ============================================================================
// Migrate ACLs Generation Methods
// ============================================================================

func (s *MigrationScriptsHCLService) generatePerPrincipalACLsTf(request types.MigrateAclsRequest) map[string]string {
	result := make(map[string]string)

	for principal, acls := range request.AclsByPrincipal {
		f := hclwrite.NewEmptyFile()
		rootBody := f.Body()

		serviceAccountResourceName := utils.FormatHclResourceName(principal)
		rootBody.AppendUnstructuredTokens(utils.TokensForComment("// Migrated principal: " + principal))
		rootBody.AppendNewline()
		rootBody.AppendBlock(confluent.GenerateServiceAccount(serviceAccountResourceName, principal, "Service Account for "+principal, request.PreventDestroy))
		rootBody.AppendNewline()

		aclIndex := 0
		for _, acl := range acls {
			if !supportedACLResourceTypes[strings.ToLower(acl.ResourceType)] {
				slog.Warn("skipping unsupported Confluent Cloud ACL resource type", "resource_type", acl.ResourceType, "principal", principal)
				continue
			}

			operationSnake := utils.CamelToScreamingSnake(acl.Operation)
			resourceTypeSnake := utils.CamelToScreamingSnake(acl.ResourceType)
			patternTypeSnake := utils.CamelToScreamingSnake(acl.ResourcePatternType)
			permissionSnake := utils.CamelToScreamingSnake(acl.PermissionType)
			tfResourceName := utils.FormatHclResourceName(fmt.Sprintf("%s_%s_%s_%s_%d", principal, acl.PermissionType, acl.ResourceType, operationSnake, aclIndex))

			rootBody.AppendBlock(confluent.GenerateKafkaACL(
				tfResourceName,
				resourceTypeSnake,
				acl.ResourceName,
				patternTypeSnake,
				fmt.Sprintf("User:${confluent_service_account.%s.id}", serviceAccountResourceName),
				acl.Host,
				operationSnake,
				permissionSnake,
				"var.confluent_cloud_cluster_id",
				"var.confluent_cloud_cluster_rest_endpoint",
				"var.confluent_cloud_cluster_api_key",
				"var.confluent_cloud_cluster_api_secret",
				request.PreventDestroy,
			))
			rootBody.AppendNewline()
			aclIndex++
		}

		fileName := fmt.Sprintf("%s.tf", serviceAccountResourceName)
		result[fileName] = string(f.Bytes())
	}

	return result
}

func (s *MigrationScriptsHCLService) generateMigrateACLsVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	variables := []struct {
		name        string
		description string
		sensitive   bool
	}{
		{confluent.VarConfluentCloudAPIKey, "Confluent Cloud API Key", false},
		{confluent.VarConfluentCloudAPISecret, "Confluent Cloud API Secret", true},
		{"confluent_cloud_cluster_id", "Confluent Cloud cluster ID", false},
		{"confluent_cloud_cluster_rest_endpoint", "Confluent Cloud cluster REST endpoint", false},
		{"confluent_cloud_cluster_api_key", "Confluent Cloud cluster API key", false},
		{"confluent_cloud_cluster_api_secret", "Confluent Cloud cluster API secret", true},
	}

	for _, v := range variables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.name})
		variableBody := variableBlock.Body()
		variableBody.SetAttributeRaw("type", utils.TokensForResourceReference("string"))
		if v.description != "" {
			variableBody.SetAttributeValue("description", cty.StringVal(v.description))
		}
		if v.sensitive {
			variableBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

func (s *MigrationScriptsHCLService) generateMigrateACLsInputsAutoTfvars(request types.MigrateAclsRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.SetAttributeValue("confluent_cloud_cluster_id", cty.StringVal(request.TargetClusterId))
	rootBody.SetAttributeValue("confluent_cloud_cluster_rest_endpoint", cty.StringVal(request.TargetClusterRestEndpoint))

	return string(f.Bytes())
}

// ============================================================================
// Migrate Connectors Generation Methods
// ============================================================================

func (s *MigrationScriptsHCLService) generateMigrateConnectorsMainTf() string {
	f := hclwrite.NewEmptyFile()
	// rootBody := f.Body()

	return string(f.Bytes())
}

func (s *MigrationScriptsHCLService) generateMigrateConnectorsVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	// rootBody := f.Body()

	return string(f.Bytes())
}

// ============================================================================
// Migrate Schemas Generation Methods
// ============================================================================

func (s *MigrationScriptsHCLService) generateMigrateSchemasMainTf(schemaRegistry types.SchemaRegistryExporterConfig) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(confluent.GenerateSchemaExporter(schemaRegistry))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (s *MigrationScriptsHCLService) generateMigrateSchemasProvidersTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(confluent.GenerateRequiredProviderTokens())
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateEmptyProviderBlock())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (s *MigrationScriptsHCLService) generateMigrateSchemasVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	for _, v := range confluent.SchemaExporterVariables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.Name})
		variableBody := variableBlock.Body()

		variableBody.SetAttributeRaw("type", utils.TokensForResourceReference(v.Type))
		if v.Description != "" {
			variableBody.SetAttributeValue("description", cty.StringVal(v.Description))
		}
		if v.Sensitive {
			variableBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

func (s *MigrationScriptsHCLService) generateMigrateSchemasInputsAutoTfvars(confluentCloudSchemaRegistryURL string, schemaRegistry types.SchemaRegistryExporterConfig) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// hard code :(
	rootBody.SetAttributeValue(confluent.VarSourceSchemaRegistryURL, cty.StringVal(schemaRegistry.SourceURL))
	rootBody.SetAttributeRaw(confluent.VarSubjects, utils.TokensForStringList(schemaRegistry.Subjects))
	rootBody.SetAttributeValue(confluent.VarConfluentCloudSchemaRegistryURL, cty.StringVal(confluentCloudSchemaRegistryURL))

	return string(f.Bytes())
}
