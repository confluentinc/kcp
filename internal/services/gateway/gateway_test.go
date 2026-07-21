package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// ===========================================================================
// deploymentRolloutComplete tests (pure logic)
// ===========================================================================

func TestDeploymentRolloutComplete_AllConditionsMet(t *testing.T) {
	dep := newGatewayDeployment("test-gw", "test-ns", 5,
		withObservedGeneration(5),
		withReplicas(3),
		withUpdatedReplicas(3),
		withAvailableReplicas(3),
	)
	assert.True(t, deploymentRolloutComplete(dep))
}

func TestDeploymentRolloutComplete_ObservedGenerationLags(t *testing.T) {
	dep := newGatewayDeployment("test-gw", "test-ns", 5,
		withObservedGeneration(4),
		withReplicas(3),
		withUpdatedReplicas(3),
		withAvailableReplicas(3),
	)
	assert.False(t, deploymentRolloutComplete(dep), "operator hasn't acknowledged the spec bump yet")
}

func TestDeploymentRolloutComplete_ZeroReplicas(t *testing.T) {
	dep := newGatewayDeployment("test-gw", "test-ns", 5,
		withObservedGeneration(5),
		withReplicas(0),
		withUpdatedReplicas(0),
		withAvailableReplicas(0),
	)
	assert.False(t, deploymentRolloutComplete(dep), "paused/degenerate deployment is not ready")
}

func TestDeploymentRolloutComplete_UpdatedButNotAvailable(t *testing.T) {
	dep := newGatewayDeployment("test-gw", "test-ns", 5,
		withObservedGeneration(5),
		withReplicas(3),
		withUpdatedReplicas(3),
		withAvailableReplicas(2), // pods updated but not yet through minReadySeconds
	)
	assert.False(t, deploymentRolloutComplete(dep), "not all replicas available yet")
}

func TestDeploymentRolloutComplete_UpdatedReplicasBehind(t *testing.T) {
	dep := newGatewayDeployment("test-gw", "test-ns", 5,
		withObservedGeneration(5),
		withReplicas(3),
		withUpdatedReplicas(2), // rolling update still in progress
		withAvailableReplicas(3),
	)
	assert.False(t, deploymentRolloutComplete(dep), "rolling update still in progress")
}

func TestDeploymentRolloutComplete_Nil(t *testing.T) {
	assert.False(t, deploymentRolloutComplete(nil))
}

// ===========================================================================
// waitForGatewayReady — happy paths
// ===========================================================================

func TestWaitForGatewayReady_DetectionPhase_NoRollout_ReturnsNoOp(t *testing.T) {
	// Deployment is already at rollout-complete state for the entire detection
	// window — should report RolloutDetected=false and return.
	shortenDetectionWindow(t, 50*time.Millisecond)

	dep := newGatewayDeployment("test-gw", "test-ns", 3,
		withObservedGeneration(3),
		withReplicas(2),
		withUpdatedReplicas(2),
		withAvailableReplicas(2),
		withReadyReplicas(2),
	)
	cs := newFakeClientset(dep)

	var progressCalls []GatewayReadinessProgress
	progressMu := &sync.Mutex{}
	onProgress := func(p GatewayReadinessProgress) {
		progressMu.Lock()
		defer progressMu.Unlock()
		progressCalls = append(progressCalls, p)
	}

	err := waitForGatewayReady(context.Background(), cs, "test-ns", "test-gw", 10*time.Millisecond, 0, onProgress)
	require.NoError(t, err)

	progressMu.Lock()
	defer progressMu.Unlock()
	require.Len(t, progressCalls, 1, "no-op should fire onProgress exactly once")
	assert.False(t, progressCalls[0].RolloutDetected)
	assert.True(t, progressCalls[0].Ready)
	assert.Equal(t, 2, progressCalls[0].InitialPodCount)
}

func TestWaitForGatewayReady_RolloutThenReady_ReturnsNil(t *testing.T) {
	// Deployment starts with observedGeneration < generation (rollout in
	// progress); a background goroutine transitions it to complete.
	shortenDetectionWindow(t, 30*time.Millisecond)

	initial := newGatewayDeployment("test-gw", "test-ns", 7,
		withObservedGeneration(6),
		withReplicas(3),
		withUpdatedReplicas(1),
		withAvailableReplicas(1),
		withReadyReplicas(1),
	)
	cs := newFakeClientset(initial)

	go func() {
		time.Sleep(80 * time.Millisecond)
		updated := newGatewayDeployment("test-gw", "test-ns", 7,
			withObservedGeneration(7),
			withReplicas(3),
			withUpdatedReplicas(3),
			withAvailableReplicas(3),
			withReadyReplicas(3),
		)
		updateDeployment(cs, updated)
	}()

	var progressCalls []GatewayReadinessProgress
	progressMu := &sync.Mutex{}
	onProgress := func(p GatewayReadinessProgress) {
		progressMu.Lock()
		defer progressMu.Unlock()
		progressCalls = append(progressCalls, p)
	}

	err := waitForGatewayReady(context.Background(), cs, "test-ns", "test-gw", 15*time.Millisecond, 5*time.Second, onProgress)
	require.NoError(t, err)

	progressMu.Lock()
	defer progressMu.Unlock()
	require.NotEmpty(t, progressCalls)
	final := progressCalls[len(progressCalls)-1]
	assert.True(t, final.RolloutDetected, "rollout should have been detected")
	assert.True(t, final.Ready, "final progress should reflect Ready=true")
	assert.Greater(t, final.Elapsed, time.Duration(0))
	assert.Equal(t, 3, final.PodsReady, "PodsReady comes from readyReplicas at completion")
}

func TestWaitForGatewayReady_NoDeadline_RunsUntilReady(t *testing.T) {
	// timeout=0 means no deadline. Simulate a slow rollout (~300ms) and assert
	// we wait it out instead of failing.
	shortenDetectionWindow(t, 20*time.Millisecond)

	initial := newGatewayDeployment("test-gw", "test-ns", 2,
		withObservedGeneration(1),
		withReplicas(1),
		withUpdatedReplicas(0),
		withAvailableReplicas(0),
	)
	cs := newFakeClientset(initial)

	go func() {
		time.Sleep(300 * time.Millisecond)
		updated := newGatewayDeployment("test-gw", "test-ns", 2,
			withObservedGeneration(2),
			withReplicas(1),
			withUpdatedReplicas(1),
			withAvailableReplicas(1),
		)
		updateDeployment(cs, updated)
	}()

	start := time.Now()
	err := waitForGatewayReady(context.Background(), cs, "test-ns", "test-gw", 20*time.Millisecond, 0, nil)
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 300*time.Millisecond, "wait should run for at least the rollout duration")
}

// ===========================================================================
// waitForGatewayReady — timeout and cancellation
// ===========================================================================

func TestWaitForGatewayReady_TimeoutExceeded_ReturnsDeadlineExceeded(t *testing.T) {
	shortenDetectionWindow(t, 20*time.Millisecond)

	dep := newGatewayDeployment("test-gw", "test-ns", 4,
		withObservedGeneration(3),
		withReplicas(2),
		withUpdatedReplicas(1),
		withAvailableReplicas(1),
	)
	cs := newFakeClientset(dep)

	err := waitForGatewayReady(context.Background(), cs, "test-ns", "test-gw", 10*time.Millisecond, 100*time.Millisecond, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected DeadlineExceeded, got: %v", err)
}

func TestWaitForGatewayReady_ParentCtxCancelled_ReturnsCanceled(t *testing.T) {
	shortenDetectionWindow(t, 20*time.Millisecond)

	dep := newGatewayDeployment("test-gw", "test-ns", 4,
		withObservedGeneration(3),
		withReplicas(2),
		withUpdatedReplicas(1),
		withAvailableReplicas(1),
	)
	cs := newFakeClientset(dep)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := waitForGatewayReady(ctx, cs, "test-ns", "test-gw", 10*time.Millisecond, 0, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "expected Canceled, got: %v", err)
}

// ===========================================================================
// waitForGatewayReady — error paths
// ===========================================================================

func TestWaitForGatewayReady_NoDeploymentFound_ReturnsError(t *testing.T) {
	// No deployment in fake clientset — initial resolution fails.
	shortenDetectionWindow(t, 20*time.Millisecond)
	cs := newFakeClientset()

	err := waitForGatewayReady(context.Background(), cs, "test-ns", "test-gw", 10*time.Millisecond, 0, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway deployment not found")
}

func TestWaitForGatewayReady_TransientAPIError_ReturnsError(t *testing.T) {
	shortenDetectionWindow(t, 100*time.Millisecond)

	// Deployment is incomplete so detection loop polls and hits the transient error.
	dep := newGatewayDeployment("test-gw", "test-ns", 5,
		withObservedGeneration(4),
		withReplicas(2),
		withUpdatedReplicas(1),
		withAvailableReplicas(1),
	)
	cs := newFakeClientset(dep)

	// Fail the second Get (first is the initial resolution; second is the detection loop).
	var getCount int32
	cs.(*kubernetesfake.Clientset).PrependReactor("get", "deployments", func(action ktesting.Action) (bool, runtime.Object, error) {
		n := atomic.AddInt32(&getCount, 1)
		if n >= 2 {
			return true, nil, fmt.Errorf("kube-api transient failure")
		}
		return false, nil, nil
	})

	err := waitForGatewayReady(context.Background(), cs, "test-ns", "test-gw", 10*time.Millisecond, 0, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transient failure")
}

// ===========================================================================
// resolveGatewayDeployment — ownerReferences fallback tests
// ===========================================================================

func TestResolveGatewayDeployment_OwnerRefFallback_ZeroMatches_ReturnsError(t *testing.T) {
	cs := newFakeClientset()

	_, err := resolveGatewayDeployment(context.Background(), cs, "test-ns", "test-gw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway deployment not found")
}

func TestResolveGatewayDeployment_OwnerRefFallback_MultipleMatches_ReturnsError(t *testing.T) {
	d1 := newGatewayDeployment("test-gw-a", "test-ns", 1, withGatewayOwner("test-gw"))
	d2 := newGatewayDeployment("test-gw-b", "test-ns", 1, withGatewayOwner("test-gw"))
	cs := newFakeClientset(d1, d2)

	_, err := resolveGatewayDeployment(context.Background(), cs, "test-ns", "test-gw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple deployments owned by gateway")
}

func TestResolveGatewayDeployment_OwnerRefFallback_OneMatch_Returns(t *testing.T) {
	dep := newGatewayDeployment("test-gw-deploy", "test-ns", 3,
		withGatewayOwner("test-gw"),
		withObservedGeneration(3),
		withReplicas(2),
		withUpdatedReplicas(2),
		withAvailableReplicas(2),
	)
	cs := newFakeClientset(dep)

	found, err := resolveGatewayDeployment(context.Background(), cs, "test-ns", "test-gw")
	require.NoError(t, err)
	assert.Equal(t, "test-gw-deploy", found.Name)
}

func TestWaitForGatewayReady_OwnerRefFallback_FullWait_ReturnsNil(t *testing.T) {
	// Deployment is found via ownerReferences (name differs from gateway).
	// Wait detects rollout in progress and converges to complete.
	shortenDetectionWindow(t, 30*time.Millisecond)

	initial := newGatewayDeployment("test-gw-deploy", "test-ns", 5,
		withGatewayOwner("test-gw"),
		withObservedGeneration(4),
		withReplicas(2),
		withUpdatedReplicas(0),
		withAvailableReplicas(0),
	)
	cs := newFakeClientset(initial)

	go func() {
		time.Sleep(80 * time.Millisecond)
		updated := newGatewayDeployment("test-gw-deploy", "test-ns", 5,
			withGatewayOwner("test-gw"),
			withObservedGeneration(5),
			withReplicas(2),
			withUpdatedReplicas(2),
			withAvailableReplicas(2),
		)
		updateDeployment(cs, updated)
	}()

	err := waitForGatewayReady(context.Background(), cs, "test-ns", "test-gw", 15*time.Millisecond, 5*time.Second, nil)
	require.NoError(t, err)
}

// ===========================================================================
// waitForGatewayReady — progress callback invariants
// ===========================================================================

func TestWaitForGatewayReady_ProgressElapsedIsMonotonic(t *testing.T) {
	shortenDetectionWindow(t, 20*time.Millisecond)

	initial := newGatewayDeployment("test-gw", "test-ns", 3,
		withObservedGeneration(2),
		withReplicas(2),
		withUpdatedReplicas(1),
		withAvailableReplicas(1),
	)
	cs := newFakeClientset(initial)

	go func() {
		time.Sleep(150 * time.Millisecond)
		updated := newGatewayDeployment("test-gw", "test-ns", 3,
			withObservedGeneration(3),
			withReplicas(2),
			withUpdatedReplicas(2),
			withAvailableReplicas(2),
		)
		updateDeployment(cs, updated)
	}()

	var elapsedSeen []time.Duration
	mu := &sync.Mutex{}
	err := waitForGatewayReady(context.Background(), cs, "test-ns", "test-gw", 25*time.Millisecond, 5*time.Second, func(p GatewayReadinessProgress) {
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
// waitForGatewayPods — pod-drain completion
// ===========================================================================

// TestWaitForGatewayPods_SurgeCapture_CompletesOnDeploymentComplete guards the
// deadlock fix: if the pre-patch UID capture raced an in-flight rollout and
// grabbed 2 pods where the desired count is 1, newPodsReady can never reach the
// captured count. Completion must instead come from the old pods being gone and
// the Deployment reporting a finished rollout.
func TestWaitForGatewayPods_SurgeCapture_CompletesOnDeploymentComplete(t *testing.T) {
	ns, gw := "test-ns", "test-gw"
	// Captured 2 old UIDs during a surge; steady state is a single replica.
	initialUIDs := map[types.UID]struct{}{"old-1": {}, "old-2": {}}
	// Current cluster: old pods gone, one new ready pod, Deployment complete at 1.
	cs := newFakeClientset(
		newGatewayPod("gw-new", ns, gw, "new-1", true),
		completeGatewayDeployment(gw, ns, 1),
	)

	var last PodRolloutProgress
	onProgress := func(p PodRolloutProgress) { last = p }

	err := waitForGatewayPods(context.Background(), cs, ns, gw, initialUIDs, 5*time.Millisecond, 2*time.Second, onProgress)
	require.NoError(t, err, "must complete via deploymentRolloutComplete despite newPodsReady (1) < captured count (2)")

	assert.Equal(t, 0, last.OldPodsRemaining)
	assert.Equal(t, 1, last.NewPodsReady)
	assert.Equal(t, 2, last.InitialPodCount, "captured (inflated) count is reported but not used as the completion target")
}

// TestWaitForGatewayPods_OldPodStillServing_DoesNotComplete ensures we do not
// return while a captured old pod is still present — the whole point of the
// pod-drain wait over a plain readiness check.
func TestWaitForGatewayPods_OldPodStillServing_DoesNotComplete(t *testing.T) {
	ns, gw := "test-ns", "test-gw"
	initialUIDs := map[types.UID]struct{}{"old-1": {}}
	// Surge in progress: old pod still there + a new ready pod; Deployment not settled.
	cs := newFakeClientset(
		newGatewayPod("gw-old", ns, gw, "old-1", true),
		newGatewayPod("gw-new", ns, gw, "new-1", true),
		newGatewayDeployment(gw, ns, 2,
			withObservedGeneration(2),
			withReplicas(2),
			withUpdatedReplicas(1),
			withAvailableReplicas(1),
			withReadyReplicas(1),
		),
	)

	err := waitForGatewayPods(context.Background(), cs, ns, gw, initialUIDs, 5*time.Millisecond, 150*time.Millisecond, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out", "must keep waiting while an old pod is still present")
}

// TestWaitForGatewayPods_NoRollout_ReturnsNoOp covers the Phase-1 no-op path:
// when the apply triggers no pod change, the wait returns without a rollout.
func TestWaitForGatewayPods_NoRollout_ReturnsNoOp(t *testing.T) {
	shortenDetectionWindow(t, 40*time.Millisecond)
	ns, gw := "test-ns", "test-gw"
	initialUIDs := map[types.UID]struct{}{"old-1": {}}
	// Only the captured pod exists and it is ready → no rollout is detected.
	cs := newFakeClientset(
		newGatewayPod("gw-old", ns, gw, "old-1", true),
		completeGatewayDeployment(gw, ns, 1),
	)

	var last PodRolloutProgress
	var got bool
	onProgress := func(p PodRolloutProgress) { last = p; got = true }

	err := waitForGatewayPods(context.Background(), cs, ns, gw, initialUIDs, 5*time.Millisecond, 2*time.Second, onProgress)
	require.NoError(t, err)
	require.True(t, got, "no-op should still fire onProgress once")
	assert.False(t, last.RolloutDetected, "no pod change means no rollout detected")
}

// TestWaitForGatewayPods_ContextCancelled_Propagates ensures cancellation
// during the wait surfaces ctx.Err().
func TestWaitForGatewayPods_ContextCancelled_Propagates(t *testing.T) {
	ns, gw := "test-ns", "test-gw"
	initialUIDs := map[types.UID]struct{}{"old-1": {}}
	// Old pod lingers so the wait would otherwise never complete.
	cs := newFakeClientset(
		newGatewayPod("gw-old", ns, gw, "old-1", true),
		newGatewayPod("gw-new", ns, gw, "new-1", true),
		newGatewayDeployment(gw, ns, 2,
			withObservedGeneration(2), withReplicas(2), withUpdatedReplicas(1), withAvailableReplicas(1), withReadyReplicas(1),
		),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	err := waitForGatewayPods(ctx, cs, ns, gw, initialUIDs, 5*time.Millisecond, 0, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ===========================================================================
// waitForGatewayObservedGeneration — operator-reconcile guard
// ===========================================================================

func TestWaitForGatewayObservedGeneration_AlreadyReconciled_ReturnsImmediately(t *testing.T) {
	ns, gw := "test-ns", "test-gw"
	cs := newFakeDynamicClient(newGatewayCR(gw, ns, 3, 3, true))

	start := time.Now()
	err := waitForGatewayObservedGeneration(context.Background(), cs, ns, gw, 20*time.Millisecond, 2*time.Second)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 20*time.Millisecond, "observedGeneration>=generation should return without polling")
}

func TestWaitForGatewayObservedGeneration_WaitsUntilReconciled(t *testing.T) {
	ns, gw := "test-ns", "test-gw"
	// Fresh fence: generation bumped to 5, operator still at observedGeneration 4.
	cs := newFakeDynamicClient(newGatewayCR(gw, ns, 5, 4, true))

	go func() {
		time.Sleep(60 * time.Millisecond)
		updateGatewayCR(t, cs, newGatewayCR(gw, ns, 5, 5, true))
	}()

	start := time.Now()
	err := waitForGatewayObservedGeneration(context.Background(), cs, ns, gw, 10*time.Millisecond, 2*time.Second)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 60*time.Millisecond, "must wait until the operator observes the new generation")
}

func TestWaitForGatewayObservedGeneration_NoStatus_TimesOut(t *testing.T) {
	ns, gw := "test-ns", "test-gw"
	// Generation bumped but the operator has written no status yet.
	cs := newFakeDynamicClient(newGatewayCR(gw, ns, 2, 0, false))

	err := waitForGatewayObservedGeneration(context.Background(), cs, ns, gw, 10*time.Millisecond, 80*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestWaitForGatewayObservedGeneration_ContextCancelled_Propagates(t *testing.T) {
	ns, gw := "test-ns", "test-gw"
	cs := newFakeDynamicClient(newGatewayCR(gw, ns, 2, 1, true))

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	err := waitForGatewayObservedGeneration(ctx, cs, ns, gw, 10*time.Millisecond, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ===========================================================================
// Helpers
// ===========================================================================

type deploymentOption func(*appsv1.Deployment)

func newGatewayDeployment(name, namespace string, generation int64, opts ...deploymentOption) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Generation: generation,
		},
	}
	for _, opt := range opts {
		opt(dep)
	}
	return dep
}

func withObservedGeneration(gen int64) deploymentOption {
	return func(d *appsv1.Deployment) { d.Status.ObservedGeneration = gen }
}

func withReplicas(n int32) deploymentOption {
	return func(d *appsv1.Deployment) { d.Status.Replicas = n }
}

func withUpdatedReplicas(n int32) deploymentOption {
	return func(d *appsv1.Deployment) { d.Status.UpdatedReplicas = n }
}

func withAvailableReplicas(n int32) deploymentOption {
	return func(d *appsv1.Deployment) { d.Status.AvailableReplicas = n }
}

func withReadyReplicas(n int32) deploymentOption {
	return func(d *appsv1.Deployment) { d.Status.ReadyReplicas = n }
}

func withGatewayOwner(gatewayName string) deploymentOption {
	return func(d *appsv1.Deployment) {
		d.OwnerReferences = append(d.OwnerReferences, metav1.OwnerReference{
			Kind: "Gateway",
			Name: gatewayName,
		})
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

// gatewayGVRForTest is the Gateway CR GVR used to seed the fake dynamic client.
var gatewayGVRForTest = schema.GroupVersionResource{Group: GatewayGroup, Version: GatewayVersion, Resource: GatewayResourcePlural}

// newGatewayCR builds an unstructured Gateway CR with the given generation. When
// hasStatus is true, status.observedGeneration is set; otherwise status is
// absent (as it is before the operator first reconciles).
func newGatewayCR(name, namespace string, generation, observedGeneration int64, hasStatus bool) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: GatewayGroup, Version: GatewayVersion, Kind: "Gateway"})
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetGeneration(generation)
	if hasStatus {
		_ = unstructured.SetNestedField(obj.Object, observedGeneration, "status", "observedGeneration")
	}
	return obj
}

// newFakeDynamicClient seeds a fake dynamic client with the given Gateway CRs.
// Objects are Created after construction (rather than seeded at construction)
// so the tracker maps them under the Gateway GVR without needing a populated
// scheme.
func newFakeDynamicClient(objs ...*unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	cs := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{gatewayGVRForTest: "GatewayList"},
	)
	for _, o := range objs {
		if _, err := cs.Resource(gatewayGVRForTest).Namespace(o.GetNamespace()).
			Create(context.Background(), o, metav1.CreateOptions{}); err != nil {
			panic(fmt.Sprintf("newFakeDynamicClient seed: %v", err))
		}
	}
	return cs
}

// updateGatewayCR replaces the Gateway CR in the fake dynamic client (used by
// background goroutines to simulate the operator advancing observedGeneration).
func updateGatewayCR(t *testing.T, cs *dynamicfake.FakeDynamicClient, obj *unstructured.Unstructured) {
	t.Helper()
	_, err := cs.Resource(gatewayGVRForTest).Namespace(obj.GetNamespace()).Update(context.Background(), obj, metav1.UpdateOptions{})
	require.NoError(t, err)
}

// newFakeClientset constructs a fake kubernetes clientset seeded with the given
// objects. NewSimpleClientset is deprecated in favour of NewClientset, but the
// latter requires apply-configuration generation that this repo does not
// produce. Centralising the call here keeps the staticcheck suppression in one
// place.
func newFakeClientset(objects ...runtime.Object) kubernetes.Interface {
	return kubernetesfake.NewSimpleClientset(objects...) //nolint:staticcheck // SA1019: see comment
}

// updateDeployment updates a Deployment in the fake clientset (used by
// background goroutines to simulate operator state transitions).
func updateDeployment(cs kubernetes.Interface, dep *appsv1.Deployment) {
	_, err := cs.AppsV1().Deployments(dep.Namespace).Update(context.Background(), dep, metav1.UpdateOptions{})
	if err != nil {
		panic(fmt.Sprintf("updateDeployment: %v", err))
	}
}

// newGatewayPod builds a gateway pod labelled app=<gatewayName> with the given
// UID. A ready pod is Running with a PodReady=True condition (what isPodReady
// requires); a non-ready pod is Pending.
func newGatewayPod(name, namespace, gatewayName, uid string, ready bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(uid),
			Labels:    map[string]string{"app": gatewayName},
		},
	}
	if ready {
		pod.Status.Phase = corev1.PodRunning
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	} else {
		pod.Status.Phase = corev1.PodPending
	}
	return pod
}

// completeGatewayDeployment builds a Deployment reporting a finished rollout at
// the given replica count.
func completeGatewayDeployment(name, namespace string, replicas int32) *appsv1.Deployment {
	return newGatewayDeployment(name, namespace, 1,
		withObservedGeneration(1),
		withReplicas(replicas),
		withUpdatedReplicas(replicas),
		withAvailableReplicas(replicas),
		withReadyReplicas(replicas),
	)
}
