package aws

import (
	_ "embed"
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// OptionalBlocksConfig represents optional configuration blocks for EC2 instances.
// The map key is the block name (e.g., "root_block_device", "metadata_options"),
// and the value is a map of attribute names to their values.
// Values can be either cty.Value (for literals) or hclwrite.Tokens (for references).
type OptionalBlocksConfig map[string]map[string]any

func GenerateAmiDataResource(tfResourceName, owners string, mostRecent bool, filters map[string]string) *hclwrite.Block {
	resourceBlock := hclwrite.NewBlock("data", []string{"aws_ami", tfResourceName})
	body := resourceBlock.Body()

	body.SetAttributeRaw("owners", utils.TokensForStringList([]string{owners}))
	body.SetAttributeValue("most_recent", cty.BoolVal(mostRecent))

	for filterName, filterValue := range filters {
		filterBlock := body.AppendNewBlock("filter", nil)
		filterBody := filterBlock.Body()
		filterBody.SetAttributeValue("name", cty.StringVal(filterName))
		filterBody.SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal(filterValue)}))
	}

	return resourceBlock
}

func GenerateEc2InstanceResource(tfResourceName, amiIdRef, instanceType, subnetIdRef, securityGroupIdsRef, keyNameRef string, publicIp bool, optionalBlocks OptionalBlocksConfig) *hclwrite.Block {
	resourceBlock := hclwrite.NewBlock("resource", []string{"aws_instance", tfResourceName})
	instanceBody := resourceBlock.Body()

	instanceBody.SetAttributeRaw("ami", utils.TokensForResourceReference(amiIdRef))
	instanceBody.SetAttributeValue("instance_type", cty.StringVal(instanceType)) // Assumes the value is passed as a string rather than a variable reference.
	instanceBody.SetAttributeRaw("subnet_id", utils.TokensForResourceReference(subnetIdRef))
	instanceBody.SetAttributeRaw("vpc_security_group_ids", utils.TokensForStringList([]string{securityGroupIdsRef}))
	instanceBody.SetAttributeRaw("key_name", utils.TokensForResourceReference(keyNameRef))
	instanceBody.SetAttributeValue("associate_public_ip_address", cty.BoolVal(publicIp))

	appendOptionalBlocks(instanceBody, optionalBlocks)

	return resourceBlock
}

func GenerateEc2UserDataInstanceResource(tfResourceName, amiIdRef, instanceType, subnetIdRef, securityGroupIdsRef, keyNameRef, userDataTemplatePath string, publicIp bool, userDataArgs map[string]hclwrite.Tokens, optionalBlocks OptionalBlocksConfig) *hclwrite.Block {
	resourceBlock := hclwrite.NewBlock("resource", []string{"aws_instance", tfResourceName})
	instanceBody := resourceBlock.Body()

	instanceBody.SetAttributeRaw("ami", utils.TokensForResourceReference(amiIdRef))
	instanceBody.SetAttributeValue("instance_type", cty.StringVal(instanceType)) // Assumes the value is passed as a string rather than a variable reference.
	instanceBody.SetAttributeRaw("subnet_id", utils.TokensForVarReference(subnetIdRef))
	instanceBody.SetAttributeRaw("vpc_security_group_ids", utils.TokensForVarReferenceList([]string{securityGroupIdsRef}))
	instanceBody.SetAttributeRaw("key_name", utils.TokensForVarReference(keyNameRef))
	instanceBody.SetAttributeValue("associate_public_ip_address", cty.BoolVal(publicIp))
	instanceBody.AppendNewline()

	templatefileTokens := utils.TokensForFunctionCall(
		"templatefile",
		utils.TokensForStringTemplate(fmt.Sprintf("${path.module}/%s", userDataTemplatePath)),
		utils.TokensForMap(userDataArgs),
	)

	instanceBody.SetAttributeRaw("user_data", templatefileTokens)

	appendOptionalBlocks(instanceBody, optionalBlocks)

	return resourceBlock
}

func GenerateEc2UserDataInstanceResourceWithForEach(tfResourceName, amiIdRef, instanceType, subnetIdRef, securityGroupIdsRef, keyNameRef, controllerBrokerUserDataTemplatePath, brokerUserDataTemplatePath, iamInstanceProfileName string, publicIp bool, userDataArgs map[string]hclwrite.Tokens, optionalBlocks OptionalBlocksConfig) *hclwrite.Block {
	resourceBlock := hclwrite.NewBlock("resource", []string{"aws_instance", tfResourceName})
	instanceBody := resourceBlock.Body()

	forEachExpr := fmt.Sprintf(`{ for idx, subnet_id in var.%s : "broker-${idx}" => subnet_id }`, subnetIdRef)
	instanceBody.SetAttributeRaw("for_each", hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(forEachExpr)},
	})

	instanceBody.SetAttributeRaw("ami", utils.TokensForResourceReference(amiIdRef))
	instanceBody.SetAttributeRaw("instance_type", utils.TokensForVarReference(instanceType))
	instanceBody.SetAttributeRaw("subnet_id", utils.TokensForResourceReference("each.value"))
	instanceBody.SetAttributeRaw("vpc_security_group_ids", utils.TokensForVarReferenceList([]string{securityGroupIdsRef}))
	instanceBody.SetAttributeRaw("key_name", utils.TokensForVarReference(keyNameRef))
	instanceBody.SetAttributeValue("associate_public_ip_address", cty.BoolVal(publicIp))

	if iamInstanceProfileName != "" {
		instanceBody.SetAttributeRaw("iam_instance_profile", utils.TokensForVarReference(iamInstanceProfileName))
	}
	instanceBody.AppendNewline()

	controllerBrokerTemplatefileTokens := utils.TokensForFunctionCall(
		"templatefile",
		utils.TokensForStringTemplate(fmt.Sprintf("${path.module}/%s", controllerBrokerUserDataTemplatePath)),
		utils.TokensForMap(userDataArgs),
	)

	brokerTemplatefileTokens := utils.TokensForFunctionCall(
		"templatefile",
		utils.TokensForStringTemplate(fmt.Sprintf("${path.module}/%s", brokerUserDataTemplatePath)),
		utils.TokensForResourceReference("{}"),
	)

	conditionTokens := hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("each.key")},
		&hclwrite.Token{Type: hclsyntax.TokenEqualOp, Bytes: []byte("==")},
		&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("broker-0")},
		&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
	}

	conditionalTokens := utils.TokensForConditional(
		conditionTokens,
		controllerBrokerTemplatefileTokens,
		brokerTemplatefileTokens,
	)

	instanceBody.SetAttributeRaw("user_data", conditionalTokens)
	instanceBody.AppendNewline()

	appendOptionalBlocks(instanceBody, optionalBlocks)

	return resourceBlock
}

func appendOptionalBlocks(instanceBody *hclwrite.Body, optionalBlocks OptionalBlocksConfig) {
	if optionalBlocks == nil {
		return
	}

	for blockName, attributes := range optionalBlocks {
		if len(attributes) == 0 {
			continue
		}

		block := instanceBody.AppendNewBlock(blockName, nil)
		blockBody := block.Body()

		for attrName, attrValue := range attributes {
			switch v := attrValue.(type) {
			case cty.Value:
				blockBody.SetAttributeValue(attrName, v)
			case hclwrite.Tokens:
				blockBody.SetAttributeRaw(attrName, v)
			default:
				// If it's neither, try to convert common types to cty.Value
				// This handles cases where someone passes int, string, bool directly
				if ctyVal := utils.ConvertToCtyValue(attrValue); ctyVal != cty.NilVal {
					blockBody.SetAttributeValue(attrName, ctyVal)
				}
			}
		}
	}
}

//go:embed ec2_user_data_templates/jump_cluster_setup_host_user_data.tpl
var jumpClusterSetupHostUserDataTpl string

//go:embed ec2_user_data_templates/jump_cluster_user_data.tpl
var jumpClusterUserDataTpl string

//go:embed ec2_user_data_templates/jump_cluster_with_sasl_scram_cluster_links_user_data.tpl
var jumpClusterWithSaslScramClusterLinksUserDataTpl string

//go:embed ec2_user_data_templates/jump_cluster_with_iam_cluster_links_user_data.tpl
var jumpClusterWithIamClusterLinksUserDataTpl string

func GenerateJumpClusterSetupHostUserDataTpl() string {
	return jumpClusterSetupHostUserDataTpl
}

func GenerateJumpClusterUserDataTpl() string {
	return jumpClusterUserDataTpl
}

func GenerateJumpClusterWithSaslScramClusterLinksUserDataTpl() string {
	return jumpClusterWithSaslScramClusterLinksUserDataTpl
}

func GenerateJumpClusterWithIamClusterLinksUserDataTpl() string {
	return jumpClusterWithIamClusterLinksUserDataTpl
}
