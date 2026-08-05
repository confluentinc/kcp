package gateway

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

// ===========================================================================
// fixtures
// ===========================================================================

// hotReloadCRYAML mirrors the live rig's gateway: the shape the hot-reload read
// has to navigate, status and all, not a minimal two-key document.
const hotReloadCRYAML = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway-hotreload
  namespace: confluent
  generation: 14
spec:
  replicas: 3
  hotReload:
    enabled: true
  image:
    application: confluentinc/confluent-gateway-for-cloud:1.3.0
  streamingDomains:
    - name: sd-1
      nodeIdRanges:
        - start: 0
          end: 2
  routes:
    - name: route-1
      streamingDomain: sd-1
status:
  conditions:
    - type: platform.confluent.io/cluster-ready
      status: "True"
      reason: Created
      lastTransitionTime: "2026-07-28T15:14:46Z"
`

// coldCRYAML is the Tier C shape: no hotReload block at all, which is what
// every gateway template in this repo currently produces.
const coldCRYAML = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
  namespace: kcp
spec:
  replicas: 3
  routes:
    - name: route-1
      streamingDomain: sd-1
`

// candidateCRYAML stands in for a user-supplied fenced CR: no server-managed
// metadata, since those files are applied byte-for-byte.
const candidateCRYAML = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  replicas: 3
  hotReload:
    enabled: true
  routes:
    - name: route-1
      streamingDomain: sd-1
      fence:
        scope: ALL
        errorCode: BROKER_NOT_AVAILABLE
`

// ===========================================================================
// apply recorder
// ===========================================================================

// applyRecorder intercepts server-side-apply patches against the Gateway GVR so
// tests control the read-back exactly. The question that decides the tier — does
// this CRD keep spec.configId or silently prune it — is a property of the
// cluster's CRD schema, which no fake can reproduce, so it is scripted here.
type applyRecorder struct {
	mu      sync.Mutex
	actions []ktesting.PatchActionImpl
	answer  func(nth int, patch []byte) (*unstructured.Unstructured, error)
}

func (r *applyRecorder) install(cs *dynamicfake.FakeDynamicClient) {
	cs.PrependReactor("patch", GatewayResourcePlural, func(action ktesting.Action) (bool, runtime.Object, error) {
		patchAction, ok := action.(ktesting.PatchActionImpl)
		if !ok {
			return false, nil, nil
		}
		r.mu.Lock()
		r.actions = append(r.actions, patchAction)
		nth := len(r.actions)
		r.mu.Unlock()

		obj, err := r.answer(nth, patchAction.Patch)
		if obj == nil {
			// Returning a typed nil here would hand the fake a non-nil
			// runtime.Object wrapping a nil pointer, which it dereferences —
			// something no real client can produce.
			return true, nil, err
		}
		return true, obj, err
	})
}

func (r *applyRecorder) calls() []ktesting.PatchActionImpl {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ktesting.PatchActionImpl(nil), r.actions...)
}

// echoPatch answers an apply by returning the patched object verbatim — a CRD
// that declares spec.configId and therefore keeps it.
func echoPatch(t *testing.T, patch []byte) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	require.NoError(t, obj.UnmarshalJSON(patch))
	return obj
}

// prunePatch answers an apply by returning the patched object with spec.configId
// stripped — a structural CRD schema that does not declare the field.
func prunePatch(t *testing.T, patch []byte) *unstructured.Unstructured {
	t.Helper()
	obj := echoPatch(t, patch)
	unstructured.RemoveNestedField(obj.Object, "spec", "configId")
	return obj
}

// newApplyProbeClient returns a fake dynamic client with the recorder installed
// and one seeded Gateway, matching the live precondition (the CR exists).
func newApplyProbeClient(rec *applyRecorder) *dynamicfake.FakeDynamicClient {
	cs := newFakeDynamicClient(newGatewayCR(testGateway, testNamespace, 1, 1, true))
	rec.install(cs)
	return cs
}

// ===========================================================================
// HotReloadEnabled tests (pure logic)
// ===========================================================================

func TestHotReloadEnabled_True(t *testing.T) {
	enabled, err := HotReloadEnabled([]byte(hotReloadCRYAML))
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestHotReloadEnabled_ExplicitFalse(t *testing.T) {
	cr := strings.Replace(hotReloadCRYAML, "enabled: true", "enabled: false", 1)
	enabled, err := HotReloadEnabled([]byte(cr))
	require.NoError(t, err)
	assert.False(t, enabled)
}

// The overwhelmingly common shape: no hotReload block at all. This must be a
// clean false, not an error, or every existing gateway fails to classify.
func TestHotReloadEnabled_BlockAbsent(t *testing.T) {
	enabled, err := HotReloadEnabled([]byte(coldCRYAML))
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestHotReloadEnabled_BlockPresentButEmpty(t *testing.T) {
	cr := `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  hotReload: {}
`
	enabled, err := HotReloadEnabled([]byte(cr))
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestHotReloadEnabled_NoSpec(t *testing.T) {
	cr := `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
`
	_, err := HotReloadEnabled([]byte(cr))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec")
}

// A non-bool enabled is ambiguous rather than false, and the CRD would reject
// it, so it must not be silently read as "hot reload off" — that would inject a
// configId decision on a guess.
func TestHotReloadEnabled_NonBoolEnabled(t *testing.T) {
	cr := `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  hotReload:
    enabled: "true"
`
	_, err := HotReloadEnabled([]byte(cr))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hotReload.enabled")
}

func TestHotReloadEnabled_MalformedYAML(t *testing.T) {
	_, err := HotReloadEnabled([]byte("spec:\n\tnot: [valid"))
	require.Error(t, err)
}

func TestHotReloadEnabled_EmptyInput(t *testing.T) {
	_, err := HotReloadEnabled(nil)
	require.Error(t, err)
}

// hotReload is not a scalar in any real CR, but a scalar there must not panic
// the type assertion.
func TestHotReloadEnabled_BlockIsScalar(t *testing.T) {
	cr := "spec:\n  hotReload: true\n"
	_, err := HotReloadEnabled([]byte(cr))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hotReload")
}

// ===========================================================================
// ResolveVerificationTier (pure logic)
//
// This replaced an SSA dry-run probe that asked the cluster, before the apply,
// whether it would keep a spec.configId. The probe cost a round trip per apply
// and could not be authoritative about anything the apply response does not
// already say — pruning is a property of the CRD schema, and the response is
// where it shows up. What genuinely had to be decided beforehand is only whether
// to stamp at all, and that is the hot-reload flag alone.
// ===========================================================================

func TestResolveVerificationTier_HotReloadAndConfigIDKept(t *testing.T) {
	assert.Equal(t, TierPerPodConfigID, ResolveVerificationTier(true, "kcp-1-a", "kcp-1-a"))
}

func TestResolveVerificationTier_HotReloadButConfigIDPruned(t *testing.T) {
	// The public-latest CFK window: hot reload works, the CRD does not declare
	// configId, so the field silently vanishes and no per-pod handle exists.
	assert.Equal(t, TierHotReloadOnly, ResolveVerificationTier(true, "kcp-1-a", ""))
}

func TestResolveVerificationTier_HotReloadButConfigIDAltered(t *testing.T) {
	// Not a usable handle: /config could never confirm a revision the server did
	// not store under the name we asked for.
	assert.Equal(t, TierHotReloadOnly, ResolveVerificationTier(true, "kcp-1-a", "something-else"))
}

// The E7 guard, at the level that decides it: with hot reload off nothing is
// stamped, so there is no id to compare and the rollout wait is the real gate.
func TestResolveVerificationTier_NoHotReloadIsAlwaysRollout(t *testing.T) {
	assert.Equal(t, TierPodRollout, ResolveVerificationTier(false, "", ""))

	// Even if a configId somehow round-tripped, hot reload off means the pods
	// rolled — the rollout wait is what proves that, not /config.
	assert.Equal(t, TierPodRollout, ResolveVerificationTier(false, "kcp-1-a", "kcp-1-a"))
}

// A gateway that hot-reloads but was never stamped cannot be verified per pod:
// every pod would report an id that matches nothing we asked for.
func TestResolveVerificationTier_HotReloadWithNoStampedID(t *testing.T) {
	assert.Equal(t, TierHotReloadOnly, ResolveVerificationTier(true, "", ""))
	assert.Equal(t, TierHotReloadOnly, ResolveVerificationTier(true, "", "kcp-leftover"),
		"an id we did not send proves nothing about this apply")
}

func TestVerificationTier_WireValues(t *testing.T) {
	assert.Equal(t, "per-pod-configid", string(TierPerPodConfigID))
	assert.Equal(t, "hot-reload-only", string(TierHotReloadOnly))
	assert.Equal(t, "pod-rollout", string(TierPodRollout))
}

// ===========================================================================
// applyGatewayYAML configId read-back
//
// The read-back is what selects the verification strategy, so it needs the same
// coverage the dry-run probe used to have. Whether a CRD keeps or prunes an
// undeclared field is a property no fake reproduces, so it is scripted.
// ===========================================================================

func TestApplyGatewayYAML_ReturnsStoredConfigID(t *testing.T) {
	rec := &applyRecorder{answer: func(_ int, patch []byte) (*unstructured.Unstructured, error) {
		return echoPatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	stamped, err := injectConfigID([]byte(candidateCRYAML), "kcp-123-abc")
	require.NoError(t, err)

	stored, err := applyGatewayYAML(context.Background(), cs, testNamespace, testGateway, stamped)
	require.NoError(t, err)
	assert.Equal(t, "kcp-123-abc", stored)
}

func TestApplyGatewayYAML_PrunedConfigIDComesBackEmpty(t *testing.T) {
	rec := &applyRecorder{answer: func(_ int, patch []byte) (*unstructured.Unstructured, error) {
		return prunePatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	stamped, err := injectConfigID([]byte(candidateCRYAML), "kcp-123-abc")
	require.NoError(t, err)

	stored, err := applyGatewayYAML(context.Background(), cs, testNamespace, testGateway, stamped)
	require.NoError(t, err, "a pruned field is inert, not an apply failure")
	assert.Empty(t, stored, "a pruned configId must not be reported as stored")
}

// An unstamped apply is the hot-reload-off path, and it must not invent an id.
func TestApplyGatewayYAML_UnstampedCRReturnsEmpty(t *testing.T) {
	rec := &applyRecorder{answer: func(_ int, patch []byte) (*unstructured.Unstructured, error) {
		return echoPatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	stored, err := applyGatewayYAML(context.Background(), cs, testNamespace, testGateway, []byte(coldCRYAML))
	require.NoError(t, err)
	assert.Empty(t, stored)
}

// A non-string configId is as unusable as an absent one, and must not fail an
// apply that in fact succeeded.
func TestApplyGatewayYAML_NonStringConfigIDReturnsEmpty(t *testing.T) {
	rec := &applyRecorder{answer: func(_ int, patch []byte) (*unstructured.Unstructured, error) {
		obj := echoPatch(t, patch)
		require.NoError(t, unstructured.SetNestedField(obj.Object, int64(42), "spec", "configId"))
		return obj, nil
	}}
	cs := newApplyProbeClient(rec)

	stamped, err := injectConfigID([]byte(candidateCRYAML), "kcp-123-abc")
	require.NoError(t, err)

	stored, err := applyGatewayYAML(context.Background(), cs, testNamespace, testGateway, stamped)
	require.NoError(t, err)
	assert.Empty(t, stored)
}

func TestApplyGatewayYAML_ForcesNameAndNamespace(t *testing.T) {
	rec := &applyRecorder{answer: func(_ int, patch []byte) (*unstructured.Unstructured, error) {
		return echoPatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	// candidateCRYAML carries no metadata at all, which is how the user-supplied
	// fenced and switchover files come.
	_, err := applyGatewayYAML(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.NoError(t, err)

	calls := rec.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, testGateway, calls[0].Name)
	assert.Equal(t, testNamespace, calls[0].Namespace)
}

func TestApplyGatewayYAML_MalformedYAML(t *testing.T) {
	rec := &applyRecorder{answer: func(_ int, patch []byte) (*unstructured.Unstructured, error) {
		return echoPatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	_, err := applyGatewayYAML(context.Background(), cs, testNamespace, testGateway, []byte("\tnot: [valid"))
	require.Error(t, err)
	assert.Empty(t, rec.calls(), "a document that will not parse must not reach the cluster")
}

func TestApplyGatewayYAML_ApplyErrorPropagates(t *testing.T) {
	rec := &applyRecorder{answer: func(_ int, _ []byte) (*unstructured.Unstructured, error) {
		return nil, errors.New("admission webhook denied the request")
	}}
	cs := newApplyProbeClient(rec)

	stored, err := applyGatewayYAML(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admission webhook denied the request")
	assert.Empty(t, stored)
}
