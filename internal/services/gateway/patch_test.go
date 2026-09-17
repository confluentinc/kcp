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

	t.Run("surfaces ErrApplyUnverified when the server stores a different configId", func(t *testing.T) {
		cs := newFakeDynamicClient(seededGateway(ns, gw, route))
		// The route mutation succeeds but the CR comes back carrying a configId
		// kcp never sent — a mutating webhook or a conflicting controller. The
		// patch already reached the cluster, so this must be reported as
		// unverified rather than swallowed.
		mutated := seededGateway(ns, gw, route)
		require.NoError(t, unstructured.SetNestedField(mutated.Object, "kcp-someone-else", "spec", "configId"))
		returnPatched(cs, mutated)

		_, err := patchGatewayRoute(context.Background(), cs, ns, gw,
			RoutePatch{RouteName: route, Field: "streamingDomain", Value: "cp-b"}, "kcp-sent")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})
}

// returnPatched forces the fake client's patch to resolve to obj, bypassing the
// default tracker that would otherwise apply the JSON patch faithfully. It
// simulates the API server returning a CR whose stored spec.configId differs
// from what kcp sent (a mutating webhook, a conflicting controller) — the case
// confirmStoredConfigID exists to catch.
func returnPatched(cs *dynamicfake.FakeDynamicClient, obj *unstructured.Unstructured) {
	cs.PrependReactor("patch", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, obj, nil
	})
}

func TestPatchGatewayConfigID(t *testing.T) {
	const ns, gw, route = "confluent", "test-gateway", "migration-route"

	t.Run("emits a single add /spec/configId op with no field manager and no force", func(t *testing.T) {
		cs := newFakeDynamicClient(seededGateway(ns, gw, route))
		captured := capturePatch(cs)

		stored, err := patchGatewayConfigID(context.Background(), cs, ns, gw, "kcp-cfg1")
		require.NoError(t, err)
		assert.Equal(t, "kcp-cfg1", stored)

		require.True(t, captured.called)
		assert.Equal(t, types.JSONPatchType, captured.patchType)
		assert.Empty(t, captured.fieldManager,
			"the hot-reload probe declares no field manager, so it owns nothing it could later prune")
		assert.False(t, captured.force)

		// Touches spec.configId and nothing else — the patch-path analogue of the
		// old SSA ownership check: a JSON Patch this narrow cannot seize or drop
		// any other field.
		var ops []map[string]any
		require.NoError(t, k8sjson.Unmarshal(captured.body, &ops))
		require.Len(t, ops, 1)
		assert.Equal(t, "add", ops[0]["op"])
		assert.Equal(t, "/spec/configId", ops[0]["path"])
		assert.Equal(t, "kcp-cfg1", ops[0]["value"])

		got, err := cs.Resource(gatewayGVRForTest).Namespace(ns).Get(context.Background(), gw, metav1.GetOptions{})
		require.NoError(t, err)
		cid, _, _ := unstructured.NestedString(got.Object, "spec", "configId")
		assert.Equal(t, "kcp-cfg1", cid)
	})

	t.Run("surfaces ErrApplyUnverified when the server drops the configId", func(t *testing.T) {
		cs := newFakeDynamicClient(seededGateway(ns, gw, route))
		returnPatched(cs, seededGateway(ns, gw, route)) // comes back with no spec.configId

		_, err := patchGatewayConfigID(context.Background(), cs, ns, gw, "kcp-cfg1")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})
}

// TestConfirmStoredConfigID restores the read-back verifier coverage lost when
// apply_test.go was deleted with the SSA write path. Every failure branch of
// confirmStoredConfigID must wrap ErrApplyUnverified: the patch has already
// landed by the time it runs, so a caller must never read a confirmation
// failure as "nothing reached the cluster".
func TestConfirmStoredConfigID(t *testing.T) {
	const gw, want = "test-gateway", "kcp-abc123"

	specWith := func(configID any) *unstructured.Unstructured {
		spec := map[string]any{}
		if configID != nil {
			spec["configId"] = configID
		}
		return &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	}

	t.Run("returns the stored configId when it matches", func(t *testing.T) {
		got, err := confirmStoredConfigID(specWith(want), gw, want)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("nil object is unverified", func(t *testing.T) {
		_, err := confirmStoredConfigID(nil, gw, want)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})

	t.Run("dropped configId is unverified", func(t *testing.T) {
		_, err := confirmStoredConfigID(specWith(nil), gw, want)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})

	t.Run("mismatched configId is unverified", func(t *testing.T) {
		_, err := confirmStoredConfigID(specWith("kcp-different"), gw, want)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})

	t.Run("non-string configId is unverified", func(t *testing.T) {
		_, err := confirmStoredConfigID(specWith(123), gw, want)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})
}
