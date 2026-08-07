package gateway

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// Gate 3 of the capability ladder: do the running pods actually serve
// GET /config? The operator and the gateway are versioned independently, so a
// CRD that declares spec.configId says nothing about the image the pods run.
//
// A capable CRD and an enabled hot-reload flag are the premise of every case
// here — the endpoint is only probed once the cheaper gates have passed.
func capableCluster(namespace, gatewayName string) *dynamicfake.FakeDynamicClient {
	return newFakeDynamicClientWithCRD(
		newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload"}}),
		newGatewayCRWithHotReload(gatewayName, namespace, boolPtr(true)),
	)
}

// mixedProber answers each pod by name, defaulting to notFound.
func mixedProber(byPod map[string]ProbeOutcome, fallback ProbeOutcome) podProber {
	return func(_ context.Context, endpoint GatewayPodEndpoint) (ProbeResult, error) {
		outcome, ok := byPod[endpoint.Name]
		if !ok {
			outcome = fallback
		}
		return ProbeResult{Outcome: outcome, Addr: endpoint.IP, ConfigID: "rev-1"}, nil
	}
}

func TestDetectCapability_ConfigEndpointGate(t *testing.T) {
	ns, gw := "confluent", "test-gateway"

	t.Run("endpoint served - per-pod configId", func(t *testing.T) {
		got, err := detectCapability(context.Background(), capableCluster(ns, gw),
			servingPods(ns, gw, 2), probeStub(ProbeApplied), ns, gw)
		require.NoError(t, err)

		assert.Equal(t, VerifyPerPodConfigID, got.Mode)
		assert.True(t, got.ConfigEndpointServed)
		assert.Empty(t, got.Advisory)
	})

	t.Run("endpoint live but never set - still capable", func(t *testing.T) {
		// configId: null means the endpoint works and no revision has been
		// applied yet, which is exactly the state of a freshly-created gateway.
		got, err := detectCapability(context.Background(), capableCluster(ns, gw),
			servingPods(ns, gw, 2), probeStub(ProbeNeverSet), ns, gw)
		require.NoError(t, err)

		assert.Equal(t, VerifyPerPodConfigID, got.Mode)
		assert.True(t, got.ConfigEndpointServed)
	})

	t.Run("unanimous 404 - refuses, because rollout verification would be vacuous", func(t *testing.T) {
		// The gateway image predates 1.3.0 while hot-reload is on: CFK renders the
		// new config, projects it, and reports success, but the gateway never
		// applies it and no pod rolls. Downgrading here would verify a fence by
		// watching for a rollout that cannot happen.
		_, err := detectCapability(context.Background(), capableCluster(ns, gw),
			servingPods(ns, gw, 3), probeStub(ProbeEndpointAbsent), ns, gw)
		require.Error(t, err, "no /config and no roll leaves nothing to verify against")

		assert.Contains(t, err.Error(), "GET /config")
		assert.Contains(t, err.Error(), "1.3.0")
		assert.Contains(t, err.Error(), "spec.hotReload.enabled: false")
	})

	t.Run("mixed 404 and 200 - capable, because an image change is itself a roll", func(t *testing.T) {
		got, err := detectCapability(context.Background(), capableCluster(ns, gw),
			servingPods(ns, gw, 2),
			mixedProber(map[string]ProbeOutcome{"test-gateway-0": ProbeEndpointAbsent}, ProbeApplied),
			ns, gw)
		require.NoError(t, err)

		assert.Equal(t, VerifyPerPodConfigID, got.Mode,
			"one pod already serves the endpoint, so the image roll in flight is bringing the rest")
	})

	t.Run("nothing reachable - error naming pod routing", func(t *testing.T) {
		_, err := detectCapability(context.Background(), capableCluster(ns, gw),
			servingPods(ns, gw, 2), probeStub(ProbeUnreachable), ns, gw)
		require.Error(t, err, "unreachable pods are an environment problem, not a reason to silently downgrade")

		assert.Contains(t, err.Error(), "route pod IPs")
		assert.Contains(t, err.Error(), "2 ready gateway pods")
		assert.NotContains(t, err.Error(), "10.0.1.", "pod IPs belong in the log, not the error")
	})

	t.Run("no ready pods - error rather than a guess", func(t *testing.T) {
		_, err := detectCapability(context.Background(), capableCluster(ns, gw),
			newFakeClientset(), probeStub(ProbeApplied), ns, gw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no ready pods")
	})

	t.Run("not-ready pods are not probed", func(t *testing.T) {
		// A pod still starting has nothing meaningful to report. If it were
		// counted, a rolling gateway would read as unreachable.
		objs := []runtime.Object{
			gatewayPodWithIP("gw-ready", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-starting", ns, gw, "uid-2", "10.0.1.2", false),
		}
		probed := map[string]int{}
		probe := func(_ context.Context, endpoint GatewayPodEndpoint) (ProbeResult, error) {
			probed[endpoint.Name]++
			return ProbeResult{Outcome: ProbeApplied}, nil
		}

		got, err := detectCapability(context.Background(), capableCluster(ns, gw),
			newFakeClientset(objs...), probe, ns, gw)
		require.NoError(t, err)

		assert.Equal(t, VerifyPerPodConfigID, got.Mode)
		assert.Equal(t, 1, probed["gw-ready"])
		assert.Zero(t, probed["gw-starting"])
	})

	t.Run("the endpoint is not probed when a cheaper gate already failed", func(t *testing.T) {
		// Ordering matters: the refusal an operator sees should name the gate that
		// actually decided, not a network error from a probe kcp had no reason to
		// make.
		noConfigID := newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"replicas"}}),
			newGatewayCRWithHotReload(gw, ns, boolPtr(true)),
		)
		probed := 0
		probe := func(context.Context, GatewayPodEndpoint) (ProbeResult, error) {
			probed++
			return ProbeResult{}, fmt.Errorf("probe must not run")
		}

		_, err := detectCapability(context.Background(), noConfigID, servingPods(ns, gw, 2), probe, ns, gw)
		require.Error(t, err)

		assert.Contains(t, err.Error(), "spec.configId")
		assert.Zero(t, probed, "the endpoint gate must not run once a cheaper gate has decided")
	})

	t.Run("hot-reload disabled also skips the probe", func(t *testing.T) {
		disabled := newFakeDynamicClientWithCRD(
			newGatewayCRD(map[string][]string{"v1beta1": {"configId", "hotReload"}}),
			newGatewayCRWithHotReload(gw, ns, boolPtr(false)),
		)
		probed := 0
		probe := func(context.Context, GatewayPodEndpoint) (ProbeResult, error) {
			probed++
			return ProbeResult{}, fmt.Errorf("probe must not run")
		}

		got, err := detectCapability(context.Background(), disabled, servingPods(ns, gw, 2), probe, ns, gw)
		require.NoError(t, err)

		assert.Equal(t, VerifyRollout, got.Mode)
		assert.Equal(t, advisoryHotReloadDisabled, got.Advisory)
		assert.Zero(t, probed)
	})
}
