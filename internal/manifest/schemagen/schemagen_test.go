package schemagen

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func asMap(t *testing.T) map[string]any {
	t.Helper()
	b, err := Generate()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(b, &doc))
	return doc
}

func props(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	p, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "expected properties object")
	return p
}

func TestGenerate_TopLevelProperties(t *testing.T) {
	doc := asMap(t)
	p := props(t, doc)
	for _, k := range []string{"apiVersion", "kind", "metadata", "spec"} {
		require.Contains(t, p, k)
	}
	require.Equal(t, false, doc["additionalProperties"])
	require.ElementsMatch(t, []any{"apiVersion", "kind", "metadata", "spec"}, doc["required"])
}

func TestGenerate_Enums(t *testing.T) {
	spec := props(t, props(t, asMap(t))["spec"].(map[string]any))
	srcType := props(t, spec["source"].(map[string]any))["type"].(map[string]any)
	require.Equal(t, []any{"msk", "apache-kafka", "confluent-platform"}, srcType["enum"])
	tgtType := props(t, spec["target"].(map[string]any))["type"].(map[string]any)
	require.Equal(t, []any{"confluent-cloud", "confluent-platform"}, tgtType["enum"])
	mode := props(t, spec["topics"].(map[string]any))["mode"].(map[string]any)
	require.Equal(t, []any{"mirror", "new"}, mode["enum"])
}

func TestGenerate_OptionalFieldsNotRequired(t *testing.T) {
	doc := asMap(t)
	spec := props(t, doc)["spec"].(map[string]any)
	require.ElementsMatch(t, []any{"source", "target"}, spec["required"])
	tgt := props(t, spec)["target"].(map[string]any)
	require.ElementsMatch(t, []any{"type", "credentials"}, tgt["required"])
}

func TestGenerate_AllRequiredSets(t *testing.T) {
	doc := asMap(t)
	p := props(t, doc)
	spec := props(t, p["spec"].(map[string]any))
	target := props(t, spec["target"].(map[string]any))

	requiredOf := func(schema map[string]any) []any {
		r, _ := schema["required"].([]any)
		return r
	}

	require.ElementsMatch(t, []any{"apiVersion", "kind", "metadata", "spec"}, requiredOf(doc))
	require.ElementsMatch(t, []any{"name"}, requiredOf(p["metadata"].(map[string]any)))
	require.ElementsMatch(t, []any{"source", "target"}, requiredOf(p["spec"].(map[string]any)))
	require.ElementsMatch(t, []any{"type", "bootstrapServers", "credentials"}, requiredOf(spec["source"].(map[string]any)))
	require.ElementsMatch(t, []any{"type", "credentials"}, requiredOf(spec["target"].(map[string]any)))
	require.ElementsMatch(t, []any{"restEndpoint"}, requiredOf(target["kafka"].(map[string]any)))
	require.ElementsMatch(t, []any{"mode", "include"}, requiredOf(spec["topics"].(map[string]any)))
	for _, sec := range []string{"acls", "schemas", "connectors"} {
		require.ElementsMatch(t, []any{"include"}, requiredOf(spec[sec].(map[string]any)))
	}
}

func TestSchemaInSync(t *testing.T) {
	got, err := Generate()
	require.NoError(t, err)
	want, err := os.ReadFile("../migration.schema.json")
	require.NoError(t, err)
	require.Equal(t, string(want), string(got),
		"migration.schema.json is stale — run: go generate ./internal/manifest/...")
}
