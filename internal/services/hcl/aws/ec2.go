package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateAmazonLinuxAMI() *hclwrite.Block {
	dataBlock := hclwrite.NewBlock("data", []string{"aws_ami", "amzn_linux_ami"})
	body := dataBlock.Body()

	body.SetAttributeValue("most_recent", cty.BoolVal(true))
	body.SetAttributeValue("owners", cty.ListVal([]cty.Value{cty.StringVal("137112412989")}))
	body.AppendNewline()

	// Filter for name
	nameFilterBlock := body.AppendNewBlock("filter", nil)
	nameFilterBody := nameFilterBlock.Body()
	nameFilterBody.SetAttributeValue("name", cty.StringVal("name"))
	nameFilterBody.SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("al2023-ami-2023.*-kernel-6.1-x86_64")}))
	body.AppendNewline()

	// Filter for state
	stateFilterBlock := body.AppendNewBlock("filter", nil)
	stateFilterBody := stateFilterBlock.Body()
	stateFilterBody.SetAttributeValue("name", cty.StringVal("state"))
	stateFilterBody.SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("available")}))
	body.AppendNewline()

	// Filter for architecture
	archFilterBlock := body.AppendNewBlock("filter", nil)
	archFilterBody := archFilterBlock.Body()
	archFilterBody.SetAttributeValue("name", cty.StringVal("architecture"))
	archFilterBody.SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("x86_64")}))
	body.AppendNewline()

	// Filter for virtualization-type
	virtFilterBlock := body.AppendNewBlock("filter", nil)
	virtFilterBody := virtFilterBlock.Body()
	virtFilterBody.SetAttributeValue("name", cty.StringVal("virtualization-type"))
	virtFilterBody.SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("hvm")}))

	return dataBlock
}

func GenerateAnsibleControlNodeInstance() *hclwrite.Block {
	resourceBlock := hclwrite.NewBlock("resource", []string{"aws_instance", "ansible_control_node_instance"})
	body := resourceBlock.Body()

	body.SetAttributeRaw("ami", utils.TokensForResourceReference("data.aws_ami.amzn_linux_ami.id"))
	body.SetAttributeValue("instance_type", cty.StringVal("t2.medium"))
	body.SetAttributeRaw("subnet_id", utils.TokensForVarReference("aws_public_subnet_id"))
	body.SetAttributeRaw("vpc_security_group_ids", utils.TokensForVarReference("security_group_id"))
	body.SetAttributeRaw("key_name", utils.TokensForVarReference("aws_key_pair_name"))
	body.SetAttributeValue("associate_public_ip_address", cty.BoolVal(true))
	body.AppendNewline()

	// Create templatefile function call for user_data
	templatefileMap := map[string]hclwrite.Tokens{
		"broker_ips":  utils.TokensForVarReference("confluent_platform_broker_instances_private_dns"),
		"private_key": utils.TokensForVarReference("private_key"),
	}

	// Build templatefile function call: templatefile("${path.module}/ansible-control-node-user-data.tpl", {...})
	templatefileTokens := hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("templatefile")},
		&hclwrite.Token{Type: hclsyntax.TokenOParen, Bytes: []byte("(")},
		&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("${path.module}/ansible-control-node-user-data.tpl")},
		&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
		&hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(", ")},
	}
	templatefileTokens = append(templatefileTokens, utils.TokensForMap(templatefileMap)...)
	templatefileTokens = append(templatefileTokens, &hclwrite.Token{Type: hclsyntax.TokenCParen, Bytes: []byte(")")})

	body.SetAttributeRaw("user_data", templatefileTokens)
	body.AppendNewline()

	// Tags
	tagsBlock := body.AppendNewBlock("tags", nil)
	tagsBody := tagsBlock.Body()
	tagsBody.SetAttributeValue("Name", cty.StringVal("ansible_control_node_instance"))

	return resourceBlock
}
