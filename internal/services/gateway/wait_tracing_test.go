package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
)

// rolledDeployment is a complete Deployment whose generation is past the
// baseline of 1 that the tests pass, so the wait sees a rollout.
func rolledDeployment(gw, ns string) *appsv1.Deployment {
	return newGatewayDeployment(gw, ns, 2,
		withObservedGeneration(2), withReplicas(1), withUpdatedReplicas(1),
		withAvailableReplicas(1), withReadyReplicas(1))
}

func TestTracing_WaitForGatewayPods_TimesTheWaitForOldPodsToGo(t *testing.T) {
	rec := tracingtest.Install(t)
	const ns, gw = "test-ns", "test-gw"
	cs := newFakeClientset(
		newGatewayPod("gw-old", ns, gw, "old-1", true),
		newGatewayPod("gw-new", ns, gw, "new-1", true),
		rolledDeployment(gw, ns),
	)
	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = cs.CoreV1().Pods(ns).Delete(context.Background(), "gw-old", metav1.DeleteOptions{})
	}()

	ctx, parent := tracing.Start(context.Background(), tracerName, "gateway.wait_pods")
	err := waitForGatewayPods(ctx, cs, ns, gw, map[types.UID]struct{}{"old-1": {}}, 1, 5*time.Millisecond, 5*time.Second, nil)
	tracing.End(parent, err)
	require.NoError(t, err)

	spans := tracingtest.Spans(rec)
	gone := tracingtest.Find(t, spans, "gateway.wait_old_pods_gone")
	assert.Contains(t, tracingtest.Names(tracingtest.Children(spans, tracingtest.Find(t, spans, "gateway.wait_pods"))), "gateway.wait_old_pods_gone")
	assert.Equal(t, codes.Unset, gone.Status().Code)
	assert.GreaterOrEqual(t, gone.EndTime().Sub(gone.StartTime()), 50*time.Millisecond, "the span covers the time the old pod lingered after the rollout completed")
	atStart, _ := tracingtest.Attr(gone, "kcp.gateway.pods.old_at_start")
	assert.Equal(t, int64(1), atStart.AsInt64())
	captured, _ := tracingtest.Attr(tracingtest.Find(t, spans, "gateway.wait_pods"), "kcp.gateway.pods.captured")
	assert.Equal(t, int64(1), captured.AsInt64())
	tracingtest.AssertNoValues(t, spans, "gw-old", "gw-new", "old-1", "new-1")
}

func TestTracing_WaitForGatewayPods_NoSpanWhenOldPodsAlreadyGone(t *testing.T) {
	rec := tracingtest.Install(t)
	const ns, gw = "test-ns", "test-gw"
	cs := newFakeClientset(newGatewayPod("gw-new", ns, gw, "new-1", true), rolledDeployment(gw, ns))

	err := waitForGatewayPods(context.Background(), cs, ns, gw, map[types.UID]struct{}{"old-1": {}}, 1, 5*time.Millisecond, 5*time.Second, nil)
	require.NoError(t, err)
	assert.Empty(t, tracingtest.FindAll(tracingtest.Spans(rec), "gateway.wait_old_pods_gone"), "no extra wait, so no span")
}

func TestTracing_WaitForGatewayPods_TimeoutEndsTheSpanWithTheError(t *testing.T) {
	rec := tracingtest.Install(t)
	const ns, gw = "test-ns", "test-gw"
	cs := newFakeClientset(
		newGatewayPod("gw-old", ns, gw, "old-1", true),
		newGatewayPod("gw-new", ns, gw, "new-1", true),
		rolledDeployment(gw, ns),
	)

	err := waitForGatewayPods(context.Background(), cs, ns, gw, map[types.UID]struct{}{"old-1": {}}, 1, 5*time.Millisecond, 60*time.Millisecond, nil)
	require.Error(t, err)

	gone := tracingtest.Find(t, tracingtest.Spans(rec), "gateway.wait_old_pods_gone")
	assert.Equal(t, codes.Error, gone.Status().Code, "a timed-out wait must close its span as failed")
}

func TestTracing_WaitForGatewayConfigID_TimesTheWaitForTerminatingPods(t *testing.T) {
	rec := tracingtest.Install(t)
	const ns, gw, want = "confluent", "test-gateway", "kcp-new"
	cs := newFakeClientset(
		completeGatewayDeployment(gw, ns, 1),
		gatewayPodWithIP("gw-new", ns, gw, "uid-new", "10.0.1.1", true),
		terminatingGatewayPod("gw-old", ns, gw, "uid-old", "10.0.1.2"),
	)
	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = cs.CoreV1().Pods(ns).Delete(context.Background(), "gw-old", metav1.DeleteOptions{})
	}()

	ctx, parent := tracing.Start(context.Background(), tracerName, "gateway.wait_config_applied")
	err := waitForGatewayConfigID(ctx, cs, staticProber(want), ns, gw,
		ConfigWaitOptions{ConfigID: want, PollInterval: testPollInterval, HotReloadTimeout: 5 * time.Second})
	tracing.End(parent, err)
	require.NoError(t, err)

	spans := tracingtest.Spans(rec)
	wait := tracingtest.Find(t, spans, "gateway.wait_terminating_pods")
	assert.Contains(t, tracingtest.Names(tracingtest.Children(spans, tracingtest.Find(t, spans, "gateway.wait_config_applied"))), "gateway.wait_terminating_pods")
	assert.GreaterOrEqual(t, wait.EndTime().Sub(wait.StartTime()), 50*time.Millisecond)
	atStart, _ := tracingtest.Attr(wait, "kcp.gateway.pods.terminating_at_start")
	assert.Equal(t, int64(1), atStart.AsInt64())
	tracingtest.AssertNoValues(t, spans, "gw-old", "gw-new", "uid-old", "uid-new")
}

func TestTracing_WaitForGatewayConfigID_NoSpanWhenNothingIsTerminating(t *testing.T) {
	rec := tracingtest.Install(t)
	const ns, gw, want = "confluent", "test-gateway", "kcp-new"
	cs := newFakeClientset(
		completeGatewayDeployment(gw, ns, 1),
		gatewayPodWithIP("gw-new", ns, gw, "uid-new", "10.0.1.1", true),
	)

	err := waitForGatewayConfigID(context.Background(), cs, staticProber(want), ns, gw,
		ConfigWaitOptions{ConfigID: want, PollInterval: testPollInterval, HotReloadTimeout: testWaitTimeout})
	require.NoError(t, err)
	assert.Empty(t, tracingtest.FindAll(tracingtest.Spans(rec), "gateway.wait_terminating_pods"), "a hot reload that rolls nothing pays no extra wait, so no span")
}

func TestTracing_WaitForGatewayConfigID_TimeoutEndsTheSpanWithTheError(t *testing.T) {
	rec := tracingtest.Install(t)
	const ns, gw, want = "confluent", "test-gateway", "kcp-new"
	cs := newFakeClientset(
		completeGatewayDeployment(gw, ns, 1),
		gatewayPodWithIP("gw-new", ns, gw, "uid-new", "10.0.1.1", true),
		terminatingGatewayPod("gw-old", ns, gw, "uid-old", "10.0.1.2"),
	)

	err := waitForGatewayConfigID(context.Background(), cs, staticProber(want), ns, gw,
		ConfigWaitOptions{ConfigID: want, PollInterval: testPollInterval, HotReloadTimeout: 60 * time.Millisecond})
	require.Error(t, err)

	wait := tracingtest.Find(t, tracingtest.Spans(rec), "gateway.wait_terminating_pods")
	assert.Equal(t, codes.Error, wait.Status().Code)
}
