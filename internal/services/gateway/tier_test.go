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
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

// ===========================================================================
// fixtures
// ===========================================================================

// hotReloadCRYAML mirrors the live rig's gateway: the shape the tier probe has
// to navigate, status and all, not a minimal two-key document.
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
// dry-run apply recorder
// ===========================================================================

// applyRecorder intercepts server-side-apply patches against the Gateway GVR so
// tests control the read-back exactly. The real question the probe asks — does
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

// echoPatch answers an apply by returning the patched object verbatim — the
// Tier A behaviour, where the API server keeps spec.configId.
func echoPatch(t *testing.T, patch []byte) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	require.NoError(t, obj.UnmarshalJSON(patch))
	return obj
}

// prunePatch answers an apply by returning the patched object with
// spec.configId stripped — the Tier B behaviour of a structural CRD schema
// that does not declare the field.
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
// hotReloadEnabled tests (pure logic)
// ===========================================================================

func TestHotReloadEnabled_True(t *testing.T) {
	enabled, err := hotReloadEnabled([]byte(hotReloadCRYAML))
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestHotReloadEnabled_ExplicitFalse(t *testing.T) {
	cr := strings.Replace(hotReloadCRYAML, "enabled: true", "enabled: false", 1)
	enabled, err := hotReloadEnabled([]byte(cr))
	require.NoError(t, err)
	assert.False(t, enabled)
}

// The overwhelmingly common shape: no hotReload block at all. This must be a
// clean false, not an error, or every existing gateway fails tier detection.
func TestHotReloadEnabled_BlockAbsent(t *testing.T) {
	enabled, err := hotReloadEnabled([]byte(coldCRYAML))
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestHotReloadEnabled_BlockPresentButEmpty(t *testing.T) {
	cr := `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  hotReload: {}
`
	enabled, err := hotReloadEnabled([]byte(cr))
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestHotReloadEnabled_NoSpec(t *testing.T) {
	cr := `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: migration-gateway
`
	_, err := hotReloadEnabled([]byte(cr))
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
	_, err := hotReloadEnabled([]byte(cr))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hotReload.enabled")
}

func TestHotReloadEnabled_MalformedYAML(t *testing.T) {
	_, err := hotReloadEnabled([]byte("spec:\n\tnot: [valid"))
	require.Error(t, err)
}

func TestHotReloadEnabled_EmptyInput(t *testing.T) {
	_, err := hotReloadEnabled(nil)
	require.Error(t, err)
}

// hotReload is not a scalar in any real CR, but a scalar there must not panic
// the type assertion.
func TestHotReloadEnabled_BlockIsScalar(t *testing.T) {
	cr := "spec:\n  hotReload: true\n"
	_, err := hotReloadEnabled([]byte(cr))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hotReload")
}

// ===========================================================================
// dryRunSupportsConfigID tests
// ===========================================================================

func TestDryRunSupportsConfigID_Echoed(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return echoPatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	supported, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.NoError(t, err)
	assert.True(t, supported)
	assert.Len(t, rec.calls(), 1, "a successful probe must not need a second apply")
}

func TestDryRunSupportsConfigID_Pruned(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return prunePatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	supported, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.NoError(t, err)
	assert.False(t, supported)
}

// The probe must never mutate the cluster. Non-persistence is an API-server
// guarantee of dryRun=All (verified live), so what is checkable here is that we
// actually ask for it — along with the same field manager the real apply uses,
// so ownership conflicts surface at probe time rather than at fence time.
func TestDryRunSupportsConfigID_UsesDryRunApplyOptions(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return echoPatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	_, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.NoError(t, err)

	calls := rec.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, types.ApplyPatchType, calls[0].PatchType)
	assert.Equal(t, []string{"All"}, calls[0].PatchOptions.DryRun)
	assert.Equal(t, gatewayFieldManager, calls[0].PatchOptions.FieldManager)
	require.NotNil(t, calls[0].PatchOptions.Force)
	assert.True(t, *calls[0].PatchOptions.Force)
}

// The id sent to the cluster must satisfy the CRD's own constraints, or a
// pruned-vs-rejected verdict is really just a verdict on a malformed id.
func TestDryRunSupportsConfigID_ProbeIDIsCRDValid(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return echoPatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	_, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.NoError(t, err)

	calls := rec.calls()
	require.Len(t, calls, 1)
	sent := echoPatch(t, calls[0].Patch)
	id, found, err := unstructured.NestedString(sent.Object, "spec", "configId")
	require.NoError(t, err)
	require.True(t, found, "the probe must actually carry a configId")
	assert.True(t, strings.HasPrefix(id, configIDPrefix), "probe id should be attributable to kcp: %q", id)
	assert.LessOrEqual(t, len(id), maxConfigIDLength)
	assert.Regexp(t, configIDPattern, id)
}

// The unvalidated live case: a CRD that rejects the unknown field rather than
// pruning it. Re-applying without configId succeeds, which attributes the
// failure to configId alone and yields Tier B rather than a hard error.
func TestDryRunSupportsConfigID_RejectedButBaseCRValid(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		if nth == 1 {
			return nil, errors.New(`Gateway in version "v1beta1" cannot be handled: strict decoding error: unknown field "spec.configId"`)
		}
		return echoPatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	supported, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.NoError(t, err)
	assert.False(t, supported)

	calls := rec.calls()
	require.Len(t, calls, 2, "a rejection must be re-tested without configId")

	// The control apply must carry no configId, or it proves nothing.
	control := echoPatch(t, calls[1].Patch)
	_, found, err := unstructured.NestedString(control.Object, "spec", "configId")
	require.NoError(t, err)
	assert.False(t, found)
}

// When the CR is rejected with and without configId, the CR itself is the
// problem: the real apply is going to fail too, so surface it now rather than
// mislabelling the cluster as Tier B.
func TestDryRunSupportsConfigID_BaseCRAlsoInvalid(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return nil, errors.New("admission webhook denied the request: streamingDomain not found")
	}}
	cs := newApplyProbeClient(rec)

	_, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "streamingDomain not found")
	assert.Len(t, rec.calls(), 2)
}

// A field that comes back holding something other than what we sent is not a
// usable per-pod handle, whatever the reason.
func TestDryRunSupportsConfigID_EchoedDifferentValue(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		obj := echoPatch(t, patch)
		require.NoError(t, unstructured.SetNestedField(obj.Object, "someone-elses-id", "spec", "configId"))
		return obj, nil
	}}
	cs := newApplyProbeClient(rec)

	supported, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.NoError(t, err)
	assert.False(t, supported)
}

// A non-string configId in the read-back is not a handle we can compare.
func TestDryRunSupportsConfigID_EchoedNonString(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		obj := echoPatch(t, patch)
		require.NoError(t, unstructured.SetNestedField(obj.Object, int64(42), "spec", "configId"))
		return obj, nil
	}}
	cs := newApplyProbeClient(rec)

	supported, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.NoError(t, err)
	assert.False(t, supported)
}

// An apply that somehow returns no object cannot be read back, so it cannot
// claim support.
func TestDryRunSupportsConfigID_NilResponse(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return nil, nil
	}}
	cs := newApplyProbeClient(rec)

	supported, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.Error(t, err)
	assert.False(t, supported)
}

func TestDryRunSupportsConfigID_MalformedCRYAML(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		t.Fatal("must not reach the cluster with an unparseable CR")
		return nil, nil
	}}
	cs := newApplyProbeClient(rec)

	_, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte("spec: [unterminated"))
	require.Error(t, err)
	assert.Empty(t, rec.calls())
}

// Mirrors ApplyGatewayYAML: the requested name and namespace win over whatever
// the file says, so a probe cannot be aimed at the wrong object.
func TestDryRunSupportsConfigID_ForcesNameAndNamespace(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return echoPatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	cr := strings.Replace(candidateCRYAML, "kind: Gateway\n",
		"kind: Gateway\nmetadata:\n  name: wrong-name\n  namespace: wrong-ns\n", 1)

	_, err := dryRunSupportsConfigID(context.Background(), cs, testNamespace, testGateway, []byte(cr))
	require.NoError(t, err)

	calls := rec.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, testGateway, calls[0].Name)
	assert.Equal(t, testNamespace, calls[0].Namespace)

	sent := echoPatch(t, calls[0].Patch)
	assert.Equal(t, testGateway, sent.GetName())
	assert.Equal(t, testNamespace, sent.GetNamespace())
}

// A cancelled probe must not be re-run as a CR validation check: the retry
// would fail for the same reason and report a perfectly good CR as invalid.
func TestDryRunSupportsConfigID_CancellationIsNotBlamedOnTheCR(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return nil, context.Canceled
	}}
	cs := newApplyProbeClient(rec)

	_, err := dryRunSupportsConfigID(ctx, cs, testNamespace, testGateway, []byte(candidateCRYAML))
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, err.Error(), "failed a dry-run apply")
	assert.Len(t, rec.calls(), 1, "a cancelled probe must not be retried")
}

// ===========================================================================
// detectGatewayVerificationTier tests
// ===========================================================================

func TestDetectTier_A_HotReloadAndConfigID(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return echoPatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	tier, err := detectGatewayVerificationTier(context.Background(), cs, testNamespace, testGateway,
		[]byte(hotReloadCRYAML), []byte(candidateCRYAML))
	require.NoError(t, err)
	assert.Equal(t, TierPerPodConfigID, tier)
}

func TestDetectTier_B_HotReloadWithoutConfigID(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return prunePatch(t, patch), nil
	}}
	cs := newApplyProbeClient(rec)

	tier, err := detectGatewayVerificationTier(context.Background(), cs, testNamespace, testGateway,
		[]byte(hotReloadCRYAML), []byte(candidateCRYAML))
	require.NoError(t, err)
	assert.Equal(t, TierHotReloadOnly, tier)
}

// Tier C must cost nothing and touch nothing: with hot reload off, a configId
// bump rolls every gateway pod, so the probe is not merely useless, it must not
// happen.
func TestDetectTier_C_NoHotReloadSkipsProbe(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		t.Fatal("Tier C must not probe for configId support")
		return nil, nil
	}}
	cs := newApplyProbeClient(rec)

	tier, err := detectGatewayVerificationTier(context.Background(), cs, testNamespace, testGateway,
		[]byte(coldCRYAML), []byte(candidateCRYAML))
	require.NoError(t, err)
	assert.Equal(t, TierPodRollout, tier)
	assert.Empty(t, rec.calls())
}

// Corollary of the short circuit: on Tier C the candidate CR is never parsed by
// detection at all, so a candidate kcp cannot inject into is not a detection
// failure.
func TestDetectTier_C_IgnoresUnparseableCandidate(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		t.Fatal("Tier C must not probe for configId support")
		return nil, nil
	}}
	cs := newApplyProbeClient(rec)

	tier, err := detectGatewayVerificationTier(context.Background(), cs, testNamespace, testGateway,
		[]byte(coldCRYAML), []byte("spec: [unterminated"))
	require.NoError(t, err)
	assert.Equal(t, TierPodRollout, tier)
}

// An unreadable hot-reload flag leaves us not knowing whether injection would
// roll the pods, so the fallback is the tier that never injects.
func TestDetectTier_UnreadableHotReloadFallsBackToC(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		t.Fatal("must not probe when the initial CR could not be read")
		return nil, nil
	}}
	cs := newApplyProbeClient(rec)

	tier, err := detectGatewayVerificationTier(context.Background(), cs, testNamespace, testGateway,
		[]byte("spec: [unterminated"), []byte(candidateCRYAML))
	require.Error(t, err)
	assert.Equal(t, TierPodRollout, tier)
}

// Once hot reload is known to be on, a failed probe must never drop to Tier C:
// C's rollout wait reports success when no rollout happens, which is exactly
// the false positive hot reload creates. B is blind but honest about it.
func TestDetectTier_ProbeFailureFallsBackToB(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		return nil, errors.New("connection reset by peer")
	}}
	cs := newApplyProbeClient(rec)

	tier, err := detectGatewayVerificationTier(context.Background(), cs, testNamespace, testGateway,
		[]byte(hotReloadCRYAML), []byte(candidateCRYAML))
	require.Error(t, err)
	assert.Equal(t, TierHotReloadOnly, tier)
}

func TestDetectTier_ExplicitFalseHotReloadIsTierC(t *testing.T) {
	rec := &applyRecorder{answer: func(nth int, patch []byte) (*unstructured.Unstructured, error) {
		t.Fatal("Tier C must not probe for configId support")
		return nil, nil
	}}
	cs := newApplyProbeClient(rec)

	cr := strings.Replace(hotReloadCRYAML, "enabled: true", "enabled: false", 1)
	tier, err := detectGatewayVerificationTier(context.Background(), cs, testNamespace, testGateway,
		[]byte(cr), []byte(candidateCRYAML))
	require.NoError(t, err)
	assert.Equal(t, TierPodRollout, tier)
}

// ===========================================================================
// tier semantics
// ===========================================================================

// The one property the rest of the wiring depends on, stated once: only Tier A
// may stamp a configId.
func TestVerificationTier_OnlyTierAInjectsConfigID(t *testing.T) {
	assert.True(t, TierPerPodConfigID.InjectsConfigID())
	assert.False(t, TierHotReloadOnly.InjectsConfigID())
	assert.False(t, TierPodRollout.InjectsConfigID())
	assert.False(t, VerificationTier("").InjectsConfigID(), "an unset tier must not inject")
}

// Tiers reach state files and log lines, so their wire values are fixed.
func TestVerificationTier_WireValues(t *testing.T) {
	assert.Equal(t, "per-pod-configid", string(TierPerPodConfigID))
	assert.Equal(t, "hot-reload-only", string(TierHotReloadOnly))
	assert.Equal(t, "pod-rollout", string(TierPodRollout))
}
