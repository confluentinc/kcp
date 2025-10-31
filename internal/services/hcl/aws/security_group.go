package aws

import (
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func GenerateSecurityGroup(tfResourceName, vpcId string, ingressPorts []int, egressPorts []int) *hclwrite.Block {
	securityGroupBlock := hclwrite.NewBlock("resource", []string{"aws_security_group", tfResourceName})
	securityGroupBlock.Body().SetAttributeValue("vpc_id", cty.StringVal(vpcId))
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
