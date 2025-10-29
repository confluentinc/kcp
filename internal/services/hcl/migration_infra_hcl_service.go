package hcl

import (
	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type MigrationInfraHCLService struct {
}

func NewMigrationInfraHCLService() *MigrationInfraHCLService {
	return &MigrationInfraHCLService{}
}

func (mi *MigrationInfraHCLService) GenerateTerraformFiles(request types.MigrationWizardRequest) (types.TerraformFiles, error) {
	terraformFiles := types.TerraformFiles{
		MainTf:      mi.generateMainTf(request),
		ProvidersTf: mi.generateProvidersTf(),
		VariablesTf: mi.generateVariablesTf(),
	}

	return terraformFiles, nil
}

func (mi *MigrationInfraHCLService) generateMainTf(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(confluent.GenerateClusterLinkLocals())
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateClusterLinkResource(request))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateProvidersTf() string {
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

func (mi *MigrationInfraHCLService) generateVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	variables := []struct {
		name        string
		description string
		sensitive   bool
	}{
		{"confluent_cloud_api_key", "Confluent Cloud API Key", false},
		{"confluent_cloud_api_secret", "Confluent Cloud API Secret", true},
		{"msk_sasl_scram_username", "MSK SASL SCRAM Username", false},
		{"msk_sasl_scram_password", "MSK SASL SCRAM Password", true},
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
