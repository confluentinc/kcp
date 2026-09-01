package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The per-pod configId check verifies both mechanisms identically, but they take
// very different amounts of time, so the budget follows whichever one the
// cluster is observed choosing.
//
// The failure this protects against is specific: a switchover CR that adds a
// route or a TLS secret makes CFK roll the pods, and a roll capped at the
// hot-reload budget fails partway through with traffic already fenced.

func TestConfigWaitBudget(t *testing.T) {
	ns, gw := "confluent", "test-gw"
	const want = "kcp-newrevision"

	t.Run("a roll observed mid-wait switches to the roll budget", func(t *testing.T) {
		// Generation 4 against a baseline of 3: CFK rewrote the pod template. The
		// pods never reach the wanted id, so the only thing that ends this wait is
		// a deadline — and it must be the roll one.
		cs := newFakeClientset(
			newGatewayDeployment(gw, ns, 4,
				withObservedGeneration(4), withReplicas(1),
				withUpdatedReplicas(1), withAvailableReplicas(1), withReadyReplicas(1)),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)

		start := time.Now()
		err := waitForGatewayConfigID(context.Background(), cs, staticProber("kcp-old"), ns, gw, ConfigWaitOptions{
			ConfigID:                     want,
			BaselineDeploymentGeneration: 3,
			PollInterval:                 10 * time.Millisecond,
			HotReloadTimeout:             40 * time.Millisecond,
			RollTimeout:                  400 * time.Millisecond,
		})
		elapsed := time.Since(start)

		require.Error(t, err)
		assert.Greater(t, elapsed, 200*time.Millisecond,
			"the wait must not be cut off at the hot-reload budget once a roll is visible")
		assert.Contains(t, err.Error(), string(MechanismPodRoll),
			"the timeout should say which mechanism it was waiting on")
	})

	t.Run("no roll keeps the hot-reload budget", func(t *testing.T) {
		// Generation equal to the baseline: CFK applied in place. A hot-reload
		// that never lands must fail promptly rather than inherit the roll budget.
		cs := newFakeClientset(
			newGatewayDeployment(gw, ns, 3,
				withObservedGeneration(3), withReplicas(1),
				withUpdatedReplicas(1), withAvailableReplicas(1), withReadyReplicas(1)),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)

		start := time.Now()
		err := waitForGatewayConfigID(context.Background(), cs, staticProber("kcp-old"), ns, gw, ConfigWaitOptions{
			ConfigID:                     want,
			BaselineDeploymentGeneration: 3,
			PollInterval:                 10 * time.Millisecond,
			HotReloadTimeout:             60 * time.Millisecond,
			RollTimeout:                  0, // unbounded, and must not be reached
		})
		elapsed := time.Since(start)

		require.Error(t, err)
		assert.Less(t, elapsed, 2*time.Second, "an unlanded hot-reload must not wait on the unbounded roll budget")
		assert.Contains(t, err.Error(), string(MechanismNoRollObserved))
	})

	t.Run("an unknown baseline stays bounded", func(t *testing.T) {
		// Baseline 0 means the pre-apply read failed. Treating any generation as a
		// roll would hand this wait the usually-unbounded roll budget — and hanging
		// forever is exactly the outcome on a gateway whose config watcher never
		// started, which is the failure this budget exists to surface.
		cs := newFakeClientset(
			newGatewayDeployment(gw, ns, 7,
				withObservedGeneration(7), withReplicas(1),
				withUpdatedReplicas(1), withAvailableReplicas(1), withReadyReplicas(1)),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)

		start := time.Now()
		err := waitForGatewayConfigID(context.Background(), cs, staticProber("kcp-old"), ns, gw, ConfigWaitOptions{
			ConfigID:                     want,
			BaselineDeploymentGeneration: 0,
			PollInterval:                 10 * time.Millisecond,
			HotReloadTimeout:             60 * time.Millisecond,
			RollTimeout:                  0,
		})

		require.Error(t, err)
		assert.Less(t, time.Since(start), 2*time.Second, "an unknown baseline must not grant an unbounded wait")
	})

	t.Run("progress reports the observed mechanism", func(t *testing.T) {
		cs := newFakeClientset(
			newGatewayDeployment(gw, ns, 9,
				withObservedGeneration(9), withReplicas(1),
				withUpdatedReplicas(1), withAvailableReplicas(1), withReadyReplicas(1)),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)

		var seen []RolloutMechanism
		_ = waitForGatewayConfigID(context.Background(), cs, staticProber("kcp-old"), ns, gw, ConfigWaitOptions{
			ConfigID:                     want,
			BaselineDeploymentGeneration: 8,
			PollInterval:                 10 * time.Millisecond,
			HotReloadTimeout:             30 * time.Millisecond,
			RollTimeout:                  80 * time.Millisecond,
			OnProgress:                   func(p ConfigWaitProgress) { seen = append(seen, p.Mechanism) },
		})

		require.NotEmpty(t, seen)
		assert.Equal(t, MechanismPodRoll, seen[0], "the roll is visible on the first poll here")
	})

	t.Run("a hot-reload budget of 0 falls back to the default rather than running unbounded", func(t *testing.T) {
		// Never unbounded: a hot-reload moves no Kubernetes signal that could be
		// waited on instead, so there would be nothing to end the wait.
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 1),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)

		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()

		err := waitForGatewayConfigID(ctx, cs, staticProber("kcp-old"), ns, gw, ConfigWaitOptions{
			ConfigID:         want,
			PollInterval:     10 * time.Millisecond,
			HotReloadTimeout: 0,
		})

		// The context expires first, which is the point: with HotReloadTimeout 0
		// the wait adopted DefaultHotReloadTimeout (90s) rather than no deadline.
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}
