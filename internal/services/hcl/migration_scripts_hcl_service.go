package hcl

import (
	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

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

func (s *MigrationScriptsHCLService) GenerateMigrateAclsFiles() (types.TerraformFiles, error) {
	return types.TerraformFiles{
		MainTf:      s.generateMigrateACLsMainTf(),
		ProvidersTf: s.generateProvidersTf(),
		VariablesTf: s.generateMigrateACLsVariablesTf(),
	}, nil
}

func (s *MigrationScriptsHCLService) GenerateMigrateSchemasFiles(request types.MigrateSchemasRequest) (types.TerraformFiles, error) {
	return types.TerraformFiles{
		MainTf:      s.generateMigrateSchemasMainTf(),
		ProvidersTf: s.generateProvidersTf(),
		VariablesTf: s.generateMigrateSchemasVariablesTf(),
	}, nil
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
// Mirror Topics Generation Methods
// ============================================================================

func (s *MigrationScriptsHCLService) generateMirrorTopicsTf(request types.MirrorTopicsRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	for _, topic := range request.SelectedTopics {
		rootBody.AppendBlock(confluent.GenerateMirrorTopic(topic, request.ClusterLinkName, request.ConfluentCloudClusterId, request.ConfluentCloudClusterRestEndpoint))
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
		{"confluent_cloud_api_key", "Confluent Cloud API Key", false},
		{"confluent_cloud_api_secret", "Confluent Cloud API Secret", true},
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

func (s *MigrationScriptsHCLService) generateMigrateACLsMainTf() string {
	f := hclwrite.NewEmptyFile()
	// rootBody := f.Body()

	return string(f.Bytes())
}

func (s *MigrationScriptsHCLService) generateMigrateACLsVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	// rootBody := f.Body()

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

func (s *MigrationScriptsHCLService) generateMigrateSchemasMainTf() string {
	f := hclwrite.NewEmptyFile()
	// rootBody := f.Body()

	return string(f.Bytes())
}

func (s *MigrationScriptsHCLService) generateMigrateSchemasVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	// rootBody := f.Body()

	return string(f.Bytes())
}
