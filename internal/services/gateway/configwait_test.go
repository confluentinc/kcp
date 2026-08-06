package gateway

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testPollInterval = 5 * time.Millisecond
	testWaitTimeout  = 400 * time.Millisecond
)

// staticProber answers every pod with the same configId.
func staticProber(configID string) podProber {
	return func(_ context.Context, e GatewayPodEndpoint) (ProbeResult, error) {
		return ProbeResult{Addr: e.IP, Outcome: ProbeApplied, ConfigID: configID}, nil
	}
}

// perPodProber answers each pod by name, defaulting to the old revision.
func perPodProber(byPod map[string]string, fallback string) podProber {
	return func(_ context.Context, e GatewayPodEndpoint) (ProbeResult, error) {
		id, ok := byPod[e.Name]
		if !ok {
			id = fallback
		}
		return ProbeResult{Addr: e.IP, Outcome: ProbeApplied, ConfigID: id}, nil
	}
}

func TestWaitForGatewayConfigID(t *testing.T) {
	const ns, gw, want = "confluent", "test-gateway", "kcp-new"

	t.Run("converges when every ready pod reports the wanted configId", func(t *testing.T) {
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 2),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "10.0.1.2", true),
		)

		err := waitForGatewayConfigID(context.Background(), cs, staticProber(want), ns, gw, want,
			testPollInterval, testWaitTimeout, nil)
		require.NoError(t, err)
	})

	t.Run("waits while a pod still reports the previous configId", func(t *testing.T) {
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 2),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "10.0.1.2", true),
		)
		prober := perPodProber(map[string]string{"gw-1": want}, "kcp-old")

		err := waitForGatewayConfigID(context.Background(), cs, prober, ns, gw, want,
			testPollInterval, testWaitTimeout, nil)
		require.Error(t, err, "one lagging pod must not be reported as converged")
	})

	t.Run("converges once a lagging pod catches up", func(t *testing.T) {
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 2),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "10.0.1.2", true),
		)

		var mu sync.Mutex
		calls := 0
		prober := func(_ context.Context, e GatewayPodEndpoint) (ProbeResult, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			id := want
			// gw-2 lags for the first few rounds, then applies the revision.
			if e.Name == "gw-2" && calls < 6 {
				id = "kcp-old"
			}
			return ProbeResult{Outcome: ProbeApplied, ConfigID: id}, nil
		}

		err := waitForGatewayConfigID(context.Background(), cs, prober, ns, gw, want,
			testPollInterval, testWaitTimeout, nil)
		require.NoError(t, err)
	})

	t.Run("an empty pod set never counts as success", func(t *testing.T) {
		// An all-of over zero pods is vacuously true; without an explicit guard
		// this would report a switchover as verified against no gateway at all.
		cs := newFakeClientset(completeGatewayDeployment(gw, ns, 2))

		err := waitForGatewayConfigID(context.Background(), cs, staticProber(want), ns, gw, want,
			testPollInterval, testWaitTimeout, nil)
		require.Error(t, err)
	})

	t.Run("a pod set with no ready members never counts as success", func(t *testing.T) {
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 2),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", false),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "10.0.1.2", false),
		)

		err := waitForGatewayConfigID(context.Background(), cs, staticProber(want), ns, gw, want,
			testPollInterval, testWaitTimeout, nil)
		require.Error(t, err)
	})

	t.Run("waits for the deployment rollout even when pods already report the id", func(t *testing.T) {
		// The roll case: surviving old pods may already carry the revision while
		// replacements are still coming up. Gating on the Deployment as well is
		// what makes one predicate cover both a hot-reload and a roll.
		cs := newFakeClientset(
			newGatewayDeployment(gw, ns, 2,
				withObservedGeneration(1),
				withReplicas(2),
				withUpdatedReplicas(1),
				withAvailableReplicas(1),
				withReadyReplicas(1),
			),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)

		err := waitForGatewayConfigID(context.Background(), cs, staticProber(want), ns, gw, want,
			testPollInterval, testWaitTimeout, nil)
		require.Error(t, err, "an incomplete rollout must not be reported as converged")
	})

	t.Run("re-enumerates pods so a roll's replacements are seen", func(t *testing.T) {
		// Proves the pod set is read fresh each poll rather than snapshotted:
		// the original pod is replaced by a new one mid-wait.
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 1),
			gatewayPodWithIP("gw-old", ns, gw, "uid-old", "10.0.1.1", true),
		)
		prober := perPodProber(map[string]string{"gw-new": want}, "kcp-old")

		go func() {
			time.Sleep(40 * time.Millisecond)
			_ = cs.CoreV1().Pods(ns).Delete(context.Background(), "gw-old", metav1.DeleteOptions{})
			_, _ = cs.CoreV1().Pods(ns).Create(context.Background(),
				gatewayPodWithIP("gw-new", ns, gw, "uid-new", "10.0.1.9", true), metav1.CreateOptions{})
		}()

		err := waitForGatewayConfigID(context.Background(), cs, prober, ns, gw, want,
			testPollInterval, 2*time.Second, nil)
		require.NoError(t, err)
	})

	t.Run("a 404 from any pod fails fast rather than waiting", func(t *testing.T) {
		// The gateway image predates /config. That will never become a 200, so
		// waiting out the timeout only delays the real message.
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 1),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)
		prober := func(_ context.Context, _ GatewayPodEndpoint) (ProbeResult, error) {
			return ProbeResult{Outcome: ProbeEndpointAbsent}, nil
		}

		start := time.Now()
		err := waitForGatewayConfigID(context.Background(), cs, prober, ns, gw, want,
			testPollInterval, 10*time.Second, nil)
		require.Error(t, err)
		assert.Less(t, time.Since(start), 2*time.Second, "must not wait out the timeout")
		assert.Contains(t, err.Error(), "/config")
	})

	t.Run("a timeout is reported as a failure, not as still propagating", func(t *testing.T) {
		// A rejected CFK canary is indistinguishable from in-progress until the
		// deadline, so the message must not imply it may still succeed.
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 1),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)

		err := waitForGatewayConfigID(context.Background(), cs, staticProber("kcp-old"), ns, gw, want,
			testPollInterval, 50*time.Millisecond, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timed out")
	})

	t.Run("the timeout error reports counts, not pod names", func(t *testing.T) {
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 2),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "10.0.1.2", true),
		)

		err := waitForGatewayConfigID(context.Background(), cs, staticProber("kcp-old"), ns, gw, want,
			testPollInterval, 50*time.Millisecond, nil)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "gw-1")
		assert.NotContains(t, err.Error(), "10.0.1.1")
	})

	t.Run("an unreachable pod keeps waiting rather than failing fast", func(t *testing.T) {
		// A pod that has just gone Ready can briefly refuse the connection. The
		// terminal reachability verdict belongs to the init-time capability probe,
		// not to a mid-migration wait that would flap on a transient.
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 1),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)
		probeCount := 0
		prober := func(_ context.Context, _ GatewayPodEndpoint) (ProbeResult, error) {
			probeCount++
			return ProbeResult{Outcome: ProbeUnreachable, Err: fmt.Errorf("connection refused")}, nil
		}

		err := waitForGatewayConfigID(context.Background(), cs, prober, ns, gw, want,
			testPollInterval, 50*time.Millisecond, nil)
		require.Error(t, err)
		assert.Greater(t, probeCount, 1, "must retry an unreachable pod")
	})

	t.Run("a cancelled context aborts", func(t *testing.T) {
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 1),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := waitForGatewayConfigID(ctx, cs, staticProber("kcp-old"), ns, gw, want,
			testPollInterval, 0, nil)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("a prober error aborts the wait", func(t *testing.T) {
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 1),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)
		prober := func(_ context.Context, _ GatewayPodEndpoint) (ProbeResult, error) {
			return ProbeResult{}, context.Canceled
		}

		err := waitForGatewayConfigID(context.Background(), cs, prober, ns, gw, want,
			testPollInterval, testWaitTimeout, nil)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("reports progress with monotonically increasing elapsed", func(t *testing.T) {
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 2),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "10.0.1.2", true),
		)
		prober := perPodProber(map[string]string{"gw-1": want}, "kcp-old")

		var got []ConfigWaitProgress
		_ = waitForGatewayConfigID(context.Background(), cs, prober, ns, gw, want,
			testPollInterval, 60*time.Millisecond, func(p ConfigWaitProgress) {
				got = append(got, p)
			})

		require.NotEmpty(t, got)
		for i, p := range got {
			assert.Equal(t, want, p.Want)
			assert.Equal(t, 2, p.PodsReady)
			assert.Equal(t, 1, p.PodsAtWant)
			assert.False(t, p.Converged)
			if i > 0 {
				assert.GreaterOrEqual(t, p.Elapsed, got[i-1].Elapsed)
			}
		}
	})

	t.Run("reports a converged progress update on success", func(t *testing.T) {
		cs := newFakeClientset(
			completeGatewayDeployment(gw, ns, 1),
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
		)

		var last ConfigWaitProgress
		err := waitForGatewayConfigID(context.Background(), cs, staticProber(want), ns, gw, want,
			testPollInterval, testWaitTimeout, func(p ConfigWaitProgress) { last = p })
		require.NoError(t, err)

		assert.True(t, last.Converged)
		assert.True(t, last.RolloutComplete)
		assert.Equal(t, 1, last.PodsAtWant)
	})

	t.Run("rejects an empty wanted configId", func(t *testing.T) {
		// Waiting for "" would match a gateway that has never applied a revision
		// and pass immediately.
		cs := newFakeClientset(completeGatewayDeployment(gw, ns, 1))

		err := waitForGatewayConfigID(context.Background(), cs, staticProber(""), ns, gw, "",
			testPollInterval, testWaitTimeout, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configId")
	})
}
