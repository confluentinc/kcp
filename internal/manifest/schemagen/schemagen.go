// Package schemagen generates the migration.yaml JSON Schema from the manifest
// Go structs. It is used by `go generate` and the drift-guard test only; no
// runtime or command package imports it.
package schemagen

import (
	"encoding/json"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/google/jsonschema-go/jsonschema"
)

// Generate reflects the Migration struct into a JSON Schema, injects the enums
// (from the manifest constants) and the intended required sets, and returns the
// indented JSON (newline-terminated). Output is deterministic.
func Generate() ([]byte, error) {
	s, err := jsonschema.For[manifest.Migration](nil)
	if err != nil {
		return nil, err
	}

	spec := s.Properties["spec"]
	source := spec.Properties["source"]
	target := spec.Properties["target"]
	topics := spec.Properties["topics"]

	source.Properties["type"].Enum = []any{manifest.SourceApacheKafka}
	target.Properties["type"].Enum = []any{manifest.TargetConfluentCloud, manifest.TargetConfluentPlatform}
	topics.Properties["mode"].Enum = []any{manifest.TopicModeMirror, manifest.TopicModeNew}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
