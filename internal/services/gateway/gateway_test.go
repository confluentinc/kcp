package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// ===========================================================================
// gatewayReadinessAtOrAfter tests (pure logic)
// ===========================================================================

func TestGatewayReadinessAtOrAfter_ReadyAtCapturedGeneration(t *testing.T) {
	cr := newGatewayCR(5, withObservedGeneration(5), withReadyCondition("True"))
	assert.True(t, gatewayReadinessAtOrAfter(cr, 5))
}

func TestGatewayReadinessAtOrAfter_ReadyAheadOfCapturedGeneration(t *testing.T) {
	cr := newGatewayCR(5, withObservedGeneration(7), withReadyCondition("True"))
	assert.True(t, gatewayReadinessAtOrAfter(cr, 5))
}

func TestGatewayReadinessAtOrAfter_ObservedGenerationBehind(t *testing.T) {
	cr := newGatewayCR(5, withObservedGeneration(4), withReadyCondition("True"))
	assert.False(t, gatewayReadinessAtOrAfter(cr, 5), "Ready=True at stale observedGeneration is not enough")
}

func TestGatewayReadinessAtOrAfter_ReadyFalse(t *testing.T) {
	cr := newGatewayCR(5, withObservedGeneration(5), withReadyCondition("False"))
	assert.False(t, gatewayReadinessAtOrAfter(cr, 5))
}

func TestGatewayReadinessAtOrAfter_NoReadyCondition(t *testing.T) {
	cr := newGatewayCR(5, withObservedGeneration(5))
	assert.False(t, gatewayReadinessAtOrAfter(cr, 5), "absent Ready condition is not ready")
}

func TestGatewayReadinessAtOrAfter_ObservedGenerationMissingFallback(t *testing.T) {
	cr := newGatewayCR(5, withReadyCondition("True"))
	assert.True(t, gatewayReadinessAtOrAfter(cr, 5), "fallback: Ready=True with no observedGeneration is treated as ready")
}

func TestGatewayReadinessAtOrAfter_NilCR(t *testing.T) {
	assert.False(t, gatewayReadinessAtOrAfter(nil, 5))
}

// ===========================================================================
// waitForGatewayReady — happy paths
// ===========================================================================

func TestWaitForGatewayReady_DetectionPhase_NoRollout_ReturnsNoOp(t *testing.T) {
	// CR is already at observedGeneration==generation with Ready=True for the
	// entire detection window — should report RolloutDetected=false and return.
	shortenDetectionWindow(t, 50*time.Millisecond)

	cr := newGatewayCR(3, withObservedGeneration(3), withReadyCondition("True"))
	dyn, _ := newFakeDynamicClient(cr)
	cs := newFakePodClient(makePod("gw-pod-1", "test-ns", "test-gw", true))

	var progressCalls []GatewayReadinessProgress
	progressMu := &sync.Mutex{}
	onProgress := func(p GatewayReadinessProgress) {
		progressMu.Lock()
		defer progressMu.Unlock()
		progressCalls = append(progressCalls, p)
	}

	err := waitForGatewayReady(context.Background(), dyn, cs, "test-ns", "test-gw", 10*time.Millisecond, 0, onProgress)
	require.NoError(t, err)

	progressMu.Lock()
	defer progressMu.Unlock()
	require.Len(t, progressCalls, 1, "no-op should fire onProgress exactly once")
	assert.False(t, progressCalls[0].RolloutDetected)
	assert.True(t, progressCalls[0].Ready)
	assert.Equal(t, 1, progressCalls[0].InitialPodCount)
}

func TestWaitForGatewayReady_RolloutThenReady_ReturnsNil(t *testing.T) {
	// CR starts with observedGeneration<generation (rollout in progress); a
	// background goroutine flips it to ready after a short delay.
	shortenDetectionWindow(t, 30*time.Millisecond)

	initial := newGatewayCR(7, withObservedGeneration(6), withReadyCondition("False"))
	dyn, tracker := newFakeDynamicClient(initial)
	cs := newFakePodClient(makePod("gw-pod-1", "test-ns", "test-gw", true))

	// Simulate operator finishing reconciliation after a few polls.
	go func() {
		time.Sleep(80 * time.Millisecond)
		updated := newGatewayCR(7, withObservedGeneration(7), withReadyCondition("True"))
		_ = tracker.Update(gatewayGVR(), updated, "test-ns")
	}()

	var progressCalls []GatewayReadinessProgress
	progressMu := &sync.Mutex{}
	onProgress := func(p GatewayReadinessProgress) {
		progressMu.Lock()
		defer progressMu.Unlock()
		progressCalls = append(progressCalls, p)
	}

	err := waitForGatewayReady(context.Background(), dyn, cs, "test-ns", "test-gw", 15*time.Millisecond, 5*time.Second, onProgress)
	require.NoError(t, err)

	progressMu.Lock()
	defer progressMu.Unlock()
	require.NotEmpty(t, progressCalls)
	final := progressCalls[len(progressCalls)-1]
	assert.True(t, final.RolloutDetected, "rollout should have been detected")
	assert.True(t, final.Ready, "final progress should reflect Ready=true")
	assert.Greater(t, final.Elapsed, time.Duration(0))
}

func TestWaitForGatewayReady_NoDeadline_RunsUntilReady(t *testing.T) {
	// timeout=0 means no deadline. Simulate a slow rollout (~300ms) and assert
	// we wait it out instead of failing.
	shortenDetectionWindow(t, 20*time.Millisecond)

	initial := newGatewayCR(2, withObservedGeneration(1), withReadyCondition("False"))
	dyn, tracker := newFakeDynamicClient(initial)
	cs := newFakePodClient()

	go func() {
		time.Sleep(300 * time.Millisecond)
		updated := newGatewayCR(2, withObservedGeneration(2), withReadyCondition("True"))
		_ = tracker.Update(gatewayGVR(), updated, "test-ns")
	}()

	start := time.Now()
	err := waitForGatewayReady(context.Background(), dyn, cs, "test-ns", "test-gw", 20*time.Millisecond, 0, nil)
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 300*time.Millisecond, "wait should have run for at least the rollout duration")
}

// ===========================================================================
// waitForGatewayReady — timeout and cancellation
// ===========================================================================

func TestWaitForGatewayReady_TimeoutExceeded_ReturnsDeadlineExceeded(t *testing.T) {
	shortenDetectionWindow(t, 20*time.Millisecond)

	// CR stays not-ready forever; timeout should fire.
	cr := newGatewayCR(4, withObservedGeneration(3), withReadyCondition("False"))
	dyn, _ := newFakeDynamicClient(cr)
	cs := newFakePodClient()

	err := waitForGatewayReady(context.Background(), dyn, cs, "test-ns", "test-gw", 10*time.Millisecond, 100*time.Millisecond, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected DeadlineExceeded, got: %v", err)
}

func TestWaitForGatewayReady_ParentCtxCancelled_ReturnsCanceled(t *testing.T) {
	shortenDetectionWindow(t, 20*time.Millisecond)

	cr := newGatewayCR(4, withObservedGeneration(3), withReadyCondition("False"))
	dyn, _ := newFakeDynamicClient(cr)
	cs := newFakePodClient()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := waitForGatewayReady(ctx, dyn, cs, "test-ns", "test-gw", 10*time.Millisecond, 0, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "expected Canceled, got: %v", err)
}

// ===========================================================================
// waitForGatewayReady — error paths
// ===========================================================================

func TestWaitForGatewayReady_InitialReadFails_ReturnsError(t *testing.T) {
	// No CR exists in the fake client; the initial Get fails.
	shortenDetectionWindow(t, 20*time.Millisecond)

	dyn, _ := newFakeDynamicClient()
	cs := newFakePodClient()

	err := waitForGatewayReady(context.Background(), dyn, cs, "test-ns", "test-gw", 10*time.Millisecond, 0, nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "failed to read gateway CR"), "error should mention CR read failure, got: %v", err)
}

func TestWaitForGatewayReady_DetectionReadFails_ReturnsError(t *testing.T) {
	shortenDetectionWindow(t, 100*time.Millisecond)

	cr := newGatewayCR(5, withObservedGeneration(5), withReadyCondition("True"))
	dyn, _ := newFakeDynamicClient(cr)

	// Fail the second get (after the initial generation capture).
	var getCount int32
	dyn.PrependReactor("get", "gateways", func(action ktesting.Action) (bool, runtime.Object, error) {
		n := atomic.AddInt32(&getCount, 1)
		if n >= 2 {
			return true, nil, fmt.Errorf("kube-api transient failure")
		}
		return false, nil, nil
	})

	cs := newFakePodClient()
	err := waitForGatewayReady(context.Background(), dyn, cs, "test-ns", "test-gw", 10*time.Millisecond, 0, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transient failure")
}

// ===========================================================================
// waitForGatewayReady — progress callback invariants
// ===========================================================================

func TestWaitForGatewayReady_ProgressElapsedIsMonotonic(t *testing.T) {
	shortenDetectionWindow(t, 20*time.Millisecond)

	initial := newGatewayCR(3, withObservedGeneration(2), withReadyCondition("False"))
	dyn, tracker := newFakeDynamicClient(initial)
	cs := newFakePodClient()

	go func() {
		time.Sleep(150 * time.Millisecond)
		updated := newGatewayCR(3, withObservedGeneration(3), withReadyCondition("True"))
		_ = tracker.Update(gatewayGVR(), updated, "test-ns")
	}()

	var elapsedSeen []time.Duration
	mu := &sync.Mutex{}
	err := waitForGatewayReady(context.Background(), dyn, cs, "test-ns", "test-gw", 25*time.Millisecond, 5*time.Second, func(p GatewayReadinessProgress) {
		mu.Lock()
		defer mu.Unlock()
		elapsedSeen = append(elapsedSeen, p.Elapsed)
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(elapsedSeen), 2)
	for i := 1; i < len(elapsedSeen); i++ {
		assert.GreaterOrEqual(t, elapsedSeen[i], elapsedSeen[i-1], "elapsed at index %d (%v) regressed from %v", i, elapsedSeen[i], elapsedSeen[i-1])
	}
}

// ===========================================================================
// Helpers
// ===========================================================================

func gatewayGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    GatewayGroup,
		Version:  GatewayVersion,
		Resource: GatewayResourcePlural,
	}
}

func newFakeDynamicClient(initial ...runtime.Object) (*dynamicfake.FakeDynamicClient, *fakeTracker) {
	scheme := runtime.NewScheme()
	gvk := schema.GroupVersionKind{Group: GatewayGroup, Version: GatewayVersion, Kind: "Gateway"}
	listGVK := schema.GroupVersionKind{Group: GatewayGroup, Version: GatewayVersion, Kind: "GatewayList"}
	scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})

	gvrToListKind := map[schema.GroupVersionResource]string{
		gatewayGVR(): "GatewayList",
	}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)
	for _, obj := range initial {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			panic(fmt.Sprintf("initial object is not *unstructured.Unstructured: %T", obj))
		}
		if err := client.Tracker().Create(gatewayGVR(), u, u.GetNamespace()); err != nil {
			panic(fmt.Sprintf("failed to seed tracker with gateway CR: %v", err))
		}
	}
	return client, &fakeTracker{client: client}
}

// fakeTracker wraps the fake dynamic client's tracker with a helper that
// always tags updates with the gateway GVR.
type fakeTracker struct {
	client dynamic.Interface
}

func (t *fakeTracker) Update(gvr schema.GroupVersionResource, obj runtime.Object, namespace string) error {
	fc, ok := t.client.(*dynamicfake.FakeDynamicClient)
	if !ok {
		return fmt.Errorf("not a fake dynamic client")
	}
	return fc.Tracker().Update(gvr, obj, namespace)
}

type crOption func(obj map[string]any)

func newGatewayCR(generation int64, opts ...crOption) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": "platform.confluent.io/v1beta1",
		"kind":       "Gateway",
		"metadata": map[string]any{
			"name":       "test-gw",
			"namespace":  "test-ns",
			"generation": generation,
		},
		"status": map[string]any{},
	}
	for _, opt := range opts {
		opt(obj)
	}
	return &unstructured.Unstructured{Object: obj}
}

func withObservedGeneration(gen int64) crOption {
	return func(obj map[string]any) {
		status, _ := obj["status"].(map[string]any)
		if status == nil {
			status = map[string]any{}
			obj["status"] = status
		}
		status["observedGeneration"] = gen
	}
}

func withReadyCondition(status string) crOption {
	return func(obj map[string]any) {
		s, _ := obj["status"].(map[string]any)
		if s == nil {
			s = map[string]any{}
			obj["status"] = s
		}
		conditions, _ := s["conditions"].([]any)
		conditions = append(conditions, map[string]any{
			"type":   "Ready",
			"status": status,
		})
		s["conditions"] = conditions
	}
}

func makePod(name, namespace, gatewayName string, ready bool) *corev1.Pod {
	readyStatus := corev1.ConditionFalse
	phase := corev1.PodPending
	if ready {
		readyStatus = corev1.ConditionTrue
		phase = corev1.PodRunning
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": gatewayName},
		},
		Status: corev1.PodStatus{
			Phase: phase,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: readyStatus},
			},
		},
	}
}

// shortenDetectionWindow swaps the package-level detection window for a
// shorter test value and restores it on teardown.
func shortenDetectionWindow(t *testing.T, d time.Duration) {
	t.Helper()
	original := gatewayReadinessDetectionWindow
	gatewayReadinessDetectionWindow = d
	t.Cleanup(func() { gatewayReadinessDetectionWindow = original })
}

// newFakePodClient constructs a fake kubernetes clientset for tests.
// NewSimpleClientset is deprecated in favor of NewClientset, but the latter
// requires apply-configuration generation that this repo does not produce.
// Centralising the call here keeps the staticcheck suppression scoped to one
// spot.
func newFakePodClient(pods ...runtime.Object) kubernetes.Interface {
	return kubernetesfake.NewSimpleClientset(pods...) //nolint:staticcheck // SA1019: see comment
}
