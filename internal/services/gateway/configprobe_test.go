package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
)

// fakeProxyResponse implements restclient.ResponseWrapper so a test can script
// what a pod's proxied /config answer looks like without a real API server.
type fakeProxyResponse struct {
	body []byte
	err  error
}

func (r fakeProxyResponse) DoRaw(context.Context) ([]byte, error) { return r.body, r.err }
func (r fakeProxyResponse) Stream(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r.body)), r.err
}

// proxyPods builds a fake clientset's Pods(ns), wired so every ProxyGet call
// returns resp. When capture is non-nil, the action it was called with is
// stashed there so a test can assert exactly which pod/port/path it targeted.
func proxyPods(ns string, resp fakeProxyResponse, capture *ktesting.ProxyGetActionImpl) typedcorev1.PodInterface {
	cs := newFakeClientset().(*kubernetesfake.Clientset)
	cs.PrependProxyReactor("pods", func(action ktesting.Action) (bool, restclient.ResponseWrapper, error) {
		if capture != nil {
			*capture = action.(ktesting.ProxyGetActionImpl)
		}
		return true, resp, nil
	})
	return cs.CoreV1().Pods(ns)
}

func TestProbeGatewayConfig(t *testing.T) {
	const ns, podName, port = "confluent", "gw-1", 9180

	t.Run("reaches the pod via the API server's proxy subresource", func(t *testing.T) {
		// The fix this whole change exists for: kcp must never need a network
		// route into the pod CIDR, so the probe has to go through the API
		// server's pods/proxy subresource rather than dialing the pod's IP.
		var captured ktesting.ProxyGetActionImpl
		pods := proxyPods(ns, fakeProxyResponse{body: []byte(`{"configId":"x"}`)}, &captured)

		_, err := probeGatewayConfig(context.Background(), pods, podName, port)
		require.NoError(t, err)

		assert.Equal(t, "pods", captured.GetResource().Resource)
		assert.Equal(t, podName, captured.GetName())
		assert.Equal(t, "9180", captured.GetPort())
		assert.Equal(t, GatewayConfigEndpointPath, captured.GetPath())
	})

	t.Run("a configId reports it as applied", func(t *testing.T) {
		pods := proxyPods(ns, fakeProxyResponse{
			body: []byte(`{"configId":"kcp-abc123","appliedAt":"2026-07-06T10:15:30.123Z"}`),
		}, nil)

		got, err := probeGatewayConfig(context.Background(), pods, podName, port)
		require.NoError(t, err)

		assert.Equal(t, ProbeApplied, got.Outcome)
		assert.Equal(t, "kcp-abc123", got.ConfigID)
		assert.Equal(t, 2026, got.AppliedAt.Year())
		assert.Equal(t, time.July, got.AppliedAt.Month())
	})

	t.Run("a null configId means never set", func(t *testing.T) {
		// The contract's documented state for a gateway that has never been
		// given a revision. Expected on a first run, and not a failure.
		pods := proxyPods(ns, fakeProxyResponse{body: []byte(`{"configId":null,"appliedAt":null}`)}, nil)

		got, err := probeGatewayConfig(context.Background(), pods, podName, port)
		require.NoError(t, err)

		assert.Equal(t, ProbeNeverSet, got.Outcome)
		assert.Empty(t, got.ConfigID)
	})

	t.Run("no configId field means never set", func(t *testing.T) {
		pods := proxyPods(ns, fakeProxyResponse{body: []byte(`{}`)}, nil)

		got, err := probeGatewayConfig(context.Background(), pods, podName, port)
		require.NoError(t, err)
		assert.Equal(t, ProbeNeverSet, got.Outcome)
	})

	t.Run("a 404 means the gateway image predates the endpoint", func(t *testing.T) {
		// A capability signal, not an environment one: /config arrived in
		// gateway 1.3.0, so a 404 says the image is too old rather than
		// unreachable. The API server relays the pod's 404 as a NotFound
		// StatusError from DoRaw.
		notFound := apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, podName)
		pods := proxyPods(ns, fakeProxyResponse{err: notFound}, nil)

		got, err := probeGatewayConfig(context.Background(), pods, podName, port)
		require.NoError(t, err)

		assert.Equal(t, ProbeEndpointAbsent, got.Outcome)
	})

	t.Run("any other proxy failure is unreachable", func(t *testing.T) {
		// Covers both a genuinely unreachable pod and the API server's own
		// proxy backend error — the two are not reliably distinguishable
		// once the request goes through the API server, and nothing
		// downstream treats them differently.
		pods := proxyPods(ns, fakeProxyResponse{err: errors.New("dial tcp 10.244.0.44:9180: connect: connection refused")}, nil)

		got, err := probeGatewayConfig(context.Background(), pods, podName, port)
		require.NoError(t, err, "a proxy failure is an outcome, not a hard error")

		assert.Equal(t, ProbeUnreachable, got.Outcome)
		require.Error(t, got.Err)
	})

	t.Run("an unparseable body is unexpected", func(t *testing.T) {
		pods := proxyPods(ns, fakeProxyResponse{body: []byte(`<html>not json</html>`)}, nil)

		got, err := probeGatewayConfig(context.Background(), pods, podName, port)
		require.NoError(t, err)

		assert.Equal(t, ProbeUnexpected, got.Outcome)
		require.Error(t, got.Err)
	})

	t.Run("a malformed appliedAt does not invalidate the configId", func(t *testing.T) {
		// appliedAt is informational in the contract; configId is what
		// verifies a transition. A bad timestamp must not fail a switchover.
		pods := proxyPods(ns, fakeProxyResponse{
			body: []byte(`{"configId":"kcp-abc123","appliedAt":"not-a-timestamp"}`),
		}, nil)

		got, err := probeGatewayConfig(context.Background(), pods, podName, port)
		require.NoError(t, err)

		assert.Equal(t, ProbeApplied, got.Outcome)
		assert.Equal(t, "kcp-abc123", got.ConfigID)
		assert.True(t, got.AppliedAt.IsZero())
	})

	t.Run("a cancelled context is a hard error, not an outcome", func(t *testing.T) {
		// Cancellation means stop the whole wait, not "this pod is unreachable".
		pods := proxyPods(ns, fakeProxyResponse{body: []byte(`{"configId":"x"}`)}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := probeGatewayConfig(ctx, pods, podName, port)
		require.Error(t, err)
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

	t.Run("returns ready pods by name", func(t *testing.T) {
		cs := newFakeClientset(
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "10.0.1.2", true),
		)

		got, err := listGatewayPodEndpoints(context.Background(), cs, ns, gw)
		require.NoError(t, err)
		require.Len(t, got, 2)

		names := []string{got[0].Name, got[1].Name}
		assert.ElementsMatch(t, []string{"gw-1", "gw-2"}, names)
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
		// A freshly created pod has no PodIP, which still means "not yet
		// scheduled" regardless of how the endpoint is later dialled.
		cs := newFakeClientset(
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("gw-2", ns, gw, "uid-2", "", false),
		)

		got, err := listGatewayPodEndpoints(context.Background(), cs, ns, gw)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "gw-1", got[0].Name)
	})

	t.Run("selects only this gateway's pods", func(t *testing.T) {
		cs := newFakeClientset(
			gatewayPodWithIP("gw-1", ns, gw, "uid-1", "10.0.1.1", true),
			gatewayPodWithIP("other-1", ns, "other-gateway", "uid-9", "10.0.9.9", true),
		)

		got, err := listGatewayPodEndpoints(context.Background(), cs, ns, gw)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "gw-1", got[0].Name)
	})

	t.Run("no pods is not an error", func(t *testing.T) {
		cs := newFakeClientset()

		got, err := listGatewayPodEndpoints(context.Background(), cs, ns, gw)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
