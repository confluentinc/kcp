package utils

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

var specialCharsRegex = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// FormatHclResourceName ensures that resources are all 'snake_case' and only contains alphanumeric characters and underscores.
func FormatHclResourceName(resourceName string) string {
	result := specialCharsRegex.ReplaceAllString(resourceName, "_")
	return strings.ToLower(result)
}

// TokensForModuleOutput creates tokens for a Terraform module output reference (e.g., "module.networking.jump_cluster_broker_subnet_ids")
func TokensForModuleOutput(moduleName, outputName string) hclwrite.Tokens {
	return hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("module." + moduleName + "." + outputName)},
	}
}

// TokensForTemplate creates properly formatted tokens for a template string (string with ${} interpolations)
func TokensForStringTemplate(template string) hclwrite.Tokens {
	return hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte(template)},
		&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
	}
}

// TokensForReference creates tokens for a resource reference (e.g., "confluent_environment.environment.id")
func TokensForResourceReference(ref string) hclwrite.Tokens {
	return hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(ref)},
	}
}

// TokensForVarReference creates tokens for a Terraform variable reference (e.g., "var.my_variable")
func TokensForVarReference(varName string) hclwrite.Tokens {
	return hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("var." + varName)},
	}
}

// TokensForVarReferenceList creates tokens for a list of variable references (e.g., [var.name1, var.name2])
func TokensForVarReferenceList(varNames []string) hclwrite.Tokens {
	tokens := hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")},
	}

	for i, varName := range varNames {
		if i > 0 {
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(", ")})
		}
		tokens = append(tokens, TokensForVarReference(varName)...)
	}

	tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")})
	return tokens
}

// TokensForList creates tokens for an array literal
func TokensForList(items []string) hclwrite.Tokens {
	tokens := hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")},
	}

	for i, item := range items {
		tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(item)})
		if i < len(items)-1 {
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(",")})
		}
	}

	tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")})
	return tokens
}

// TokensForStringList creates tokens for a list of quoted strings (e.g., ["item1", "item2"])
func TokensForStringList(items []string) hclwrite.Tokens {
	values := make([]cty.Value, len(items))
	for i, item := range items {
		values[i] = cty.StringVal(item)
	}

	return hclwrite.TokensForValue(cty.ListVal(values))
}

func TokensForComment(comment string) hclwrite.Tokens {
	return hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenComment, Bytes: []byte(comment)},
	}
}

// TokensForFunctionCall creates tokens for a function call with a string template argument
// e.g., base64encode("${var.key}:${var.secret}")
func TokensForFunctionCall(functionName string, args ...hclwrite.Tokens) hclwrite.Tokens {
	tokens := hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(functionName)},
		&hclwrite.Token{Type: hclsyntax.TokenOParen, Bytes: []byte("(")},
	}

	for i, arg := range args {
		if i > 0 {
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(", ")})
		}
		tokens = append(tokens, arg...)
	}

	tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCParen, Bytes: []byte(")")})
	return tokens
}

// TokensForMap creates tokens for a map/object with string keys and token values
// e.g., { key1 = value1, key2 = value2 }
func TokensForMap(entries map[string]hclwrite.Tokens) hclwrite.Tokens {
	tokens := hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOBrace, Bytes: []byte("{")},
		&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
	}

	for key, valueTokens := range entries {
		tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(key)})
		tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenEqual, Bytes: []byte("=")})
		tokens = append(tokens, valueTokens...)
		tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")})
	}

	tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCBrace, Bytes: []byte("}")})
	return tokens
}

// GenerateLifecyleBlock creates tokens for a lifecycle block - supports only 'prevent_destroy' and
// 'create_before_destroy'.
func GenerateLifecycleBlock(resourceBlock *hclwrite.Block, lifecycle string, boolean bool) error {
	var acceptedLifecycles = []string{"prevent_destroy", "create_before_destroy"}
	if !slices.Contains(acceptedLifecycles, lifecycle) {
		return fmt.Errorf("invalid lifecycle: %s", lifecycle)
	}

	lifecycleBlock := resourceBlock.Body().AppendNewBlock("lifecycle", nil)
	lifecycleBlock.Body().SetAttributeValue(lifecycle, cty.BoolVal(boolean))

	return nil
}

func ConvertToCtyValue(v any) cty.Value {
	switch val := v.(type) {
	case string:
		return cty.StringVal(val)
	case int:
		return cty.NumberIntVal(int64(val))
	case int64:
		return cty.NumberIntVal(val)
	case bool:
		return cty.BoolVal(val)
	default:
		return cty.NilVal
	}
}

// TokensForConditional creates tokens for a ternary conditional expression
// condition ? trueValue : falseValue
func TokensForConditional(condition, trueValue, falseValue hclwrite.Tokens) hclwrite.Tokens {
	tokens := hclwrite.Tokens{}
	tokens = append(tokens, condition...)
	tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenQuestion, Bytes: []byte("?")})
	tokens = append(tokens, trueValue...)
	tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenColon, Bytes: []byte(":")})
	tokens = append(tokens, falseValue...)
	return tokens
}
