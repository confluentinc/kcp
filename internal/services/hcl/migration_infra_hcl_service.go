package hcl

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/hcl/aws"
	"github.com/confluentinc/kcp/internal/services/hcl/confluent"
	"github.com/confluentinc/kcp/internal/services/hcl/other"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type MigrationInfraHCLService struct {
}

func NewMigrationInfraHCLService() *MigrationInfraHCLService {
	return &MigrationInfraHCLService{}
}

func (mi *MigrationInfraHCLService) GenerateTerraformModules(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	if request.HasPublicCcEndpoints {
		return mi.handleClusterLink(request)
	}
	return mi.handlePrivateLink(request)
}

func (mi *MigrationInfraHCLService) handleClusterLink(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	return types.MigrationInfraTerraformProject{
		MainTf:      mi.generateRootMainTfForClusterLink(),
		ProvidersTf: mi.generateRootProvidersTfForClusterLink(),
		VariablesTf: mi.generateVariablesTf(confluent.ClusterLinkVariables),
		Modules: []types.MigrationInfraTerraformModule{
			{
				Name:        "cluster_link",
				MainTf:      mi.generateClusterLinkMainTf(request),
				VariablesTf: mi.generateClusterLinkVariablesTf(),
			},
		},
	}
}

// TODO: Is `handlePrivateLink` the best name? It had me slightly confused at first.
func (mi *MigrationInfraHCLService) handlePrivateLink(request types.MigrationWizardRequest) types.MigrationInfraTerraformProject {
	// Get all variables using the unified module definition pattern
	requiredVariables := GetModuleVariableDefinitions(request)

	return types.MigrationInfraTerraformProject{
		MainTf:           mi.generateRootMainTfForPrivateLink(),
		ProvidersTf:      mi.generateRootProvidersTfForPrivateLink(),
		VariablesTf:      mi.generateVariablesTf(requiredVariables),
		InputsAutoTfvars: mi.generateInputsAutoTfvars(request),
		Modules: []types.MigrationInfraTerraformModule{
			{
				Name:        "jump_cluster_setup_host",
				MainTf:      mi.generateJumpClusterSetupHostMainTf(),
				VariablesTf: mi.generateJumpClusterSetupHostVariablesTf(),
				VersionsTf:  mi.generateJumpClusterSetupHostVersionsTf(),
				AdditionalFiles: map[string]string{
					"jump-cluster-setup-host-user-data.tpl": mi.generateJumpClusterSetupHostUserDataTpl(),
				},
			},
			{
				Name:        "confluent_platform_broker_instances",
				MainTf:      mi.generateConfluentPlatformBrokerInstancesMainTf(),
				VariablesTf: mi.generateConfluentPlatformBrokerInstancesVariablesTf(),
				OutputsTf:   mi.generateConfluentPlatformBrokerInstancesOutputsTf(),
			},
			{
				Name:        "networking",
				MainTf:      mi.generateNetworkingMainTf(request),
				VariablesTf: mi.generateNetworkingVariablesTf(request),
				OutputsTf:   mi.generateNetworkingOutputsTf(request),
			},
			{
				Name:        "private_link_connection",
				MainTf:      mi.generatePrivateLinkConnectionMainTf(),
				VariablesTf: mi.generatePrivateLinkConnectionVariablesTf(),
				OutputsTf:   mi.generatePrivateLinkConnectionOutputsTf(),
			},
		},
	}
}

// ============================================================================
// Root-Level Generation - Cluster Link
// ============================================================================

func (mi *MigrationInfraHCLService) generateRootMainTfForClusterLink() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	moduleBlock := rootBody.AppendNewBlock("module", []string{"cluster_link"})
	moduleBody := moduleBlock.Body()

	moduleBody.SetAttributeValue("source", cty.StringVal("./cluster_link"))
	moduleBody.AppendNewline()

	// Pass all variables to the cluster_link module
	for _, v := range confluent.ClusterLinkVariables {
		moduleBody.SetAttributeRaw(v.Name, utils.TokensForVarReference(v.Name))
	}

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateRootProvidersTfForClusterLink() string {
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
// Root-Level Generation - Private Link
// ============================================================================

func (mi *MigrationInfraHCLService) generateRootMainTfForPrivateLink() string {
	// TODO: Implement main.tf generation for private link
	return ""
}

func (mi *MigrationInfraHCLService) generateRootProvidersTfForPrivateLink() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	// Add confluent provider
	requiredProvidersBody.SetAttributeRaw(confluent.GenerateRequiredProviderTokens())
	// Add aws provider
	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())
	rootBody.AppendNewline()

	// Add confluent provider block
	rootBody.AppendBlock(confluent.GenerateProviderBlock())
	rootBody.AppendNewline()

	// Add aws provider block with variable reference
	rootBody.AppendBlock(aws.GenerateProviderBlockWithVar())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// ============================================================================
// Cluster Link Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generateClusterLinkMainTf(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(confluent.GenerateClusterLinkLocals())
	rootBody.AppendNewline()

	rootBody.AppendBlock(confluent.GenerateClusterLinkResource(request))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateClusterLinkVariablesTf() string {
	return mi.generateVariablesTf(confluent.ClusterLinkVariables)
}

// ============================================================================
// Jump Cluster Setup Host Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostMainTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.AppendBlock(aws.GenerateAmazonLinuxAMI())
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateJumpClusterSetupHost())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostUserDataTpl() string {
	return aws.GenerateJumpClusterSetupHostUserDataTpl()
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostVariablesTf() string {
	return mi.generateVariablesTf(GetJumpClusterSetupHostVariableDefinitions())
}

func (mi *MigrationInfraHCLService) generateJumpClusterSetupHostVersionsTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())

	return string(f.Bytes())
}

// ============================================================================
// Confluent Platform Broker Instances Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generateConfluentPlatformBrokerInstancesMainTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generateConfluentPlatformBrokerInstancesVariablesTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generateConfluentPlatformBrokerInstancesOutputsTf() string {
	return ""
}

// ============================================================================
// Networking Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generateNetworkingMainTf(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Get variable names from module definitions
	vpcIdVarName := GetModuleVariableName("vpc_id")
	jumpClusterBrokerSubnetCidrsVarName := GetModuleVariableName("jump_cluster_broker_subnet_cidrs")
	jumpClusterSetupHostSubnetCidrVarName := GetModuleVariableName("jump_cluster_setup_host_subnet_cidr")

	if request.HasExistingInternetGateway {
		rootBody.AppendBlock(aws.GenerateInternetGatewayDataSource("internet_gateway", vpcIdVarName))
	} else {
		rootBody.AppendBlock(aws.GenerateInternetGatewayResource("internet_gateway", vpcIdVarName))
	}
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateAvailabilityZonesDataSource("this"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSecurityGroup("security_group", []int{22, 9091, 9092, 9093, 8090, 8081}, []int{0}, vpcIdVarName))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSubnetResourceWithForEach(
		"jump_cluster_broker_subnets",
		jumpClusterBrokerSubnetCidrsVarName,
		"data.aws_availability_zones.this",
		vpcIdVarName,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateSubnetResource(
		"jump_cluster_setup_host_subnet",
		jumpClusterSetupHostSubnetCidrVarName,
		"data.aws_availability_zones.available.names[0]",
		vpcIdVarName,
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateEIPResource("nat_eip"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateNATGatewayResource("nat_gw", "aws_eip.nat_eip.id", "aws_subnet.jump_cluster_setup_host_subnet.id"))
	rootBody.AppendNewline()

	if request.HasExistingInternetGateway {
		rootBody.AppendBlock(aws.GenerateRouteTableResource("jump_cluster_setup_host_public_rt", aws.GetInternetGatewayReference(request.HasExistingInternetGateway, "internet_gateway"), vpcIdVarName))
		rootBody.AppendNewline()
	} else {
		rootBody.AppendBlock(aws.GenerateRouteTableResource("jump_cluster_setup_host_public_rt", aws.GetInternetGatewayReference(request.HasExistingInternetGateway, "internet_gateway"), vpcIdVarName))
		rootBody.AppendNewline()
	}

	rootBody.AppendBlock(aws.GenerateRouteTableAssociationResource("jump_cluster_setup_host_public_rt_association", aws.GenerateSubnetResourceReference("jump_cluster_setup_host_subnet"), "aws_route_table.jump_cluster_setup_host_public_rt.id"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateRouteTableResource("private_subnet_rt", "aws_nat_gateway.nat_gw.id", vpcIdVarName))
	rootBody.AppendNewline()

	for i := range request.JumpClusterBrokerSubnetCidr {
		rootBody.AppendBlock(aws.GenerateRouteTableAssociationResource(fmt.Sprintf("jump_cluster_broker_route_table_assoc_%d", i), aws.GenerateSubnetResourceReference(fmt.Sprintf("jump_cluster_broker_subnet_%d", i)), "aws_route_table.private_subnet_rt.id"))
		if i < len(request.JumpClusterBrokerSubnetCidr) {
			rootBody.AppendNewline()
		}
	}

	rootBody.AppendBlock(aws.GenerateSecurityGroup("private_link_security_group", []int{80, 443, 9092}, []int{0}, vpcIdVarName))
	rootBody.AppendNewline()

	rootBody.AppendBlock(other.GenerateTLSPrivateKeyResource("jump_cluster_ssh_key", "RSA", 4096))
	rootBody.AppendNewline()

	rootBody.AppendBlock(other.GenerateLocalFileResource("jump_cluster_ssh_key_private_key", "tls_private_key.jump_cluster_ssh_key.private_key_pem", "./.ssh/jump_cluster_ssh_key_private_key_rsa", "400"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(other.GenerateLocalFileResource("jump_cluster_ssh_key_public_key", "tls_private_key.jump_cluster_ssh_key.public_key_openssh", "./.ssh/jump_cluster_ssh_key_public_key.pub", "400"))
	rootBody.AppendNewline()

	rootBody.AppendBlock(aws.GenerateKeyPairResource("jump_cluster_ssh_key", fmt.Sprintf("jump_cluster_ssh_key_%s", utils.RandomString(5)), "tls_private_key.jump_cluster_ssh_key.public_key_openssh"))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (mi *MigrationInfraHCLService) generateNetworkingVariablesTf(request types.MigrationWizardRequest) string {
	requiredVariables := GetModuleVariableDefinitions(request)
	return mi.generateVariablesTf(requiredVariables)
}

func (mi *MigrationInfraHCLService) generateNetworkingOutputsTf(request types.MigrationWizardRequest) string {
	outputs := GetNetworkingModuleOutputDefinitions()
	return mi.generateOutputsTf(outputs)
}

// generateOutputsTf generates outputs.tf content from output definitions
func (mi *MigrationInfraHCLService) generateOutputsTf(tfOutputs []TerraformOutput) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	for _, output := range tfOutputs {
		outputBlock := rootBody.AppendNewBlock("output", []string{output.Name})
		outputBody := outputBlock.Body()

		// Parse the value expression by wrapping it in a temporary output block
		// This allows us to parse complex expressions correctly
		tempHcl := fmt.Sprintf("output \"temp\" {\n  value = %s\n}", output.Value)
		tempFile, diags := hclwrite.ParseConfig([]byte(tempHcl), "", hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			// If parsing fails, fall back to using the raw expression as a resource reference
			outputBody.SetAttributeRaw("value", utils.TokensForResourceReference(output.Value))
		} else {
			// Extract the value attribute from the temporary file
			tempBody := tempFile.Body()
			blocks := tempBody.Blocks()
			if len(blocks) > 0 {
				tempOutputBody := blocks[0].Body()
				attrs := tempOutputBody.Attributes()
				if valueAttr, ok := attrs["value"]; ok {
					outputBody.SetAttributeRaw("value", valueAttr.Expr().BuildTokens(nil))
				} else {
					// Fallback to resource reference
					outputBody.SetAttributeRaw("value", utils.TokensForResourceReference(output.Value))
				}
			} else {
				// Fallback to resource reference
				outputBody.SetAttributeRaw("value", utils.TokensForResourceReference(output.Value))
			}
		}

		if output.Description != "" {
			outputBody.SetAttributeValue("description", cty.StringVal(output.Description))
		}
		if output.Sensitive {
			outputBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

// ============================================================================
// Inputs Auto Tfvars Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generateInputsAutoTfvars(request types.MigrationWizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	values := GetModuleVariableValues(request)

	for varName, value := range values {
		switch v := value.(type) {
		case string:
			if v != "" {
				rootBody.SetAttributeValue(varName, cty.StringVal(v))
			}
		case []string:
			if len(v) > 0 {
				ctyValues := make([]cty.Value, len(v))
				for i, s := range v {
					ctyValues[i] = cty.StringVal(s)
				}
				rootBody.SetAttributeValue(varName, cty.ListVal(ctyValues))
			}
		case bool:
			rootBody.SetAttributeValue(varName, cty.BoolVal(v))
		case int:
			rootBody.SetAttributeValue(varName, cty.NumberIntVal(int64(v)))
		}
	}

	return string(f.Bytes())
}

// ============================================================================
// Private Link Connection Module Generation
// ============================================================================

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionMainTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionVariablesTf() string {
	return ""
}

func (mi *MigrationInfraHCLService) generatePrivateLinkConnectionOutputsTf() string {
	return ""
}

// ============================================================================
// Shared/Utility Functions
// ============================================================================

func (mi *MigrationInfraHCLService) generateVariablesTf(tfVariables []types.TerraformVariable) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	for _, v := range tfVariables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.Name})
		variableBody := variableBlock.Body()
		// Use Type field if specified, otherwise default to "string"
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
