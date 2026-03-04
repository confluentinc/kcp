package aws

import (
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// GenerateCallerIdentityDataSource creates a data "aws_caller_identity" data source.
func GenerateCallerIdentityDataSource(tfResourceName string) *hclwrite.Block {
	return hclwrite.NewBlock("data", []string{"aws_caller_identity", tfResourceName})
}
