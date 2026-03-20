package hcl

import (
	"sort"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// GenerateVariablesTf generates a variables.tf file from a list of variable definitions.
// Duplicate variable names are deduplicated (first occurrence wins).
func GenerateVariablesTf(tfVariables []types.TerraformVariable) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	varSeenVariables := make(map[string]bool)

	for _, v := range tfVariables {
		if varSeenVariables[v.Name] {
			continue
		}
		varSeenVariables[v.Name] = true
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

// GenerateOutputsTf generates an outputs.tf file from a list of output definitions.
func GenerateOutputsTf(tfOutputs []types.TerraformOutput) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	for _, output := range tfOutputs {
		outputBlock := rootBody.AppendNewBlock("output", []string{output.Name})
		outputBody := outputBlock.Body()
		outputBody.SetAttributeRaw("value", utils.TokensForResourceReference(output.Value))

		if output.Description != "" {
			outputBody.SetAttributeValue("description", cty.StringVal(output.Description))
		}
		outputBody.SetAttributeValue("sensitive", cty.BoolVal(output.Sensitive))
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

// GenerateVersionsTf generates a versions.tf file with the given required providers.
// Each provider function receives the required_providers block body to add its provider.
func GenerateVersionsTf(providers ...func(*hclwrite.Body)) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	for _, addProvider := range providers {
		addProvider(requiredProvidersBody)
	}

	return string(f.Bytes())
}

// GenerateInputsAutoTfvarsWithBrokers generates an inputs.auto.tfvars file, handling the
// ExtOutboundClusterKafkaBroker custom type in addition to standard types.
func GenerateInputsAutoTfvarsWithBrokers(values map[string]any) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	varNames := make([]string, 0, len(values))
	for varName := range values {
		varNames = append(varNames, varName)
	}
	sort.Strings(varNames)

	varSeenVariables := make(map[string]bool)
	for _, varName := range varNames {
		if varSeenVariables[varName] {
			continue
		}
		varSeenVariables[varName] = true

		value := values[varName]
		switch v := value.(type) {
		case string:
			rootBody.SetAttributeValue(varName, cty.StringVal(v))
		case []string:
			ctyValues := make([]cty.Value, len(v))
			for i, s := range v {
				ctyValues[i] = cty.StringVal(s)
			}
			rootBody.SetAttributeValue(varName, cty.ListVal(ctyValues))
		case bool:
			rootBody.SetAttributeValue(varName, cty.BoolVal(v))
		case int:
			rootBody.SetAttributeValue(varName, cty.NumberIntVal(int64(v)))
		case []types.ExtOutboundClusterKafkaBroker:
			brokerObjects := make([]cty.Value, len(v))
			for i, broker := range v {
				endpoints := make([]cty.Value, len(broker.Endpoints))
				for j, endpoint := range broker.Endpoints {
					endpoints[j] = cty.ObjectVal(map[string]cty.Value{
						"host": cty.StringVal(endpoint.Host),
						"port": cty.NumberIntVal(int64(endpoint.Port)),
						"ip":   cty.StringVal(endpoint.IP),
					})
				}
				brokerObjects[i] = cty.ObjectVal(map[string]cty.Value{
					"id":        cty.StringVal(broker.ID),
					"subnet_id": cty.StringVal(broker.SubnetID),
					"endpoints": cty.ListVal(endpoints),
				})
			}
			rootBody.SetAttributeValue(varName, cty.ListVal(brokerObjects))
		}
	}

	return string(f.Bytes())
}

// GenerateInputsAutoTfvars generates an inputs.auto.tfvars file from a map of variable names to values.
// Supports string, []string, bool, and int value types.
func GenerateInputsAutoTfvars(values map[string]any) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	varNames := make([]string, 0, len(values))
	for varName := range values {
		varNames = append(varNames, varName)
	}
	sort.Strings(varNames)

	varSeenVariables := make(map[string]bool)
	for _, varName := range varNames {
		if varSeenVariables[varName] {
			continue
		}
		varSeenVariables[varName] = true

		value := values[varName]
		switch v := value.(type) {
		case string:
			rootBody.SetAttributeValue(varName, cty.StringVal(v))
		case []string:
			ctyValues := make([]cty.Value, len(v))
			for i, s := range v {
				ctyValues[i] = cty.StringVal(s)
			}
			rootBody.SetAttributeValue(varName, cty.ListVal(ctyValues))
		case bool:
			rootBody.SetAttributeValue(varName, cty.BoolVal(v))
		case int:
			rootBody.SetAttributeValue(varName, cty.NumberIntVal(int64(v)))
		}
	}

	return string(f.Bytes())
}
