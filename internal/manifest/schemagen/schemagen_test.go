package schemagen

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
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
	require.ElementsMatch(t, []any{"type", "clusterCredentials"}, tgt["required"])
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
	require.ElementsMatch(t, []any{"type", "clusterCredentials"}, requiredOf(spec["target"].(map[string]any)))
	require.ElementsMatch(t, []any{"restEndpoint"}, requiredOf(target["kafka"].(map[string]any)))
	require.ElementsMatch(t, []any{"mode", "include"}, requiredOf(spec["topics"].(map[string]any)))
	// clusterLink.mode is optional (validator defaults empty → "destination"), so
	// only name is required — guards against the schema re-adding mode (M0).
	require.ElementsMatch(t, []any{"name"}, requiredOf(spec["clusterLink"].(map[string]any)))
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

// --- gateway migration schema ---

func gatewayMap(t *testing.T) map[string]any {
	t.Helper()
	b, err := GenerateGateway()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(b, &doc))
	return doc
}

func TestGenerateGateway_TopLevelProperties(t *testing.T) {
	doc := gatewayMap(t)
	p := props(t, doc)
	for _, k := range []string{"apiVersion", "kind", "metadata", "spec", "interpolate"} {
		require.Contains(t, p, k)
	}
	require.Equal(t, false, doc["additionalProperties"])
}

func TestGenerateGateway_Enums(t *testing.T) {
	spec := props(t, props(t, gatewayMap(t))["spec"].(map[string]any))
	srcType := props(t, spec["source"].(map[string]any))["type"].(map[string]any)
	require.Equal(t, []any{"msk", "apache-kafka"}, srcType["enum"],
		"the gateway migration reads from MSK or Apache Kafka only")
	tgtType := props(t, spec["target"].(map[string]any))["type"].(map[string]any)
	require.Equal(t, []any{"confluent-cloud", "confluent-platform"}, tgtType["enum"])
}

// TestGenerateGateway_DurationsAreStringsNotIntegers: jsonschema-go reflects
// time.Duration as an integer, but goccy parses "10m" — without the override
// the yaml-language-server header would flag the documented example as invalid.
//
// The duration fields are enumerated from manifest.DefaultPolicies by reflection
// rather than a hardcoded list: a copy of the list here would pass even if
// GenerateGateway forgot to patch a newly-added time.Duration field, which is
// exactly the omission this test exists to catch.
func TestGenerateGateway_DurationsAreStringsNotIntegers(t *testing.T) {
	spec := props(t, props(t, gatewayMap(t))["spec"].(map[string]any))
	policy := props(t, spec["defaultPolicies"].(map[string]any))

	durationType := reflect.TypeOf(time.Duration(0))
	policyType := reflect.TypeOf(manifest.DefaultPolicies{})
	sawDuration := false
	for i := 0; i < policyType.NumField(); i++ {
		field := policyType.Field(i)
		if field.Type != durationType {
			continue
		}
		sawDuration = true
		name := jsonFieldName(field)
		f, ok := policy[name].(map[string]any)
		require.True(t, ok, "policy.%s (a time.Duration) is missing from the schema", name)
		require.Equal(t, "string", f["type"], "%s must be a duration string", name)
		require.NotEmpty(t, f["pattern"], "%s must carry a duration pattern", name)
	}
	require.True(t, sawDuration, "expected at least one time.Duration field in manifest.DefaultPolicies")

	// The counts stay integers.
	for _, k := range []string{"lagThreshold", "promoteBatchSize"} {
		require.Equal(t, "integer", policy[k].(map[string]any)["type"])
	}
}

// jsonFieldName returns the schema property name for a struct field: its json
// tag minus options, matching how jsonschema-go names reflected properties.
func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

// TestGenerateGateway_CredentialsArePolymorphic — a credentials slot is either
// a path string or an inline mapping, so the schema must accept both.
func TestGenerateGateway_CredentialsArePolymorphic(t *testing.T) {
	doc := gatewayMap(t)
	spec := props(t, props(t, doc)["spec"].(map[string]any))
	src := props(t, spec["source"].(map[string]any))

	oneOf, ok := src["credentials"].(map[string]any)["oneOf"].([]any)
	require.True(t, ok, "spec.source.credentials must be a oneOf")
	require.Len(t, oneOf, 2)
	require.Equal(t, "string", oneOf[0].(map[string]any)["type"])
	require.NotEmpty(t, oneOf[1].(map[string]any)["$ref"])

	defs, ok := doc["$defs"].(map[string]any)
	require.True(t, ok, "the inline branch must $ref a real definition")
	require.NotEmpty(t, defs)
}

// TestGenerateGateway_InlineCredentialsDefRejectsInterpolate — `interpolate` is
// a file-level key that kcp rejects inside an inline block, so the schema must
// not advertise it there.
func TestGenerateGateway_InlineCredentialsDefRejectsInterpolate(t *testing.T) {
	defs := gatewayMap(t)["$defs"].(map[string]any)
	for name, def := range defs {
		p, ok := def.(map[string]any)["properties"].(map[string]any)
		if !ok {
			continue
		}
		require.NotContains(t, p, "interpolate", "$defs.%s must not offer interpolate", name)
	}
}

// TestGenerateGateway_PortsRetiredFlagGuidance — retiring ~56 flags retires ~56
// pieces of --help guidance, so every ported field carries its flag's usage text.
func TestGenerateGateway_PortsRetiredFlagGuidance(t *testing.T) {
	spec := props(t, props(t, gatewayMap(t))["spec"].(map[string]any))

	policy := props(t, spec["defaultPolicies"].(map[string]any))
	require.Contains(t, policy["detectUnroutedProducersDuration"].(map[string]any)["description"],
		"minimum 10s", "the opt-in check's minimum was documented on the flag")
	require.Contains(t, policy["promoteBatchSize"].(map[string]any)["description"], "0")

	gatewayProps := props(t, spec["gateway"].(map[string]any))
	require.Contains(t, gatewayProps["cr-name"].(map[string]any)["description"], "name")

	topicGroup := spec["topicGroup"].(map[string]any)
	require.Contains(t, topicGroup["description"], "fence")

	item := props(t, topicGroup["items"].(map[string]any))
	require.NotEmpty(t, item["topics"].(map[string]any)["description"])
	require.NotContains(t, item["topics"].(map[string]any)["description"], "lag-check",
		"topic selection has no effect on lag-check; the description must not imply otherwise")
	require.Contains(t, item["topicPatterns"].(map[string]any)["description"], "regular expression")
	require.NotEmpty(t, item["route"].(map[string]any)["description"])
	require.NotEmpty(t, item["targetStreamingDomain"].(map[string]any)["description"])
}

func TestGenerateGateway_RequiredSets(t *testing.T) {
	doc := gatewayMap(t)
	p := props(t, doc)
	spec := props(t, p["spec"].(map[string]any))

	requiredOf := func(schema map[string]any) []any {
		r, _ := schema["required"].([]any)
		return r
	}
	require.ElementsMatch(t, []any{"apiVersion", "kind", "metadata", "spec"}, requiredOf(doc))
	require.ElementsMatch(t, []any{"source", "target", "clusterLink", "gateway", "topicGroup"}, requiredOf(p["spec"].(map[string]any)))
	require.ElementsMatch(t, []any{"type", "clusterId", "kafka"}, requiredOf(spec["target"].(map[string]any)))
	// kafka's reflected required set is only restEndpoint, but Validate() also
	// requires bootstrapServers and credentials — the schema must match so a lint
	// pass cannot green-light a manifest init will reject. restCredentials is
	// derived and stays optional.
	require.ElementsMatch(t, []any{"restEndpoint", "bootstrapServers", "credentials"},
		requiredOf(props(t, spec["target"].(map[string]any))["kafka"].(map[string]any)))
	require.ElementsMatch(t, []any{"namespace", "cr-name"}, requiredOf(spec["gateway"].(map[string]any)))
	// The old crs/routes/topics shape is gone from the schema entirely (O2): a
	// stale key must be flagged by the editor, not offered as legal.
	gwProps := props(t, spec["gateway"].(map[string]any))
	require.NotContains(t, gwProps, "crs", "the crs block is retired — see cr-name")
	require.NotContains(t, gwProps, "routes", "routes are retired — see topicGroup")
	require.NotContains(t, spec, "topics", "spec.topics is retired — see topicGroup")
	// Each topicGroup entry pairs a route with its target streaming domain. The
	// bootstrap server id is derived from the live CR (D1) and mode from the CR
	// (D5), so neither is a required manifest field.
	item := spec["topicGroup"].(map[string]any)["items"].(map[string]any)
	require.ElementsMatch(t, []any{"route", "targetStreamingDomain"}, item["required"])
	// D2: at least one of topics/topicPatterns, hand-patched as an anyOf since
	// the constraint isn't expressible on the struct.
	anyOf, ok := item["anyOf"].([]any)
	require.True(t, ok, "the topicGroup item must carry an anyOf for the at-least-one topics/topicPatterns rule")
	require.Len(t, anyOf, 2)
	// lagThreshold's zero value is meaningful (fail-safe/strictest), not a
	// stand-in for "omitted", so the key must be required even though the
	// block it lives in (spec.defaultPolicies) stays optional.
	require.ElementsMatch(t, []any{"lagThreshold"}, requiredOf(spec["defaultPolicies"].(map[string]any)))
}

func TestGatewaySchemaInSync(t *testing.T) {
	got, err := GenerateGateway()
	require.NoError(t, err)
	want, err := os.ReadFile("../gatewaymigration.schema.json")
	require.NoError(t, err)
	require.Equal(t, string(want), string(got),
		"gatewaymigration.schema.json is stale — run: go generate ./internal/manifest/...")
}

// TestGenerate_MigrationCredentialsArePolymorphicToo — widening the existing
// credentials fields to CredentialsRef must be reflected in kind: Migration's
// schema, or editors would flag the newly-legal inline spelling as invalid.
func TestGenerate_MigrationCredentialsArePolymorphicToo(t *testing.T) {
	doc := asMap(t)
	spec := props(t, props(t, doc)["spec"].(map[string]any))
	src := props(t, spec["source"].(map[string]any))
	oneOf, ok := src["credentials"].(map[string]any)["oneOf"].([]any)
	require.True(t, ok, "spec.source.credentials must be a oneOf")
	require.Len(t, oneOf, 2)
}

// TestGenerate_MigrationTargetKafkaOmitsGatewayOnlyCredentials — credentials and
// restCredentials live on the shared TargetKafka for kind: GatewayMigration
// only; kcp migrate ignores them and Validate() rejects them, so the kind:
// Migration schema must not offer them (they otherwise reflect as a broken raw
// {Path, Inline} object).
func TestGenerate_MigrationTargetKafkaOmitsGatewayOnlyCredentials(t *testing.T) {
	spec := props(t, props(t, asMap(t))["spec"].(map[string]any))
	kafka := props(t, props(t, spec["target"].(map[string]any))["kafka"].(map[string]any))
	require.NotContains(t, kafka, "credentials")
	require.NotContains(t, kafka, "restCredentials")
}
