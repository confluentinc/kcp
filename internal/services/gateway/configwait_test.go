package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
)

// testGateway and testNamespace are declared in validate_test.go.
const testPort = "9180"

// --- fakes ------------------------------------------------------------------

// fakeResponseWrapper satisfies rest.ResponseWrapper. The fake clientset's
// ProxyGet returns a nil ResponseWrapper unless a proxy reactor supplies one,
// and a reactor that returns a non-nil error is skipped entirely (leaving nil),
// so transport failures must be modelled as a wrapper whose DoRaw fails rather
// than as a reactor error.
type fakeResponseWrapper struct {
	body []byte
	err  error
}

func (f *fakeResponseWrapper) DoRaw(context.Context) ([]byte, error) {
	return f.body, f.err
}

func (f *fakeResponseWrapper) Stream(context.Context) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

// proxyStub answers GET /config per pod, tracking how many times each pod has
// been asked so tests can converge deterministically on the Nth poll instead of
// racing a wall clock.
type proxyStub struct {
	mu     sync.Mutex
	counts map[string]int
	answer func(pod string, nth int) (body string, err error)
	seen   []string
}

func newProxyStub(answer func(pod string, nth int) (string, error)) *proxyStub {
	return &proxyStub{counts: map[string]int{}, answer: answer}
}

func (p *proxyStub) install(cs *kubernetesfake.Clientset) {
	cs.PrependProxyReactor("pods", func(action ktesting.Action) (bool, rest.ResponseWrapper, error) {
		get, ok := action.(ktesting.ProxyGetActionImpl)
		if !ok {
			return false, nil, nil
		}

		p.mu.Lock()
		p.counts[get.Name]++
		nth := p.counts[get.Name]
		p.seen = append(p.seen, fmt.Sprintf("%s:%s%s", get.Name, get.Port, get.Path))
		p.mu.Unlock()

		body, err := p.answer(get.Name, nth)
		return true, &fakeResponseWrapper{body: []byte(body), err: err}, nil
	})
}

func (p *proxyStub) callsFor(pod string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[pod]
}

func (p *proxyStub) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

func configBody(id string) string {
	return fmt.Sprintf(`{"configId":%q,"appliedAt":"2026-08-03T10:53:18.507420686Z"}`, id)
}

// --- object builders --------------------------------------------------------

func labelledPod(name string, phase corev1.PodPhase, ready bool) *corev1.Pod {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{"app": testGateway},
		},
		Status: corev1.PodStatus{
			Phase: phase,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			}},
		},
	}
	if ready {
		p.Status.Conditions[0].Status = corev1.ConditionTrue
	}
	return &p
}

func readyPods(names ...string) []runtime.Object {
	objs := make([]runtime.Object, 0, len(names))
	for _, n := range names {
		objs = append(objs, labelledPod(n, corev1.PodRunning, true))
	}
	return objs
}

func gatewayDeployment(readyReplicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: testGateway, Namespace: testNamespace},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: readyReplicas},
	}
}

// gatewayCRWithConditions builds a Gateway CR carrying status.conditions.
func gatewayCRWithConditions(conds ...map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: GatewayGroup, Version: GatewayVersion, Kind: "Gateway",
	})
	obj.SetName(testGateway)
	obj.SetNamespace(testNamespace)
	obj.SetGeneration(30)

	list := make([]any, 0, len(conds))
	for _, c := range conds {
		list = append(list, c)
	}
	if err := unstructured.SetNestedSlice(obj.Object, list, "status", "conditions"); err != nil {
		panic(err)
	}
	return obj
}

func healthyCR() *unstructured.Unstructured {
	return gatewayCRWithConditions(
		cond(ConditionGarbageCollecting, "False", "Garbage Collection not triggered", staleGarbageCollectAt),
		cond(ConditionHotReloadStatus, "True", "Succeeded", staleHotReloadAt),
		cond(ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt),
	)
}

func fakeCS(objs ...runtime.Object) *kubernetesfake.Clientset {
	return kubernetesfake.NewSimpleClientset(objs...) //nolint:staticcheck // SA1019: matches newFakeClientset
}

// baselineOf snapshots a Gateway CR's conditions the way the production caller
// does — off the live object, immediately before the apply.
//
// Building the baseline by hand invites a fixture that is not actually a snapshot
// of anything: leave a field out and it reads as "changed" against the very CR it
// claims to describe, so a test can pass while proving nothing.
func baselineOf(t *testing.T, cr *unstructured.Unstructured) ConditionSnapshot {
	t.Helper()
	conds, err := gatewayConditions(cr.Object)
	require.NoError(t, err)
	return snapshotConditions(conds)
}

// --- happy path -------------------------------------------------------------

func TestWaitForGatewayConfigApplied_ConvergesAfterSeveralTicks(t *testing.T) {
	objs := append(readyPods("gw-a", "gw-b", "gw-c"), gatewayDeployment(3))
	cs := fakeCS(objs...)

	// Each pod reports the old id for its first two polls, then the target —
	// the measured shape, where pods flip ~7s after the apply.
	stub := newProxyStub(func(_ string, nth int) (string, error) {
		if nth <= 2 {
			return configBody("old-000"), nil
		}
		return configBody("fence-101"), nil
	})
	stub.install(cs)

	dyn := newFakeDynamicClient(healthyCR())

	err := waitForGatewayConfigApplied(context.Background(), cs, dyn,
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, 5*time.Second, nil)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, stub.callsFor("gw-a"), 3, "should have polled until the pod flipped")
}

// The endpoint must be reached on the configured port and path, via the pod
// proxy — not a Service, and not a pod IP.
func TestWaitForGatewayConfigApplied_UsesConfiguredPortAndPath(t *testing.T) {
	objs := append(readyPods("gw-a"), gatewayDeployment(1))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return configBody("t1"), nil })
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "t1", snapshotConditions(nil), "19180",
		5*time.Millisecond, time.Second, nil)
	require.NoError(t, err)

	require.NotEmpty(t, stub.requests())
	assert.Equal(t, "gw-a:19180/config", stub.requests()[0])
}

func TestWaitForGatewayConfigApplied_ReturnsImmediatelyWhenAlreadyConverged(t *testing.T) {
	objs := append(readyPods("gw-a", "gw-b", "gw-c"), gatewayDeployment(3))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return configBody("fence-101"), nil })
	stub.install(cs)

	start := time.Now()
	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		time.Second, 10*time.Second, nil)

	require.NoError(t, err)
	assert.Less(t, time.Since(start), 500*time.Millisecond,
		"an already-converged gateway must not wait a poll interval")
	assert.Equal(t, 1, stub.callsFor("gw-a"))
}

// --- the failure the whole design exists for --------------------------------

// Test B: pods hold the previous configId for the entire poll. This must fail,
// not succeed — observedGeneration would have gone green at +1.2s.
func TestWaitForGatewayConfigApplied_TimesOutWhenPodsNeverConverge(t *testing.T) {
	objs := append(readyPods("gw-a", "gw-b", "gw-c"), gatewayDeployment(3))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return configBody("fence-101"), nil })
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "badfence-102", snapshotConditions(nil), testPort,
		5*time.Millisecond, 150*time.Millisecond, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.Contains(t, err.Error(), "0 of 3", "the error should report how many pods applied")
}

// The reason string reaches the terminal, so it must carry counts, not a list
// of pod names.
func TestWaitForGatewayConfigApplied_TimeoutErrorHasNoPodNames(t *testing.T) {
	objs := append(readyPods("gw-abcdef-11111", "gw-abcdef-22222"), gatewayDeployment(2))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return configBody("old"), nil })
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "t1", snapshotConditions(nil), testPort,
		5*time.Millisecond, 100*time.Millisecond, nil)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "gw-abcdef-11111")
	assert.NotContains(t, err.Error(), "gw-abcdef-22222")
}

// --- fast-fail --------------------------------------------------------------

func TestWaitForGatewayConfigApplied_FastFailsOnNewRejection(t *testing.T) {
	objs := append(readyPods("gw-a", "gw-b", "gw-c"), gatewayDeployment(3))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return configBody("old-000"), nil })
	stub.install(cs)

	// Snapshot the gateway as it stood before the apply, then have the operator
	// publish ContainerCrashed. cluster-ready keeps its stale ApplyFailed
	// throughout, so only hot-reload-status is a verdict on this apply.
	before := baselineOf(t, healthyCR())
	crashed := gatewayCRWithConditions(
		cond(ConditionHotReloadStatus, "False", "ContainerCrashed", crashedHotReloadAt),
		cond(ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt),
	)

	start := time.Now()
	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(crashed),
		testNamespace, testGateway, "fence-101", before, testPort,
		5*time.Millisecond, 10*time.Second, nil)

	require.Error(t, err)
	var rejection *GatewayRejection
	require.True(t, errors.As(err, &rejection), "expected a GatewayRejection, got %T: %v", err, err)
	assert.Equal(t, "ContainerCrashed", rejection.Reason)
	assert.Less(t, time.Since(start), 2*time.Second,
		"a published rejection must abort rather than wait out the timeout")
}

// The guard that keeps a 6-day-stale ApplyFailed from aborting a good fence.
func TestWaitForGatewayConfigApplied_DoesNotFastFailOnStaleCondition(t *testing.T) {
	objs := append(readyPods("gw-a"), gatewayDeployment(1))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return configBody("old-000"), nil })
	stub.install(cs)

	// cluster-ready has been False/ApplyFailed since before the apply, and the
	// gateway is re-read unchanged: same status, same reason, same message, same
	// timestamp. Nothing about it is a verdict on what we just applied.
	before := baselineOf(t, healthyCR())

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", before, testPort,
		5*time.Millisecond, 100*time.Millisecond, nil)

	require.Error(t, err)
	var rejection *GatewayRejection
	assert.False(t, errors.As(err, &rejection),
		"a stale condition must not be reported as a rejection; got %v", err)
	assert.Contains(t, err.Error(), "timed out")
}

// Without a pre-apply baseline there is no way to distinguish a fresh rejection
// from a condition that has been failing for days, so the fast-fail must switch
// itself off rather than guess. The gateway here carries the real stale
// ApplyFailed; an empty baseline must NOT turn that into a rejection.
func TestWaitForGatewayConfigApplied_EmptyBaselineDisablesFastFail(t *testing.T) {
	objs := append(readyPods("gw-a"), gatewayDeployment(1))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return configBody("old-000"), nil })
	stub.install(cs)

	// hot-reload-status is False/ContainerCrashed, which with a populated
	// baseline would fast-fail immediately.
	crashed := gatewayCRWithConditions(
		cond(ConditionHotReloadStatus, "False", "ContainerCrashed", crashedHotReloadAt),
		cond(ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt),
	)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(crashed),
		testNamespace, testGateway, "fence-101", nil, testPort,
		5*time.Millisecond, 100*time.Millisecond, nil)

	require.Error(t, err)
	var rejection *GatewayRejection
	assert.False(t, errors.As(err, &rejection),
		"with no baseline the wait must fall back to /config and time out, not report a rejection; got %v", err)
	assert.Contains(t, err.Error(), "timed out")
}

// --- enumeration guards -----------------------------------------------------

// A gateway scaled to zero has no pods, making "all pods agree" vacuous.
func TestWaitForGatewayConfigApplied_ZeroEligiblePodsNeverSucceeds(t *testing.T) {
	cs := fakeCS(gatewayDeployment(0))
	stub := newProxyStub(func(string, int) (string, error) { return configBody("fence-101"), nil })
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, 80*time.Millisecond, nil)

	require.Error(t, err, "zero pods must never count as converged")
	assert.Contains(t, err.Error(), "timed out")
}

// The measured hazard: evicted pods keep the app= label and a stale podIP.
// They must be excluded, and their presence must not stall a healthy gateway.
func TestWaitForGatewayConfigApplied_IgnoresEvictedPods(t *testing.T) {
	objs := []runtime.Object{
		labelledPod("gw-live-a", corev1.PodRunning, true),
		labelledPod("gw-live-b", corev1.PodRunning, true),
		labelledPod("gw-live-c", corev1.PodRunning, true),
		labelledPod("gw-dead-a", corev1.PodFailed, false),
		labelledPod("gw-dead-b", corev1.PodFailed, false),
		labelledPod("gw-dead-c", corev1.PodFailed, false),
		gatewayDeployment(3),
	}
	cs := fakeCS(objs...)
	stub := newProxyStub(func(pod string, _ int) (string, error) {
		if pod == "gw-dead-a" || pod == "gw-dead-b" || pod == "gw-dead-c" {
			return "", errors.New("no route to host")
		}
		return configBody("fence-101"), nil
	})
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, time.Second, nil)

	require.NoError(t, err, "3 dead pods alongside 3 healthy ones must not block convergence")
	assert.Zero(t, stub.callsFor("gw-dead-a"), "evicted pods must never be probed")
}

// Mid-rollout the eligible count drifts from the Deployment's ready count.
func TestWaitForGatewayConfigApplied_WaitsWhenPodCountDisagreesWithDeployment(t *testing.T) {
	objs := append(readyPods("gw-a", "gw-b"), gatewayDeployment(3))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return configBody("fence-101"), nil })
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, 80*time.Millisecond, nil)

	require.Error(t, err, "2 agreeing pods must not satisfy a 3-replica gateway")
}

func TestWaitForGatewayConfigApplied_UnreachablePodBlocksConvergence(t *testing.T) {
	objs := append(readyPods("gw-a", "gw-b", "gw-c"), gatewayDeployment(3))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(pod string, _ int) (string, error) {
		if pod == "gw-b" {
			return "", errors.New("connection refused")
		}
		return configBody("fence-101"), nil
	})
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, 80*time.Millisecond, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreachable")
}

// An evicted pod returned HTTP 200 with an empty body through the proxy.
func TestWaitForGatewayConfigApplied_EmptyBodyBlocksConvergence(t *testing.T) {
	objs := append(readyPods("gw-a"), gatewayDeployment(1))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return "", nil })
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, 80*time.Millisecond, nil)

	require.Error(t, err, "an empty body must not read as a matching configId")
}

// When the Deployment cannot be read, fall back to "all eligible pods agree"
// rather than blocking forever on an unknown replica count.
func TestWaitForGatewayConfigApplied_MissingDeploymentFallsBackToEligibleAgreement(t *testing.T) {
	cs := fakeCS(readyPods("gw-a", "gw-b")...) // no Deployment object at all
	stub := newProxyStub(func(string, int) (string, error) { return configBody("fence-101"), nil })
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, time.Second, nil)

	require.NoError(t, err)
}

// --- ctx, progress, timeout semantics ---------------------------------------

func TestWaitForGatewayConfigApplied_ContextCancellation(t *testing.T) {
	objs := append(readyPods("gw-a"), gatewayDeployment(1))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return configBody("old-000"), nil })
	stub.install(cs)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := waitForGatewayConfigApplied(ctx, cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, 10*time.Second, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestWaitForGatewayConfigApplied_ReportsProgress(t *testing.T) {
	objs := append(readyPods("gw-a", "gw-b", "gw-c"), gatewayDeployment(3))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(_ string, nth int) (string, error) {
		if nth <= 2 {
			return configBody("old-000"), nil
		}
		return configBody("fence-101"), nil
	})
	stub.install(cs)

	var mu sync.Mutex
	var seen []ConfigApplyProgress
	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, 5*time.Second, func(p ConfigApplyProgress) {
			mu.Lock()
			seen = append(seen, p)
			mu.Unlock()
		})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, seen, "progress must be reported")

	last := seen[len(seen)-1]
	assert.Equal(t, "fence-101", last.TargetConfigID)
	assert.Equal(t, 3, last.PodsApplied)
	assert.Equal(t, 3, last.PodsTotal)
	assert.True(t, last.Converged)

	first := seen[0]
	assert.False(t, first.Converged)
	assert.Equal(t, 0, first.PodsApplied)
}

// timeout <= 0 means no deadline, consistent with the other gateway waits.
func TestWaitForGatewayConfigApplied_ZeroTimeoutMeansNoDeadline(t *testing.T) {
	objs := append(readyPods("gw-a"), gatewayDeployment(1))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(_ string, nth int) (string, error) {
		if nth < 4 {
			return configBody("old-000"), nil
		}
		return configBody("fence-101"), nil
	})
	stub.install(cs)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := waitForGatewayConfigApplied(ctx, cs, newFakeDynamicClient(healthyCR()),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, 0, nil)

	require.NoError(t, err)
}

// --- SnapshotGatewayConditions ---------------------------------------------

func TestSnapshotGatewayConditions(t *testing.T) {
	dyn := newFakeDynamicClient(healthyCR())

	snap, err := snapshotGatewayConditions(context.Background(), dyn, testNamespace, testGateway)
	require.NoError(t, err)

	assert.Equal(t, mustTime(t, staleClusterReadyAt), snap[ConditionClusterReady].LastTransitionTime)
	assert.Equal(t, mustTime(t, staleHotReloadAt), snap[ConditionHotReloadStatus].LastTransitionTime)
	assert.Equal(t, "ApplyFailed", snap[ConditionClusterReady].Reason)

	// And it behaves as the guard: the stale condition reads as unchanged.
	assert.False(t, snap.changed(condAt(t, ConditionClusterReady, "False", "ApplyFailed", staleClusterReadyAt)))
	assert.True(t, snap.changed(condAt(t, ConditionHotReloadStatus, "False", "ContainerCrashed", crashedHotReloadAt)))
}

func TestSnapshotGatewayConditions_MissingGateway(t *testing.T) {
	_, err := snapshotGatewayConditions(context.Background(), newFakeDynamicClient(), testNamespace, testGateway)
	require.Error(t, err)
}

func TestSnapshotGatewayConditions_GatewayWithoutStatus(t *testing.T) {
	dyn := newFakeDynamicClient(newGatewayCR(testGateway, testNamespace, 1, 0, false))

	snap, err := snapshotGatewayConditions(context.Background(), dyn, testNamespace, testGateway)
	require.NoError(t, err, "a CR with no status yet is normal, not an error")
	assert.Empty(t, snap)
}

// Guard against a silently-unused fake: the dynamic client really is consulted.
func TestWaitForGatewayConfigApplied_ToleratesUnreadableGatewayCR(t *testing.T) {
	objs := append(readyPods("gw-a"), gatewayDeployment(1))
	cs := fakeCS(objs...)
	stub := newProxyStub(func(string, int) (string, error) { return configBody("fence-101"), nil })
	stub.install(cs)

	// No Gateway CR seeded, so the fast-fail check cannot run. Convergence is
	// decided by /config alone and must still succeed.
	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(),
		testNamespace, testGateway, "fence-101", snapshotConditions(nil), testPort,
		5*time.Millisecond, time.Second, nil)

	require.NoError(t, err, "an unreadable CR must not block a verified convergence")
}

// ===========================================================================
// RBAC denial on the pod proxy
//
// /config is reached through the API-server pod-proxy subresource, which needs
// "get pods/proxy" in the gateway namespace. Granting "get pods" is not enough.
// A denial is the one probe failure that polling cannot resolve, so it must not
// be spent as a verification timeout with the gateway already fenced.
// ===========================================================================

func forbiddenProxyError() error {
	return apierrors.NewForbidden(
		schema.GroupResource{Resource: "pods/proxy"},
		testGateway+"-0",
		errors.New(`User "system:serviceaccount:kcp:kcp-runner" cannot get resource "pods/proxy" in API group "" in the namespace "kcp"`),
	)
}

func TestProbeGatewayPod_ForbiddenMapsToAccessDenied(t *testing.T) {
	cs := kubernetesfake.NewClientset()
	newProxyStub(func(string, int) (string, error) {
		return "", forbiddenProxyError()
	}).install(cs)

	result := probeGatewayPod(context.Background(), cs, testNamespace, testPort, testGateway+"-0")

	var denied *GatewayConfigAccessDeniedError
	require.True(t, errors.As(result.Err, &denied), "a 403 must be typed, not a generic reach failure: %v", result.Err)
	assert.Equal(t, testNamespace, denied.Namespace)
	assert.Nil(t, result.ConfigID)
}

// The message has to name the subresource and the namespace, or the reader has
// to guess what to grant and where.
func TestGatewayConfigAccessDeniedError_MessageIsActionable(t *testing.T) {
	err := &GatewayConfigAccessDeniedError{Namespace: "confluent", Err: forbiddenProxyError()}

	assert.Contains(t, err.Error(), "pods/proxy")
	assert.Contains(t, err.Error(), `"get"`)
	assert.Contains(t, err.Error(), `"confluent"`)
	assert.NotContains(t, err.Error(), "❌", "errors are data, not console output")
}

// The API error must stay reachable so callers can still inspect the raw status.
func TestGatewayConfigAccessDeniedError_UnwrapsTheAPIError(t *testing.T) {
	underlying := forbiddenProxyError()
	err := &GatewayConfigAccessDeniedError{Namespace: "confluent", Err: underlying}

	assert.True(t, apierrors.IsForbidden(errors.Unwrap(err)))
	assert.ErrorIs(t, err, underlying)
}

// A denial must abort on the first tick, not after the timeout.
func TestWaitForGatewayConfigApplied_ForbiddenFailsFast(t *testing.T) {
	cs := kubernetesfake.NewClientset(readyPods(testGateway+"-0", testGateway+"-1", testGateway+"-2")...)
	stub := newProxyStub(func(string, int) (string, error) {
		return "", forbiddenProxyError()
	})
	stub.install(cs)

	var progress []ConfigApplyProgress
	start := time.Now()
	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(), testNamespace, testGateway,
		"kcp-target", nil, testPort, 10*time.Millisecond, 5*time.Second,
		func(p ConfigApplyProgress) { progress = append(progress, p) })
	elapsed := time.Since(start)

	var denied *GatewayConfigAccessDeniedError
	require.True(t, errors.As(err, &denied), "expected a typed denial, got: %v", err)
	assert.Less(t, elapsed, time.Second, "a denial must not be waited out")
	assert.Empty(t, progress, "no progress line should precede the denial: it would blame the config, not the RBAC")
	assert.Equal(t, 1, stub.callsFor(testGateway+"-0"), "one round of probing is enough to know")
}

// One pod's denial is every pod's denial — pods/proxy is granted per namespace —
// so there is nothing to learn from asking the rest.
func TestWaitForGatewayConfigApplied_PartialForbiddenStillFailsFast(t *testing.T) {
	cs := kubernetesfake.NewClientset(readyPods(testGateway+"-0", testGateway+"-1", testGateway+"-2")...)
	stub := newProxyStub(func(pod string, _ int) (string, error) {
		if pod == testGateway+"-2" {
			return "", forbiddenProxyError()
		}
		return configBody("kcp-target"), nil
	})
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(), testNamespace, testGateway,
		"kcp-target", nil, testPort, 10*time.Millisecond, 2*time.Second, nil)

	var denied *GatewayConfigAccessDeniedError
	require.True(t, errors.As(err, &denied), "expected a typed denial, got: %v", err)
}

// The converse guard: an unreachable pod IS transient — pods vanish mid-roll —
// so it must keep polling and eventually converge rather than abort.
func TestWaitForGatewayConfigApplied_UnreachablePodKeepsPolling(t *testing.T) {
	cs := kubernetesfake.NewClientset(readyPods(testGateway + "-0")...)
	stub := newProxyStub(func(_ string, nth int) (string, error) {
		if nth < 3 {
			return "", apierrors.NewServiceUnavailable("no route to host")
		}
		return configBody("kcp-target"), nil
	})
	stub.install(cs)

	err := waitForGatewayConfigApplied(context.Background(), cs, newFakeDynamicClient(), testNamespace, testGateway,
		"kcp-target", nil, testPort, 10*time.Millisecond, 5*time.Second, nil)

	require.NoError(t, err, "a transient probe failure must not abort the wait")
	assert.GreaterOrEqual(t, stub.callsFor(testGateway+"-0"), 3)
}
