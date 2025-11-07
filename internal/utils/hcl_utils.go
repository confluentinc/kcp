package utils

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// FormatHclResourceName ensures that resources are all 'snake_case'.
func FormatHclResourceName(resourceName string) string {
	return strings.ToLower(strings.ReplaceAll(resourceName, "-", "_"))
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
		tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenEqual, Bytes: []byte(" = ")})
		tokens = append(tokens, valueTokens...)
		tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")})
	}

	tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCBrace, Bytes: []byte("}")})
	return tokens
}

// GenerateLifecyleBlock creates tokens for a lifecycle block - supports only 'prevent_destroy' and
// 'create_before_destroy'.
func GenerateLifecycleBlock(lifecycle string, boolean bool) (hclwrite.Tokens, error) {
	var acceptedLifecycles = []string{"prevent_destroy", "create_before_destroy"}
	if !slices.Contains(acceptedLifecycles, lifecycle) {
		return nil, fmt.Errorf("invalid lifecycle: %s", lifecycle)
	}

	return hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")},
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(lifecycle)},
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(strconv.FormatBool(boolean))},
		&hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")},
	}, nil
}
