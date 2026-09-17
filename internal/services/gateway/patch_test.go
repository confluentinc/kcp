package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sjson "k8s.io/apimachinery/pkg/util/json"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// capturedPatch records the patch a JSON Patch write issued, then lets the fake
// tracker apply it (the fake client natively services JSONPatchType).
type capturedPatch struct {
	patchType    types.PatchType
	fieldManager string
	force        bool
	body         []byte
	called       bool
}

func capturePatch(cs *dynamicfake.FakeDynamicClient) *capturedPatch {
	c := &capturedPatch{}
	cs.PrependReactor("patch", "gateways", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pa, ok := action.(k8stesting.PatchActionImpl)
		if !ok {
			return false, nil, nil
		}
		c.called = true
		c.patchType = pa.GetPatchType()
		c.fieldManager = pa.PatchOptions.FieldManager
		c.force = pa.PatchOptions.Force != nil && *pa.PatchOptions.Force
		c.body = pa.GetPatch()
		return false, nil, nil // fall through to the default reactor, which applies the JSON patch
	})
	return c
}

func seededGateway(ns, name, route string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "platform.confluent.io/v1beta1",
		"kind":       "Gateway",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{
			"routes": []any{
				map[string]any{"name": route, "streamingDomain": "cp-a"},
			},
		},
	}}
}

func TestPatchGatewayRoute(t *testing.T) {
	const ns, gw, route = "confluent", "test-gateway", "migration-route"

	t.Run("uses JSON Patch with no field manager and no force", func(t *testing.T) {
		cs := newFakeDynamicClient(seededGateway(ns, gw, route))
		captured := capturePatch(cs)

		stored, err := patchGatewayRoute(context.Background(), cs, ns, gw,
			RoutePatch{RouteName: route, Field: "streamingDomain", Value: "cp-b"}, "kcp-abc123")
		require.NoError(t, err)
		assert.Equal(t, "kcp-abc123", stored)

		require.True(t, captured.called)
		assert.Equal(t, types.JSONPatchType, captured.patchType)
		assert.Empty(t, captured.fieldManager)
		assert.False(t, captured.force)

		// The tracker applied the ops: spec.configId is set.
		got, err := cs.Resource(gatewayGVRForTest).Namespace(ns).Get(context.Background(), gw, metav1.GetOptions{})
		require.NoError(t, err)
		cid, _, _ := unstructured.NestedString(got.Object, "spec", "configId")
		assert.Equal(t, "kcp-abc123", cid)
	})

	t.Run("writes no configId and returns empty when none supplied", func(t *testing.T) {
		cs := newFakeDynamicClient(seededGateway(ns, gw, route))
		captured := capturePatch(cs)

		got, err := patchGatewayRoute(context.Background(), cs, ns, gw,
			RoutePatch{RouteName: route, Field: "fence", Value: map[string]any{"scope": "all"}}, "")
		require.NoError(t, err)
		assert.Empty(t, got)
		require.True(t, captured.called)

		var ops []map[string]any
		require.NoError(t, k8sjson.Unmarshal(captured.body, &ops))
		for _, op := range ops {
			assert.NotEqual(t, "/spec/configId", op["path"], "spec.configId must not be patched when no configId is supplied")
		}
	})
}
