package manifest

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// parseStrict decodes YAML into v with strict decoding: unknown fields and
// duplicate keys are rejected so typos surface rather than being ignored.
//
// A single helper because Strict() must be applied at every decode point,
// including inside CredentialsRef's custom unmarshaler, where goccy does NOT
// propagate the outer decode's options.
func parseStrict(data []byte, v any) error {
	return yaml.UnmarshalWithOptions(data, v, yaml.Strict())
}

// Parse decodes a kind: Migration manifest with strict decoding.
//
// It peeks at the envelope first so that a GatewayMigration file yields "this
// is a kcp migration manifest" rather than a field-by-field unknown-key dump —
// the two kinds share an apiVersion and are told apart only by kind.
func Parse(data []byte) (*Migration, error) {
	kind, err := ParseKind(data)
	if err != nil {
		return nil, err
	}
	if kind != KindMigration {
		return nil, wrongKindError(kind, KindMigration)
	}

	var m Migration
	if err := parseStrict(data, &m); err != nil {
		return nil, fmt.Errorf("parsing migration manifest: %w", err)
	}
	return &m, nil
}
