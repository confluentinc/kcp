package aws

import (
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateSecurityGroupIngressRule(tfResourceName string, port int, sourceSecurityGroupRef string, securityGroupRef string) *hclwrite.Block {
	ruleBlock := hclwrite.NewBlock("resource", []string{"aws_security_group_rule", tfResourceName})
	ruleBlock.Body().SetAttributeValue("type", cty.StringVal("ingress"))
	ruleBlock.Body().SetAttributeValue("from_port", cty.NumberIntVal(int64(port)))
	ruleBlock.Body().SetAttributeValue("to_port", cty.NumberIntVal(int64(port)))
	ruleBlock.Body().SetAttributeValue("protocol", cty.StringVal("tcp"))
	ruleBlock.Body().SetAttributeRaw("source_security_group_id", utils.TokensForResourceReference(sourceSecurityGroupRef))
	ruleBlock.Body().SetAttributeRaw("security_group_id", utils.TokensForResourceReference(securityGroupRef))
	return ruleBlock
}

func GenerateSecurityGroup(tfResourceName string, ingressPorts []int, egressPorts []int, vpcIdVarName string) *hclwrite.Block {
	securityGroupBlock := hclwrite.NewBlock("resource", []string{"aws_security_group", tfResourceName})
	securityGroupBlock.Body().SetAttributeRaw("vpc_id", utils.TokensForVarReference(vpcIdVarName))
	securityGroupBlock.Body().AppendNewline()

	for i, ingressPort := range ingressPorts {
		ingressBlock := hclwrite.NewBlock("ingress", nil)
		ingressBlock.Body().SetAttributeValue("from_port", cty.NumberIntVal(int64(ingressPort)))
		ingressBlock.Body().SetAttributeValue("to_port", cty.NumberIntVal(int64(ingressPort)))
		ingressBlock.Body().SetAttributeValue("protocol", cty.StringVal("tcp"))
		ingressBlock.Body().SetAttributeValue("cidr_blocks", cty.ListVal([]cty.Value{cty.StringVal("0.0.0.0/0")}))

		securityGroupBlock.Body().AppendBlock(ingressBlock)
		if i > 0 {
			securityGroupBlock.Body().AppendNewline()
		}
	}

	for i, egressPort := range egressPorts {
		egressBlock := hclwrite.NewBlock("egress", nil)
		egressBlock.Body().SetAttributeValue("from_port", cty.NumberIntVal(int64(egressPort)))
		egressBlock.Body().SetAttributeValue("to_port", cty.NumberIntVal(int64(egressPort)))
		egressBlock.Body().SetAttributeValue("protocol", cty.StringVal("-1"))
		egressBlock.Body().SetAttributeValue("cidr_blocks", cty.ListVal([]cty.Value{cty.StringVal("0.0.0.0/0")}))

		securityGroupBlock.Body().AppendBlock(egressBlock)
		if i > 0 {
			securityGroupBlock.Body().AppendNewline()
		}
	}

	return securityGroupBlock
}
