package manifest

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// Parse decodes a migration manifest from YAML with strict decoding:
// unknown fields and duplicate keys are rejected so typos surface.
func Parse(data []byte) (*Migration, error) {
	var m Migration
	if err := yaml.UnmarshalWithOptions(data, &m, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parsing migration manifest: %w", err)
	}
	return &m, nil
}
