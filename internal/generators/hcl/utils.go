package hcl

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

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

func GenerateLifecyleBlock(lifecycle string, boolean bool) (hclwrite.Tokens, error) {
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