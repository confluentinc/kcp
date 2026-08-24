package gateway

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// These tests pin kcp's two hard-coded assumptions about the Gateway CRD against
// the schema the operator actually enforces, taken from the shipped chart rather
// than from the contract prose:
//
//  1. spec.configId lives at .spec.versions[].schema.openAPIV3Schema.properties
//     .spec.properties.configId — the path crdSupportsConfigID walks.
//  2. Generated ids satisfy the CRD's own maxLength and pattern. If they did not,
//     every apply would be rejected by the API server, and only against the real
//     schema is that visible without a cluster.
//
// The fixture's provenance is recorded in its header. It is pruned, not
// hand-written: the schema values are verbatim.
const realGatewayCRDFixture = "testdata/gateway-crd-v0.1718.34.yaml"

func loadRealGatewayCRD(t *testing.T) *unstructured.Unstructured {
	t.Helper()

	raw, err := os.ReadFile(realGatewayCRDFixture)
	require.NoError(t, err)

	// sigs.k8s.io/yaml converts YAML to JSON and decodes with encoding/json, so
	// numbers land as float64 — what runtime.DeepCopyJSON (and therefore the
	// unstructured accessors) accept, and what a real API server response yields.
	// Decoding with goccy instead would produce uint64 and panic the deep copy.
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &obj))
	return &unstructured.Unstructured{Object: obj}
}

// configIDSchema pulls the CRD's own constraints for spec.configId, so the tests
// below assert against the operator's schema rather than a copy of it.
func configIDSchema(t *testing.T, crd *unstructured.Unstructured) (maxLength int64, pattern string) {
	t.Helper()

	versions, found, err := unstructured.NestedSlice(crd.Object, "spec", "versions")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, versions)

	version, ok := versions[0].(map[string]any)
	require.True(t, ok)

	schema, found, err := unstructured.NestedMap(version, "schema", "openAPIV3Schema", "properties", "spec", "properties", gatewayConfigIDField)
	require.NoError(t, err)
	require.True(t, found, "the fixture must carry the configId schema")

	switch n := schema["maxLength"].(type) {
	case float64:
		maxLength = int64(n)
	case int64:
		maxLength = n
	default:
		t.Fatalf("unexpected maxLength type %T", schema["maxLength"])
	}

	pattern, ok = schema["pattern"].(string)
	require.True(t, ok, "the CRD must constrain configId with a pattern")
	return maxLength, pattern
}

func TestRealGatewayCRD_IsDetectedAsConfigIDCapable(t *testing.T) {
	crd := loadRealGatewayCRD(t)

	assert.True(t, crdSupportsConfigID(crd),
		"the shipped CRD declares spec.configId; if this fails, the schema path crdSupportsConfigID walks has moved "+
			"and kcp would silently downgrade every capable cluster to rollout verification")
}

func TestRealGatewayCRD_GeneratedConfigIDsSatisfyTheSchema(t *testing.T) {
	crd := loadRealGatewayCRD(t)
	maxLength, pattern := configIDSchema(t, crd)

	crdPattern, err := regexp.Compile(pattern)
	require.NoError(t, err, "the CRD's pattern must be a valid Go regexp for this test to mean anything")

	// Many draws, because the id carries random hex: a length or alphabet bug
	// could otherwise pass by luck.
	for i := 0; i < 200; i++ {
		id, err := NewConfigID()
		require.NoError(t, err)

		assert.LessOrEqual(t, int64(len(id)), maxLength,
			"generated configId %q exceeds the CRD's maxLength of %d — the API server would reject the apply", id, maxLength)
		assert.True(t, crdPattern.MatchString(id),
			"generated configId %q violates the CRD pattern %s — the API server would reject the apply", id, pattern)
	}
}

// TestRealGatewayCRD_PatternRejectsBase64Padding records why the ids are hex.
// The CRD's alphabet excludes +, / and =, so a base64-encoded random id — the
// obvious first choice — would be rejected by the API server for some inputs and
// accepted for others, making it fail intermittently.
func TestRealGatewayCRD_PatternRejectsBase64Padding(t *testing.T) {
	crd := loadRealGatewayCRD(t)
	_, pattern := configIDSchema(t, crd)

	crdPattern, err := regexp.Compile(pattern)
	require.NoError(t, err)

	for _, bad := range []string{"kcp-a+b", "kcp-a/b", "kcp-ab==", "kcp with space", "kcp-é"} {
		assert.False(t, crdPattern.MatchString(bad), "expected the CRD pattern to reject %q", bad)
		assert.Error(t, validateConfigID(bad), "kcp's own validation must reject %q for the same reason", bad)
	}
}

// TestRealGatewayCRD_LocalValidationMatchesSchemaBound guards the constant kcp
// duplicates from the CRD. It is a duplicate by necessity — kcp validates before
// it applies — so it has to be checked against the source.
func TestRealGatewayCRD_LocalValidationMatchesSchemaBound(t *testing.T) {
	crd := loadRealGatewayCRD(t)
	maxLength, pattern := configIDSchema(t, crd)

	assert.Equal(t, int64(maxConfigIDLength), maxLength,
		"kcp's maxConfigIDLength has drifted from the CRD's maxLength")
	assert.Equal(t, pattern, configIDPattern.String(),
		"kcp's configIDPattern has drifted from the CRD's pattern")
}

func TestRealGatewayCRD_HotReloadFlagPathMatches(t *testing.T) {
	crd := loadRealGatewayCRD(t)

	// The CRD declares the flag kcp reads at spec.hotReload.enabled...
	versions, found, err := unstructured.NestedSlice(crd.Object, "spec", "versions")
	require.NoError(t, err)
	require.True(t, found)
	version, ok := versions[0].(map[string]any)
	require.True(t, ok)

	_, found, err = unstructured.NestedMap(version, "schema", "openAPIV3Schema", "properties", "spec", "properties", "hotReload", "properties", "enabled")
	require.NoError(t, err)
	assert.True(t, found, "the shipped CRD must declare spec.hotReload.enabled where hotReloadEnabledInCR reads it")

	// ...and a CR written against it is read correctly.
	cr := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"hotReload": map[string]any{"enabled": true}},
	}}
	assert.True(t, hotReloadEnabledInCR(cr))
}
