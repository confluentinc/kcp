package gateway

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	// k8sjson converts integral numbers to int64, matching what a real API server
	// response looks like by the time the dynamic client hands it back.
	// encoding/json would yield float64 and misrepresent the object under test.
	k8sjson "k8s.io/apimachinery/pkg/util/json"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

const applyTestCR = `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: test-gateway
  namespace: confluent
spec:
  replicas: 3
`

// appliedObject captures what kcp handed to server-side apply.
type appliedObject struct {
	obj          *unstructured.Unstructured
	patchType    types.PatchType
	fieldManager string
	force        bool
	called       bool
}

// emulateApply installs a reactor that stands in for a compliant API server:
// it records the object kcp applied and echoes it back as the result.
//
// This is necessary because client-go's fake dynamic client cannot service a
// server-side apply on an unstructured object at all — its apply path runs the
// typed strategic-merge machinery and fails with "unable to find api field in
// struct Unstructured for the json field metadata". Do not spend time trying to
// make cs.Resource(...).Apply(...) work against the tracker; emulate the server.
func emulateApply(cs *dynamicfake.FakeDynamicClient) *appliedObject {
	captured := &appliedObject{}
	cs.PrependReactor("patch", "gateways", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction, ok := action.(k8stesting.PatchActionImpl)
		if !ok {
			return false, nil, nil
		}
		captured.called = true
		captured.patchType = patchAction.GetPatchType()
		captured.fieldManager = patchAction.PatchOptions.FieldManager
		captured.force = patchAction.PatchOptions.Force != nil && *patchAction.PatchOptions.Force

		var obj unstructured.Unstructured
		if err := k8sjson.Unmarshal(patchAction.GetPatch(), &obj.Object); err != nil {
			return true, nil, fmt.Errorf("test reactor: %w", err)
		}
		captured.obj = &obj
		return true, &obj, nil
	})
	return captured
}

func TestApplyGatewayYAML(t *testing.T) {
	const ns, gw = "confluent", "test-gateway"

	t.Run("injects the configId and returns what the server stored", func(t *testing.T) {
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		got, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte(applyTestCR), "kcp-abc123")
		require.NoError(t, err)
		assert.Equal(t, "kcp-abc123", got)

		require.True(t, captured.called)
		id, found, err := unstructured.NestedString(captured.obj.Object, "spec", gatewayConfigIDField)
		require.NoError(t, err)
		require.True(t, found, "the applied object must carry spec.configId")
		assert.Equal(t, "kcp-abc123", id)
	})

	t.Run("uses server-side apply, not a merge patch", func(t *testing.T) {
		// Field ownership is what lets kcp own spec.configId for the duration of
		// a migration; a merge patch would not establish it.
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		_, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte(applyTestCR), "kcp-abc123")
		require.NoError(t, err)

		assert.Equal(t, types.ApplyPatchType, captured.patchType)
	})

	t.Run("applies under the migration field manager, forced", func(t *testing.T) {
		// migrationFieldManager is what lets the fence, switchover and unfence
		// CRs each own the fields they declare, in turn — see
		// hotReloadCheckFieldManager for why the hot-reload check must NOT
		// share it.
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		_, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte(applyTestCR), "kcp-abc123")
		require.NoError(t, err)

		assert.Equal(t, migrationFieldManager, captured.fieldManager)
		assert.True(t, captured.force)
	})

	t.Run("writes no configId and returns empty when none is supplied", func(t *testing.T) {
		// The VerifyRollout path: on a pre-hot-reload cluster the field is not in
		// the CRD and server-side apply would reject it outright.
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		got, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte(applyTestCR), "")
		require.NoError(t, err)
		assert.Empty(t, got)

		require.True(t, captured.called)
		_, found, err := unstructured.NestedString(captured.obj.Object, "spec", gatewayConfigIDField)
		require.NoError(t, err)
		assert.False(t, found, "spec.configId must not be sent at all")
	})

	t.Run("preserves the rest of the applied spec", func(t *testing.T) {
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		_, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte(applyTestCR), "kcp-abc123")
		require.NoError(t, err)

		replicas, found, err := unstructured.NestedInt64(captured.obj.Object, "spec", "replicas")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, int64(3), replicas)
	})

	t.Run("forces name and namespace onto the applied object", func(t *testing.T) {
		mismatched := []byte(`
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: wrong-name
  namespace: wrong-namespace
spec:
  replicas: 1
`)
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		_, err := applyGatewayYAML(context.Background(), cs, ns, gw, mismatched, "kcp-abc123")
		require.NoError(t, err)

		assert.Equal(t, gw, captured.obj.GetName())
		assert.Equal(t, ns, captured.obj.GetNamespace())
	})

	t.Run("errors when the server silently drops the configId", func(t *testing.T) {
		// A CRD that does not declare spec.configId would leave every subsequent
		// GET /config poll waiting for a revision that never arrives. Fail here,
		// with the cause, rather than later with a bare timeout.
		cs := newFakeDynamicClient()
		cs.PrependReactor("patch", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": GatewayGroup + "/" + GatewayVersion,
				"kind":       GatewayKind,
				"metadata":   map[string]any{"name": gw, "namespace": ns},
				"spec":       map[string]any{"replicas": int64(3)},
			}}, nil
		})

		_, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte(applyTestCR), "kcp-abc123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configId")
		// The apply itself succeeded and the spec is live — see
		// ErrApplyUnverified — so a caller must not treat this as a no-op.
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})

	t.Run("errors when the server returns a different configId", func(t *testing.T) {
		cs := newFakeDynamicClient()
		cs.PrependReactor("patch", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": GatewayGroup + "/" + GatewayVersion,
				"kind":       GatewayKind,
				"metadata":   map[string]any{"name": gw, "namespace": ns},
				"spec":       map[string]any{gatewayConfigIDField: "something-else"},
			}}, nil
		})

		_, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte(applyTestCR), "kcp-abc123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configId")
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})

	t.Run("does not require a readback when no configId was injected", func(t *testing.T) {
		// The rollout path must stay working against servers whose response
		// carries no configId, since that is the normal case there.
		cs := newFakeDynamicClient()
		cs.PrependReactor("patch", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": GatewayGroup + "/" + GatewayVersion,
				"kind":       GatewayKind,
				"metadata":   map[string]any{"name": gw, "namespace": ns},
				"spec":       map[string]any{"replicas": int64(3)},
			}}, nil
		})

		got, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte(applyTestCR), "")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("propagates an apply failure", func(t *testing.T) {
		cs := newFakeDynamicClient()
		cs.PrependReactor("patch", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("admission webhook denied the request")
		})

		_, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte(applyTestCR), "kcp-abc123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "admission webhook denied the request")
		// The apply never reached the cluster — nothing to undo, unlike the
		// read-back guards above.
		assert.NotErrorIs(t, err, ErrApplyUnverified)
	})

	t.Run("rejects an invalid configId without contacting the cluster", func(t *testing.T) {
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		_, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte(applyTestCR), "not+valid")
		require.Error(t, err)
		assert.False(t, captured.called, "an invalid configId must fail before the apply")
	})

	t.Run("rejects unparseable YAML without contacting the cluster", func(t *testing.T) {
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		_, err := applyGatewayYAML(context.Background(), cs, ns, gw, []byte("\tnot: [valid"), "kcp-abc123")
		require.Error(t, err)
		assert.False(t, captured.called)
	})
}

func TestApplyGatewayConfigID(t *testing.T) {
	const ns, gw = "confluent", "test-gateway"

	t.Run("applies spec.configId alone, under the hot-reload check field manager", func(t *testing.T) {
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		got, err := applyGatewayConfigID(context.Background(), cs, ns, gw, "kcp-abc123")
		require.NoError(t, err)
		assert.Equal(t, "kcp-abc123", got)

		require.True(t, captured.called)
		assert.Equal(t, types.ApplyPatchType, captured.patchType)
		assert.Equal(t, hotReloadCheckFieldManager, captured.fieldManager)
		assert.NotEqual(t, migrationFieldManager, captured.fieldManager,
			"must never share the manager the fence/switchover/unfence CRs apply under — see hotReloadCheckFieldManager")
		assert.True(t, captured.force)

		// Nothing else must be declared: a fence, switchover or unfence CR that
		// omits any of these fields must have nothing to prune.
		assert.Equal(t, map[string]any{gatewayConfigIDField: "kcp-abc123"}, captured.obj.Object["spec"])
		metadata, ok := captured.obj.Object["metadata"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, map[string]any{"name": gw, "namespace": ns}, metadata)
	})

	t.Run("sets the requested name and namespace", func(t *testing.T) {
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		_, err := applyGatewayConfigID(context.Background(), cs, ns, gw, "kcp-abc123")
		require.NoError(t, err)

		assert.Equal(t, gw, captured.obj.GetName())
		assert.Equal(t, ns, captured.obj.GetNamespace())
	})

	t.Run("errors, wrapped in ErrApplyUnverified, when the server drops the configId", func(t *testing.T) {
		cs := newFakeDynamicClient()
		cs.PrependReactor("patch", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": GatewayGroup + "/" + GatewayVersion,
				"kind":       GatewayKind,
				"metadata":   map[string]any{"name": gw, "namespace": ns},
				"spec":       map[string]any{},
			}}, nil
		})

		_, err := applyGatewayConfigID(context.Background(), cs, ns, gw, "kcp-abc123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configId")
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})

	t.Run("errors, wrapped in ErrApplyUnverified, when the server returns a different configId", func(t *testing.T) {
		cs := newFakeDynamicClient()
		cs.PrependReactor("patch", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": GatewayGroup + "/" + GatewayVersion,
				"kind":       GatewayKind,
				"metadata":   map[string]any{"name": gw, "namespace": ns},
				"spec":       map[string]any{gatewayConfigIDField: "something-else"},
			}}, nil
		})

		_, err := applyGatewayConfigID(context.Background(), cs, ns, gw, "kcp-abc123")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})

	t.Run("propagates an apply failure, not wrapped in ErrApplyUnverified", func(t *testing.T) {
		cs := newFakeDynamicClient()
		cs.PrependReactor("patch", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("admission webhook denied the request")
		})

		_, err := applyGatewayConfigID(context.Background(), cs, ns, gw, "kcp-abc123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "admission webhook denied the request")
		assert.NotErrorIs(t, err, ErrApplyUnverified)
	})

	t.Run("rejects an invalid configId without contacting the cluster", func(t *testing.T) {
		cs := newFakeDynamicClient()
		captured := emulateApply(cs)

		_, err := applyGatewayConfigID(context.Background(), cs, ns, gw, "not+valid")
		require.Error(t, err)
		assert.False(t, captured.called, "an invalid configId must fail before the apply")
	})
}

// TestConfirmStoredConfigID pins the one case the two ApplyGateway* callers
// cannot themselves construct: the API server returning no object at all
// alongside a nil error. Reachable only against a non-compliant client, but
// kept so the guard cannot silently regress into a nil-pointer panic.
func TestConfirmStoredConfigID(t *testing.T) {
	t.Run("errors when the server returns no object", func(t *testing.T) {
		_, err := confirmStoredConfigID(nil, "test-gateway", "kcp-abc123")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrApplyUnverified)
	})
}
