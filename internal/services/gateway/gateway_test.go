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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
