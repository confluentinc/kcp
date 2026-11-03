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
	for _, k := range []string{"apiVersion", "kind", "metadata", "spec"} {
		require.Contains(t, p, k)
	}
	require.NotContains(t, p, "interpolate", "the top-level interpolate field is retired")
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

func sections(t *testing.T) (spec, static, dynamic, conv map[string]any) {
	t.Helper()
	spec = props(t, props(t, gatewayMap(t))["spec"].(map[string]any))
	return spec, spec["staticMigration"].(map[string]any), spec["dynamicMigration"].(map[string]any), spec["dynamicToStaticConversion"].(map[string]any)
}

func requiredOf(schema map[string]any) []any {
	r, _ := schema["required"].([]any)
	return r
}

func TestGenerateGateway_SpecDeclaresExactlyOneSection(t *testing.T) {
	p := props(t, gatewayMap(t))["spec"].(map[string]any)
	oneOf, ok := p["oneOf"].([]any)
	require.True(t, ok, "spec must carry a oneOf: exactly one of the three migration sections")
	require.Len(t, oneOf, 3)
	var got []string
	for _, b := range oneOf {
		got = append(got, b.(map[string]any)["required"].([]any)[0].(string))
	}
	require.ElementsMatch(t, []string{"staticMigration", "dynamicMigration", "dynamicToStaticConversion"}, got)
	spec := props(t, p)
	for _, k := range []string{"route", "defaultPolicies"} {
		require.NotContains(t, spec, k, "spec.%s is retired — see the three migration sections", k)
	}
	for _, k := range []string{"pauseConsumerOffsetSync", "consumerOffsetSyncBaseline"} {
		require.NotContains(t, props(t, spec["clusterLink"].(map[string]any)), k)
	}
}

func TestGenerateGateway_SectionsAreObjectsWithTheirOwnFields(t *testing.T) {
	_, static, dynamic, conv := sections(t)
	for name, sec := range map[string]map[string]any{"static": static, "dynamic": dynamic, "conv": conv} {
		require.Equal(t, "object", sec["type"], "%s must be a plain object, not nullable", name)
		route := props(t, sec)["route"].(map[string]any)
		require.ElementsMatch(t, []any{"name", "targetStreamingDomain"}, requiredOf(route))
		require.NotEmpty(t, sec["description"], "%s must carry a description", name)
		require.NotEmpty(t, route["description"])
	}
	require.ElementsMatch(t, []any{"route", "topicGroup"}, requiredOf(static))
	require.ElementsMatch(t, []any{"route", "topicGroup"}, requiredOf(dynamic))
	require.ElementsMatch(t, []any{"route"}, requiredOf(conv))
	require.NotContains(t, props(t, conv), "topicGroup", "a conversion selects no topics")
	require.Contains(t, props(t, static), "consumerOffsetSync")
	require.NotContains(t, props(t, dynamic), "consumerOffsetSync")

	// At least one of topics/topicPatterns (not expressible on the struct).
	for _, sec := range []map[string]any{static, dynamic} {
		item := props(t, sec)["topicGroup"].(map[string]any)["items"].(map[string]any)
		anyOf, ok := item["anyOf"].([]any)
		require.True(t, ok)
		require.Len(t, anyOf, 2)
	}
}

func TestGenerateGateway_OffsetSyncBaselineEnum(t *testing.T) {
	_, static, _, _ := sections(t)
	cos := props(t, props(t, static)["consumerOffsetSync"].(map[string]any))
	require.Equal(t, []any{"enabled", "disabled"}, cos["baseline"].(map[string]any)["enum"])
}

// Review Focus: every policies block carries its section's fields — including the
// inlined common fields, which the reflector must promote.
func TestGenerateGateway_PoliciesPerSection(t *testing.T) {
	_, static, dynamic, conv := sections(t)
	want := map[string][]string{
		"static":  {"lagThreshold", "promoteBatchSize", "rolloutTimeout", "detectUnroutedProducersDuration", "consumerOffsetSyncDrainDuration", "hotReloadTimeout", "gatewayConfigPort"},
		"dynamic": {"lagThreshold", "promoteBatchSize", "rolloutTimeout", "detectUnroutedProducersDuration", "hotReloadTimeout", "gatewayConfigPort"},
		"conv":    {"rolloutTimeout", "detectUnroutedCommitsDuration", "offsetSyncConcurrency", "hotReloadTimeout", "gatewayConfigPort"},
	}
	for name, sec := range map[string]map[string]any{"static": static, "dynamic": dynamic, "conv": conv} {
		policy := props(t, props(t, sec)["policies"].(map[string]any))
		var got []string
		for k := range policy {
			got = append(got, k)
		}
		require.ElementsMatch(t, want[name], got, "%s policies", name)
		for k, v := range policy {
			require.NotEmpty(t, v.(map[string]any)["description"], "%s.policies.%s needs a description", name, k)
		}
	}
	require.ElementsMatch(t, []any{"lagThreshold"}, requiredOf(props(t, static)["policies"].(map[string]any)))
	require.ElementsMatch(t, []any{"lagThreshold"}, requiredOf(props(t, dynamic)["policies"].(map[string]any)))
}

// jsonschema-go reflects time.Duration as an integer, but goccy parses "10m";
// without the override the yaml-language-server header would flag the documented
// examples as invalid. Durations are found by reflection (recursing into the
// inlined common fields) so a newly added Duration field that GenerateGateway
// forgets to patch fails here.
func TestGenerateGateway_DurationsAreStringsNotIntegers(t *testing.T) {
	_, static, dynamic, conv := sections(t)
	durationType := reflect.TypeOf(time.Duration(0))
	check := func(name string, sec map[string]any, v any) {
		policy := props(t, props(t, sec)["policies"].(map[string]any))
		saw := false
		var walk func(rt reflect.Type)
		walk = func(rt reflect.Type) {
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				if f.Anonymous {
					walk(f.Type)
					continue
				}
				if f.Type != durationType {
					continue
				}
				saw = true
				n := jsonFieldName(f)
				got, ok := policy[n].(map[string]any)
				require.True(t, ok, "%s policies.%s (a time.Duration) is missing from the schema", name, n)
				require.Equal(t, "string", got["type"], "%s.%s must be a duration string", name, n)
				require.NotEmpty(t, got["pattern"])
			}
		}
		walk(reflect.TypeOf(v))
		require.True(t, saw, "%s: expected at least one time.Duration field", name)
	}
	check("static", static, manifest.StaticPolicies{})
	check("dynamic", dynamic, manifest.DynamicPolicies{})
	check("conv", conv, manifest.DynamicToStaticPolicies{})
	require.Equal(t, "integer", props(t, props(t, static)["policies"].(map[string]any))["lagThreshold"].(map[string]any)["type"])
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

// TestGenerateGateway_CredentialsAreStrings — every credentials slot is a
// file-path string only; inline mappings are gone, so no property is a oneOf and
// no $defs are emitted.
func TestGenerateGateway_CredentialsAreStrings(t *testing.T) {
	doc := gatewayMap(t)
	spec := props(t, props(t, doc)["spec"].(map[string]any))
	src := props(t, spec["source"].(map[string]any))
	kafka := props(t, props(t, spec["target"].(map[string]any))["kafka"].(map[string]any))
	link := props(t, spec["clusterLink"].(map[string]any))

	for _, f := range []map[string]any{
		src["credentials"].(map[string]any),
		kafka["clusterCredentials"].(map[string]any),
		link["linkCredentials"].(map[string]any),
	} {
		require.Equal(t, "string", f["type"])
		require.NotContains(t, f, "oneOf")
		require.NotContains(t, f, "$ref")
	}

	require.NotContains(t, doc, "$defs", "no field accepts an inline object, so there are no credential $defs")
}

// TestGenerateGateway_NoOneOfAnywhere — with inline credentials removed, no
// oneOf shape survives anywhere under source, target, or clusterLink.
func TestGenerateGateway_NoOneOfAnywhere(t *testing.T) {
	spec := props(t, props(t, gatewayMap(t))["spec"].(map[string]any))
	for _, sec := range []string{"source", "target", "clusterLink"} {
		assertNoKey(t, spec[sec], "oneOf")
	}
}

// assertNoKey fails if key appears anywhere in the nested map/slice structure.
func assertNoKey(t *testing.T, v any, key string) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		require.NotContains(t, x, key)
		for _, val := range x {
			assertNoKey(t, val, key)
		}
	case []any:
		for _, val := range x {
			assertNoKey(t, val, key)
		}
	}
}

func TestGenerateGateway_RequiredSets(t *testing.T) {
	doc := gatewayMap(t)
	p := props(t, doc)
	spec := props(t, p["spec"].(map[string]any))
	require.ElementsMatch(t, []any{"apiVersion", "kind", "metadata", "spec"}, requiredOf(doc))
	require.ElementsMatch(t, []any{"source", "target", "clusterLink", "gateway"}, requiredOf(p["spec"].(map[string]any)),
		"the three sections are optional individually; the spec-level oneOf requires exactly one")
	require.ElementsMatch(t, []any{"type", "clusterId", "kafka"}, requiredOf(spec["target"].(map[string]any)))
	require.ElementsMatch(t, []any{"restEndpoint", "bootstrapServers", "clusterCredentials"},
		requiredOf(props(t, spec["target"].(map[string]any))["kafka"].(map[string]any)))
	require.ElementsMatch(t, []any{"name", "linkCredentials"}, requiredOf(spec["clusterLink"].(map[string]any)))
	require.ElementsMatch(t, []any{"namespace", "cr-name"}, requiredOf(spec["gateway"].(map[string]any)))
	gwProps := props(t, spec["gateway"].(map[string]any))
	require.NotContains(t, gwProps, "crs", "the crs block is retired — see cr-name")
	require.NotContains(t, gwProps, "routes", "routes are retired — see the section's route")
	require.NotContains(t, spec, "topics", "spec.topics is retired — see the section's topicGroup")
}

func TestGenerateGateway_PortsRetiredFlagGuidance(t *testing.T) {
	spec, static, dynamic, _ := sections(t)
	policy := props(t, props(t, static)["policies"].(map[string]any))
	require.Contains(t, policy["detectUnroutedProducersDuration"].(map[string]any)["description"], "minimum 10s")
	require.Contains(t, policy["promoteBatchSize"].(map[string]any)["description"], "0")

	gatewayProps := props(t, spec["gateway"].(map[string]any))
	require.Contains(t, gatewayProps["cr-name"].(map[string]any)["description"], "name")

	route := props(t, props(t, dynamic)["route"].(map[string]any))
	require.NotEmpty(t, route["name"].(map[string]any)["description"])
	require.NotEmpty(t, route["targetStreamingDomain"].(map[string]any)["description"])

	topicGroup := props(t, dynamic)["topicGroup"].(map[string]any)
	require.Contains(t, topicGroup["description"], "fence")
	item := props(t, topicGroup["items"].(map[string]any))
	require.NotContains(t, item["topics"].(map[string]any)["description"], "lag-check")
	require.Contains(t, item["topicPatterns"].(map[string]any)["description"], "regular expression")
}

func TestGatewaySchemaInSync(t *testing.T) {
	got, err := GenerateGateway()
	require.NoError(t, err)
	want, err := os.ReadFile("../gatewaymigration.schema.json")
	require.NoError(t, err)
	require.Equal(t, string(want), string(got),
		"gatewaymigration.schema.json is stale — run: go generate ./internal/manifest/...")
}

// TestGenerate_MigrationCredentialsAreStrings — every credentials field on kind:
// Migration is a file-path string only; the inline spelling is gone repo-wide,
// so no credentials property is a oneOf.
func TestGenerate_MigrationCredentialsAreStrings(t *testing.T) {
	doc := asMap(t)
	spec := props(t, props(t, doc)["spec"].(map[string]any))
	src := props(t, spec["source"].(map[string]any))
	require.Equal(t, "string", src["credentials"].(map[string]any)["type"])
	require.NotContains(t, src["credentials"].(map[string]any), "oneOf")

	tgt := props(t, spec["target"].(map[string]any))
	require.Equal(t, "string", tgt["clusterCredentials"].(map[string]any)["type"])

	for _, sec := range []string{"source", "target", "clusterLink"} {
		if s, ok := spec[sec]; ok {
			assertNoKey(t, s, "oneOf")
		}
	}
	require.NotContains(t, doc, "$defs")
}

// TestGenerate_MigrationTargetKafkaOmitsGatewayOnlyCredentials —
// clusterCredentials lives on the shared TargetKafka for kind: GatewayMigration
// only; kcp migrate ignores it and Validate() rejects it, so the kind: Migration
// schema must not offer it (it otherwise reflects as a broken raw {Path} object).
func TestGenerate_MigrationTargetKafkaOmitsGatewayOnlyCredentials(t *testing.T) {
	spec := props(t, props(t, asMap(t))["spec"].(map[string]any))
	kafka := props(t, props(t, spec["target"].(map[string]any))["kafka"].(map[string]any))
	require.NotContains(t, kafka, "clusterCredentials")
	require.NotContains(t, kafka, "credentials")
	require.NotContains(t, kafka, "restCredentials")
}
