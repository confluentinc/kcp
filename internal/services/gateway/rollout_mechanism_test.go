package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// ===========================================================================
// getGatewayDeploymentGeneration
// ===========================================================================

func TestGetGatewayDeploymentGeneration_ReturnsMetadataGeneration(t *testing.T) {
	cs := newFakeClientset(newGatewayDeployment("test-gw", "test-ns", 9, withObservedGeneration(9)))

	gen, err := getGatewayDeploymentGeneration(context.Background(), cs, "test-ns", "test-gw")
	require.NoError(t, err)
	assert.Equal(t, int64(9), gen)
}

func TestGetGatewayDeploymentGeneration_OwnerRefFallback(t *testing.T) {
	// Deployment name differs from the Gateway CR name, so the baseline has to
	// come through the same ownerReferences fallback the waits use.
	cs := newFakeClientset(newGatewayDeployment("test-gw-deploy", "test-ns", 4, withGatewayOwner("test-gw")))

	gen, err := getGatewayDeploymentGeneration(context.Background(), cs, "test-ns", "test-gw")
	require.NoError(t, err)
	assert.Equal(t, int64(4), gen)
}

func TestGetGatewayDeploymentGeneration_NotFound_ReturnsError(t *testing.T) {
	cs := newFakeClientset()

	_, err := getGatewayDeploymentGeneration(context.Background(), cs, "test-ns", "test-gw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway deployment not found")
}

// ===========================================================================
// observeRolloutMechanism
// ===========================================================================

func TestObserveRolloutMechanism_GenerationAlreadyBumped_ReturnsPodRollImmediately(t *testing.T) {
	// The operator rewrote the pod template before we looked. The window must not
	// be paid at all — the bump is already conclusive.
	shortenRollConfirmationWindow(t, 5*time.Second)
	cs := newFakeClientset(newGatewayDeployment("test-gw", "test-ns", 8, withObservedGeneration(7)))

	start := time.Now()
	mechanism, dep, err := observeRolloutMechanism(context.Background(), cs, "test-ns", "test-gw", 7, 50*time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, MechanismPodRoll, mechanism)
	require.NotNil(t, dep)
	assert.Equal(t, int64(8), dep.Generation, "the caller gets the Deployment it decided on")
	assert.Less(t, time.Since(start), 50*time.Millisecond, "a bump already visible must not wait out the window")
}

func TestObserveRolloutMechanism_GenerationBumpsMidWindow_ReturnsPodRoll(t *testing.T) {
	// The operator writes the Deployment a moment after it writes the CR status.
	// This is the residual race the confirmation window exists to cover.
	shortenRollConfirmationWindow(t, 2*time.Second)
	cs := newFakeClientset(newGatewayDeployment("test-gw", "test-ns", 3, withObservedGeneration(3)))

	go func() {
		time.Sleep(60 * time.Millisecond)
		updateDeployment(cs, newGatewayDeployment("test-gw", "test-ns", 4, withObservedGeneration(3)))
	}()

	mechanism, dep, err := observeRolloutMechanism(context.Background(), cs, "test-ns", "test-gw", 3, 10*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, MechanismPodRoll, mechanism)
	assert.Equal(t, int64(4), dep.Generation)
}

func TestObserveRolloutMechanism_NoBumpWithinWindow_ReturnsNoRollObserved(t *testing.T) {
	shortenRollConfirmationWindow(t, 60*time.Millisecond)
	cs := newFakeClientset(newGatewayDeployment("test-gw", "test-ns", 5, withObservedGeneration(5)))

	mechanism, dep, err := observeRolloutMechanism(context.Background(), cs, "test-ns", "test-gw", 5, 10*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, MechanismNoRollObserved, mechanism)
	require.NotNil(t, dep, "the last read Deployment is returned either way")
}

// TestObserveRolloutMechanism_CompletedRollBetweenPolls_StillReturnsPodRoll is
// the regression the generation baseline exists for.
//
// The previous detection window asked "does the Deployment look unsettled right
// now". A single-replica gateway with a warm image can roll and settle inside one
// poll interval, so every observation sees a complete Deployment and the wait
// concluded "no restart required" — never verifying the roll it had just caused.
// metadata.generation does not rewind, so the roll is still visible afterwards.
func TestObserveRolloutMechanism_CompletedRollBetweenPolls_StillReturnsPodRoll(t *testing.T) {
	shortenRollConfirmationWindow(t, 60*time.Millisecond)
	// Generation 6 and fully converged at 6: a rollout that already finished.
	cs := newFakeClientset(newGatewayDeployment("test-gw", "test-ns", 6,
		withObservedGeneration(6),
		withReplicas(1),
		withUpdatedReplicas(1),
		withAvailableReplicas(1),
		withReadyReplicas(1),
	))

	mechanism, _, err := observeRolloutMechanism(context.Background(), cs, "test-ns", "test-gw", 5, 10*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, MechanismPodRoll, mechanism,
		"a finished roll is still a roll: the generation moved past the pre-apply baseline")
}

func TestObserveRolloutMechanism_UnknownBaseline_TreatsAnyGenerationAsBump(t *testing.T) {
	// Baseline 0 means the pre-apply read failed. Routing to the convergence wait
	// is the conservative direction — the alternative is declaring there is
	// nothing to wait for on no evidence.
	shortenRollConfirmationWindow(t, 5*time.Second)
	cs := newFakeClientset(newGatewayDeployment("test-gw", "test-ns", 1, withObservedGeneration(1)))

	mechanism, _, err := observeRolloutMechanism(context.Background(), cs, "test-ns", "test-gw", 0, 10*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, MechanismPodRoll, mechanism)
}

func TestObserveRolloutMechanism_ContextCancelled_ReturnsError(t *testing.T) {
	shortenRollConfirmationWindow(t, 5*time.Second)
	cs := newFakeClientset(newGatewayDeployment("test-gw", "test-ns", 2, withObservedGeneration(2)))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	_, _, err := observeRolloutMechanism(ctx, cs, "test-ns", "test-gw", 2, 10*time.Millisecond)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestObserveRolloutMechanism_DeploymentGoesMissing_ReturnsError(t *testing.T) {
	shortenRollConfirmationWindow(t, 200*time.Millisecond)
	cs := newFakeClientset(newGatewayDeployment("test-gw", "test-ns", 2, withObservedGeneration(2)))

	var getCount int32
	cs.(*kubernetesfake.Clientset).PrependReactor("get", "deployments", func(action ktesting.Action) (bool, runtime.Object, error) {
		if atomic.AddInt32(&getCount, 1) >= 2 {
			return true, nil, fmt.Errorf("kube-api transient failure")
		}
		return false, nil, nil
	})

	_, _, err := observeRolloutMechanism(context.Background(), cs, "test-ns", "test-gw", 2, 10*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transient failure")
	assert.False(t, errors.Is(err, context.Canceled))
}

// TestObserveRolloutMechanism_ZeroWindow_StillObservesOnce guards the boundary:
// even with no window to spend, one observation happens, so an already-visible
// bump is never missed.
func TestObserveRolloutMechanism_ZeroWindow_StillObservesOnce(t *testing.T) {
	shortenRollConfirmationWindow(t, 0)
	cs := newFakeClientset(newGatewayDeployment("test-gw", "test-ns", 4, withObservedGeneration(3)))

	mechanism, _, err := observeRolloutMechanism(context.Background(), cs, "test-ns", "test-gw", 3, 10*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, MechanismPodRoll, mechanism)
}
