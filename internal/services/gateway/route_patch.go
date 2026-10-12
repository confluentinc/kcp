package gateway

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// FragmentValue parses a single-key reconcile fragment ({fence: ...},
// {rules: ...}, {route: ...}) and returns the value at key, for a RoutePatch
// the gateway service applies as a JSON Patch.
func FragmentValue(fragment []byte, key string) (any, error) {
	var m map[string]any
	if err := yaml.Unmarshal(fragment, &m); err != nil {
		return nil, fmt.Errorf("parsing gateway fragment: %w", err)
	}
	v, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("gateway fragment has no top-level %s key", key)
	}
	if v == nil {
		return nil, fmt.Errorf("gateway fragment's top-level %s key is null", key)
	}
	return v, nil
}
