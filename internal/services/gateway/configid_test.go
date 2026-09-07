package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNewConfigID(t *testing.T) {
	t.Run("always satisfies the CRD-enforced format", func(t *testing.T) {
		for range 200 {
			id, err := NewConfigID()
			require.NoError(t, err)

			assert.Regexp(t, configIDPattern, id)
			assert.LessOrEqual(t, len(id), maxConfigIDLength)
			assert.NotEmpty(t, id)
		}
	})

	t.Run("never emits base64 padding characters", func(t *testing.T) {
		// The CRD pattern rejects +, / and =, so a base64-derived id would be
		// refused by the API server. Guard the generator against regressing to one.
		for range 200 {
			id, err := NewConfigID()
			require.NoError(t, err)

			assert.NotContains(t, id, "+")
			assert.NotContains(t, id, "/")
			assert.NotContains(t, id, "=")
		}
	})

	t.Run("is unique across calls", func(t *testing.T) {
		// The contract requires each configId to differ from the last value sent;
		// a repeat would make a transition unverifiable.
		seen := make(map[string]struct{}, 1000)
		for range 1000 {
			id, err := NewConfigID()
			require.NoError(t, err)
			require.NotContains(t, seen, id, "generated a duplicate configId")
			seen[id] = struct{}{}
		}
	})

	t.Run("is identifiable as kcp's", func(t *testing.T) {
		id, err := NewConfigID()
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(id, configIDPrefix),
			"a configId in a gateway log should be traceable back to kcp")
	})
}

func TestValidateConfigID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "generated-style hex id", id: "kcp-0123456789abcdef0123456789abcdef"},
		{name: "uuid form", id: "3f1a9c8e-1b2c-4d5e-8f90-a1b2c3d4e5f6"},
		{name: "all permitted specials", id: "a.b_c:d-e"},
		{name: "single character", id: "a"},
		{name: "exactly 64 characters", id: strings.Repeat("a", 64)},

		{name: "empty", id: "", wantErr: true},
		{name: "65 characters", id: strings.Repeat("a", 65), wantErr: true},
		{name: "base64 plus", id: "abc+def", wantErr: true},
		{name: "base64 slash", id: "abc/def", wantErr: true},
		{name: "base64 padding", id: "abcdef==", wantErr: true},
		{name: "contains a space", id: "abc def", wantErr: true},
		{name: "contains a newline", id: "abc\ndef", wantErr: true},
		{name: "leading newline only", id: "\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfigID(tt.id)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestPrepareGatewayApply(t *testing.T) {
	const ns, gw = "confluent", "test-gateway"

	minimalCR := []byte(`
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: some-other-name
  namespace: some-other-namespace
spec:
  replicas: 3
`)

	t.Run("injects the configId into spec", func(t *testing.T) {
		obj, err := prepareGatewayApply(minimalCR, ns, gw, "kcp-abc123")
		require.NoError(t, err)

		got, found, err := unstructured.NestedString(obj.Object, "spec", gatewayConfigIDField)
		require.NoError(t, err)
		require.True(t, found, "spec.configId must be present")
		assert.Equal(t, "kcp-abc123", got)
	})

	t.Run("leaves spec.configId absent when no id is supplied", func(t *testing.T) {
		// The VerifyRollout path on a pre-hot-reload cluster: server-side apply
		// hard-fails on an undeclared field, so kcp must not write one at all.
		obj, err := prepareGatewayApply(minimalCR, ns, gw, "")
		require.NoError(t, err)

		_, found, err := unstructured.NestedString(obj.Object, "spec", gatewayConfigIDField)
		require.NoError(t, err)
		assert.False(t, found, "spec.configId must not be written when no id is supplied")
	})

	t.Run("overwrites a configId already present in the user's YAML", func(t *testing.T) {
		withID := []byte(`
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  configId: users-own-value
  replicas: 3
`)
		obj, err := prepareGatewayApply(withID, ns, gw, "kcp-abc123")
		require.NoError(t, err)

		got, _, err := unstructured.NestedString(obj.Object, "spec", gatewayConfigIDField)
		require.NoError(t, err)
		assert.Equal(t, "kcp-abc123", got, "kcp owns the field for the duration of a migration")
	})

	t.Run("preserves a user configId when kcp is not injecting", func(t *testing.T) {
		withID := []byte(`
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  configId: users-own-value
`)
		obj, err := prepareGatewayApply(withID, ns, gw, "")
		require.NoError(t, err)

		got, _, err := unstructured.NestedString(obj.Object, "spec", gatewayConfigIDField)
		require.NoError(t, err)
		assert.Equal(t, "users-own-value", got)
	})

	t.Run("creates the spec block when the CR has none", func(t *testing.T) {
		noSpec := []byte(`
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: x
`)
		obj, err := prepareGatewayApply(noSpec, ns, gw, "kcp-abc123")
		require.NoError(t, err)

		got, found, err := unstructured.NestedString(obj.Object, "spec", gatewayConfigIDField)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "kcp-abc123", got)
	})

	t.Run("forces name and namespace to the migration target", func(t *testing.T) {
		obj, err := prepareGatewayApply(minimalCR, ns, gw, "kcp-abc123")
		require.NoError(t, err)

		assert.Equal(t, gw, obj.GetName())
		assert.Equal(t, ns, obj.GetNamespace())
	})

	t.Run("preserves the rest of the spec", func(t *testing.T) {
		obj, err := prepareGatewayApply(minimalCR, ns, gw, "kcp-abc123")
		require.NoError(t, err)

		spec, ok := obj.Object["spec"].(map[string]any)
		require.True(t, ok)
		// Read the raw value rather than using unstructured.NestedInt64: goccy
		// decodes YAML integers as uint64, and NestedInt64 type-asserts to int64.
		// This is why injectConfigID writes the map directly instead of using
		// unstructured.SetNestedField, whose deep copy would panic on uint64.
		assert.Equal(t, "3", fmt.Sprint(spec["replicas"]))
	})

	t.Run("rejects an invalid configId before touching the cluster", func(t *testing.T) {
		_, err := prepareGatewayApply(minimalCR, ns, gw, "not+valid")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configId")
	})

	t.Run("rejects unparseable YAML", func(t *testing.T) {
		_, err := prepareGatewayApply([]byte("\tnot: [valid"), ns, gw, "kcp-abc123")
		require.Error(t, err)
	})

	t.Run("rejects a CR whose spec is not a map", func(t *testing.T) {
		// Without a guard this would panic or silently discard the CR body.
		badSpec := []byte(`
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec: "a string, not a map"
`)
		_, err := prepareGatewayApply(badSpec, ns, gw, "kcp-abc123")
		require.Error(t, err)
	})

	t.Run("injected id survives a YAML round trip", func(t *testing.T) {
		// The object is handed to server-side apply; make sure the injected field
		// is real structured data and not lost in re-serialisation.
		obj, err := prepareGatewayApply(minimalCR, ns, gw, "kcp-abc123")
		require.NoError(t, err)

		out, err := yaml.Marshal(obj.Object)
		require.NoError(t, err)
		assert.Contains(t, string(out), "kcp-abc123")
	})
}
