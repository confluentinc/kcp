package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// serveConfig starts a stub gateway config endpoint and returns its host:port.
func serveConfig(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// deadAddr returns a host:port that nothing is listening on.
func deadAddr(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.Listener.Addr().String()
	srv.Close()
	return addr
}

func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestProbeGatewayConfig(t *testing.T) {
	client := newConfigProbeClient(2 * time.Second)

	t.Run("200 with a configId reports it as applied", func(t *testing.T) {
		addr := serveConfig(t, jsonHandler(http.StatusOK,
			`{"configId":"kcp-abc123","appliedAt":"2026-07-06T10:15:30.123Z"}`))

		got, err := probeGatewayConfig(context.Background(), client, addr)
		require.NoError(t, err)

		assert.Equal(t, ProbeApplied, got.Outcome)
		assert.Equal(t, "kcp-abc123", got.ConfigID)
		assert.Equal(t, 2026, got.AppliedAt.Year())
		assert.Equal(t, time.July, got.AppliedAt.Month())
	})

	t.Run("hits the /config path", func(t *testing.T) {
		var path string
		addr := serveConfig(t, func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			_, _ = w.Write([]byte(`{"configId":"x"}`))
		})

		_, err := probeGatewayConfig(context.Background(), client, addr)
		require.NoError(t, err)
		assert.Equal(t, GatewayConfigEndpointPath, path)
	})

	t.Run("200 with a null configId means never set", func(t *testing.T) {
		// The contract's documented state for a gateway that has never been given
		// a revision. Expected on a first run, and not a failure.
		addr := serveConfig(t, jsonHandler(http.StatusOK, `{"configId":null,"appliedAt":null}`))

		got, err := probeGatewayConfig(context.Background(), client, addr)
		require.NoError(t, err)

		assert.Equal(t, ProbeNeverSet, got.Outcome)
		assert.Empty(t, got.ConfigID)
	})

	t.Run("200 with no configId field means never set", func(t *testing.T) {
		addr := serveConfig(t, jsonHandler(http.StatusOK, `{}`))

		got, err := probeGatewayConfig(context.Background(), client, addr)
		require.NoError(t, err)
		assert.Equal(t, ProbeNeverSet, got.Outcome)
	})

	t.Run("404 means the gateway image predates the endpoint", func(t *testing.T) {
		// A capability signal, not an environment one: /config arrived in gateway
		// 1.3.0, so a 404 says the image is too old rather than unreachable.
		addr := serveConfig(t, jsonHandler(http.StatusNotFound, `not found`))

		got, err := probeGatewayConfig(context.Background(), client, addr)
		require.NoError(t, err)

		assert.Equal(t, ProbeEndpointAbsent, got.Outcome)
	})

	t.Run("connection refused means unreachable", func(t *testing.T) {
		// An environment signal: the pod CIDR is not routable from here. Must be
		// distinguishable from a 404 so the operator is told the right thing.
		got, err := probeGatewayConfig(context.Background(), client, deadAddr(t))
		require.NoError(t, err, "an unreachable pod is an outcome, not a hard error")

		assert.Equal(t, ProbeUnreachable, got.Outcome)
		require.Error(t, got.Err)
	})

	t.Run("a server error is unexpected, not absent", func(t *testing.T) {
		addr := serveConfig(t, jsonHandler(http.StatusInternalServerError, `boom`))

		got, err := probeGatewayConfig(context.Background(), client, addr)
		require.NoError(t, err)

		assert.Equal(t, ProbeUnexpected, got.Outcome)
		require.Error(t, got.Err)
	})

	t.Run("an unparseable body is unexpected", func(t *testing.T) {
		addr := serveConfig(t, jsonHandler(http.StatusOK, `<html>not json</html>`))

		got, err := probeGatewayConfig(context.Background(), client, addr)
		require.NoError(t, err)

		assert.Equal(t, ProbeUnexpected, got.Outcome)
		require.Error(t, got.Err)
	})

	t.Run("a malformed appliedAt does not invalidate the configId", func(t *testing.T) {
		// appliedAt is informational in the contract; configId is what verifies a
		// transition. A bad timestamp must not fail a switchover.
		addr := serveConfig(t, jsonHandler(http.StatusOK,
			`{"configId":"kcp-abc123","appliedAt":"not-a-timestamp"}`))

		got, err := probeGatewayConfig(context.Background(), client, addr)
		require.NoError(t, err)

		assert.Equal(t, ProbeApplied, got.Outcome)
		assert.Equal(t, "kcp-abc123", got.ConfigID)
		assert.True(t, got.AppliedAt.IsZero())
	})

	t.Run("does not follow redirects", func(t *testing.T) {
		// A 30x from /config is not a valid response. Following one would let a
		// misconfigured proxy or ingress return some other pod's configId and
		// fake a successful verification.
		elsewhere := serveConfig(t, jsonHandler(http.StatusOK, `{"configId":"someone-elses-id"}`))
		addr := serveConfig(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "http://"+elsewhere+GatewayConfigEndpointPath)
			w.WriteHeader(http.StatusFound)
		})

		got, err := probeGatewayConfig(context.Background(), client, addr)
		require.NoError(t, err)

		assert.NotEqual(t, "someone-elses-id", got.ConfigID)
		assert.Equal(t, ProbeUnexpected, got.Outcome)
	})

	t.Run("a cancelled context is a hard error, not an outcome", func(t *testing.T) {
		// Cancellation means stop the whole wait, not "this pod is unreachable".
		addr := serveConfig(t, jsonHandler(http.StatusOK, `{"configId":"x"}`))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := probeGatewayConfig(ctx, client, addr)
		require.Error(t, err)
	})
}

func TestGatewayConfigAddr(t *testing.T) {
	t.Run("ipv4", func(t *testing.T) {
		assert.Equal(t, "10.0.1.5:9180", gatewayConfigAddr("10.0.1.5", 9180))
	})

	t.Run("ipv6 is bracketed", func(t *testing.T) {
		// Without net.JoinHostPort this would produce an unparseable URL.
		assert.Equal(t, "[fd00::1]:9180", gatewayConfigAddr("fd00::1", 9180))
	})

	t.Run("honours a non-default port", func(t *testing.T) {
		assert.Equal(t, "10.0.1.5:19180", gatewayConfigAddr("10.0.1.5", 19180))
	})
}

// gatewayPodWithIP builds a gateway pod carrying a pod IP.
func gatewayPodWithIP(name, namespace, gatewayName, uid, ip string, ready bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(uid),
			Labels:    map[string]string{"app": gatewayName},
		},
	}
	pod.Status.PodIP = ip
	if ready {
		pod.Status.Phase = corev1.PodRunning
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	} else {
		pod.Status.Phase = corev1.PodPending
	}
	return pod
}

func TestListGatewayPodEndpoints(t *testing.T) {
	const ns, gw = "confluent", "test-gateway"

	t.Run("returns ready pods with their IPs", func(t *testing.T) {
		cs := newFakeClientset(
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "10.0.1.2", true),
		)

		got, err := listGatewayPodEndpoints(context.Background(), cs, ns, gw)
		require.NoError(t, err)
		require.Len(t, got, 2)

		ips := []string{got[0].IP, got[1].IP}
		assert.ElementsMatch(t, []string{"10.0.1.1", "10.0.1.2"}, ips)
		assert.True(t, got[0].Ready)
	})

	t.Run("reports not-ready pods rather than hiding them", func(t *testing.T) {
		// The caller needs the full picture: during a roll a not-ready pod is
		// expected, but a pod set with no ready members must never read as success.
		cs := newFakeClientset(
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "10.0.1.2", false),
		)

		got, err := listGatewayPodEndpoints(context.Background(), cs, ns, gw)
		require.NoError(t, err)
		require.Len(t, got, 2)

		readyCount := 0
		for _, e := range got {
			if e.Ready {
				readyCount++
			}
		}
		assert.Equal(t, 1, readyCount)
	})

	t.Run("skips pods with no IP assigned yet", func(t *testing.T) {
		// A freshly created pod has no PodIP; there is nothing to dial.
		cs := newFakeClientset(
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "", false),
		)

		got, err := listGatewayPodEndpoints(context.Background(), cs, ns, gw)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "10.0.1.1", got[0].IP)
	})

	t.Run("selects only this gateway's pods", func(t *testing.T) {
		cs := newFakeClientset(
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("other-1", ns, "other-gateway", "uid-9", "10.0.9.9", true),
		)

		got, err := listGatewayPodEndpoints(context.Background(), cs, ns, gw)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "10.0.1.1", got[0].IP)
	})

	t.Run("no pods is not an error", func(t *testing.T) {
		cs := newFakeClientset()

		got, err := listGatewayPodEndpoints(context.Background(), cs, ns, gw)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
