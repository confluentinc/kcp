package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// withHotReload sets the CR's spec.hotReload.enabled.
func withHotReload(enabled bool) gatewayCROption {
	return func(obj *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(obj.Object, enabled, "spec", "hotReload", "enabled")
	}
}

// withSpecConfigID sets the CR's spec.configId.
func withSpecConfigID(id string) gatewayCROption {
	return func(obj *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(obj.Object, id, "spec", "configId")
	}
}

// withProgressing sets the Deployment's Progressing condition.
func withProgressing(status corev1.ConditionStatus, reason string) deploymentOption {
	return func(d *appsv1.Deployment) {
		d.Status.Conditions = append(d.Status.Conditions, appsv1.DeploymentCondition{
			Type:   appsv1.DeploymentProgressing,
			Status: status,
			Reason: reason,
		})
	}
}

// outcomeProber answers each pod with its scripted result. An unscripted pod
// answers unreadably, so a probe the test did not expect shows up as a verdict.
func outcomeProber(byPod map[string]ProbeResult) podProber {
	return func(_ context.Context, e GatewayPodEndpoint) (ProbeResult, error) {
		result, ok := byPod[e.Name]
		if !ok {
			return ProbeResult{Addr: e.Name, Outcome: ProbeUnexpected, Err: fmt.Errorf("no outcome scripted for %s", e.Name)}, nil
		}
		result.Addr = e.Name
		return result, nil
	}
}

func applied(id string) ProbeResult { return ProbeResult{Outcome: ProbeApplied, ConfigID: id} }

func terminating(pod *corev1.Pod) *corev1.Pod {
	pod.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	return pod
}

func TestCheckPodSync(t *testing.T) {
	const (
		ns = "kcp"
		gw = "gw"
	)
	pod := func(name string, ready bool) *corev1.Pod {
		return gatewayPodWithIP(name, ns, gw, "uid-"+name, "10.0.0.1", ready)
	}
	settled := func() *appsv1.Deployment { return completeGatewayDeployment(gw, ns, 2) }
	hotReloadOn := func(configID string) *unstructured.Unstructured {
		opts := []gatewayCROption{withHotReload(true)}
		if configID != "" {
			opts = append(opts, withSpecConfigID(configID))
		}
		return newGatewayCR(gw, ns, 3, 3, true, opts...)
	}

	tests := []struct {
		name         string
		cr           *unstructured.Unstructured
		objects      []runtime.Object
		probe        podProber
		wantVerdict  PodSyncVerdict
		wantDetail   string
		wantEvidence []string
		notEvidence  []string
	}{
		{
			name:        "observedGeneration behind generation is out of sync",
			cr:          newGatewayCR(gw, ns, 3, 2, true),
			wantVerdict: PodsOutOfSync,
			wantDetail:  "the operator has not caught up with the CR: status.observedGeneration is 2, metadata.generation is 3",
			wantEvidence: []string{
				"CR kcp/gw: metadata.generation 3, status.observedGeneration 2, spec.configId none, hot reload off",
			},
		},
		{
			name:        "no status is out of sync",
			cr:          newGatewayCR(gw, ns, 3, 0, false),
			wantVerdict: PodsOutOfSync,
			wantDetail:  "the operator has not reported status.observedGeneration for this CR",
			wantEvidence: []string{
				"CR kcp/gw: metadata.generation 3, status.observedGeneration none, spec.configId none, hot reload off",
			},
		},
		{
			name: "a current rejection is out of sync",
			cr: newGatewayCR(gw, ns, 3, 3, true,
				withGatewayConditionAt("ready", "False", "ApplyFailed", "secretRef not found", 3, time.Now())),
			wantVerdict: PodsOutOfSync,
			wantDetail:  "the operator rejected the CR: secretRef not found (reason ApplyFailed, condition ready)",
		},
		{
			// Live on CFK 0.1838.0: a spec fixed 11s after its rejection was
			// applied (observedGeneration caught up), and CFK left the
			// ApplyFailed behind. It must not refuse a healthy gateway.
			name: "hot reload off: a leftover rejection without its own generation is ignored once the operator has caught up",
			cr: newGatewayCR(gw, ns, 8, 8, true,
				withGatewayConditionAtTime("ready", "False", "ApplyFailed", "secretRef missing-secret-d not found", time.Now())),
			objects:     []runtime.Object{settled(), pod("gw-0", true), pod("gw-1", true)},
			wantVerdict: PodsInSync,
			wantDetail:  "the gateway Deployment has finished rolling out and no gateway pod is terminating",
		},
		{
			name:        "hot reload off: settled Deployment is in sync",
			cr:          newGatewayCR(gw, ns, 3, 3, true),
			objects:     []runtime.Object{settled(), pod("gw-1", true), pod("gw-2", true)},
			wantVerdict: PodsInSync,
			wantDetail:  "the gateway Deployment has finished rolling out and no gateway pod is terminating",
			wantEvidence: []string{
				"Deployment gw: 2 replicas, 2 updated, 2 available",
			},
			notEvidence: []string{"re-run"},
		},
		{
			name: "hot reload off: rollout in progress is out of sync with the re-run hint",
			cr:   newGatewayCR(gw, ns, 3, 3, true),
			objects: []runtime.Object{
				newGatewayDeployment(gw, ns, 2, withObservedGeneration(2), withReplicas(2),
					withUpdatedReplicas(1), withAvailableReplicas(2), withProgressing(corev1.ConditionTrue, "ReplicaSetUpdated")),
				pod("gw-1", true), pod("gw-2", true),
			},
			wantVerdict: PodsOutOfSync,
			wantDetail:  `the gateway Deployment "gw" has not finished rolling out: 1 of 2 pods updated, 2 available`,
			wantEvidence: []string{
				"Deployment gw: 2 replicas, 1 updated, 2 available",
				"The gateway Deployment reports a rollout in progress (1 of 2 pods updated). If it completes, re-run.",
			},
		},
		{
			name: "hot reload off: stuck rollout carries no hint",
			cr:   newGatewayCR(gw, ns, 3, 3, true),
			objects: []runtime.Object{
				newGatewayDeployment(gw, ns, 2, withObservedGeneration(2), withReplicas(2),
					withUpdatedReplicas(1), withAvailableReplicas(1), withProgressing(corev1.ConditionFalse, "ProgressDeadlineExceeded")),
				pod("gw-1", true), pod("gw-2", false),
			},
			wantVerdict: PodsOutOfSync,
			wantDetail:  `the gateway Deployment "gw" has not finished rolling out: 1 of 2 pods updated, 1 available`,
			notEvidence: []string{"re-run"},
		},
		{
			name: "hot reload off: stuck rollout with a terminating pod carries no hint",
			cr:   newGatewayCR(gw, ns, 3, 3, true),
			objects: []runtime.Object{
				newGatewayDeployment(gw, ns, 2, withObservedGeneration(2), withReplicas(2),
					withUpdatedReplicas(1), withAvailableReplicas(1), withProgressing(corev1.ConditionFalse, "ProgressDeadlineExceeded")),
				pod("gw-1", true), terminating(pod("gw-0", true)),
			},
			wantVerdict:  PodsOutOfSync,
			wantDetail:   `the gateway Deployment "gw" has not finished rolling out: 1 of 2 pods updated, 1 available`,
			wantEvidence: []string{"pod gw-0: terminating"},
			notEvidence:  []string{"re-run"},
		},
		{
			name: "hot reload off: complete counts but the Deployment controller lags is out of sync",
			cr:   newGatewayCR(gw, ns, 3, 3, true),
			objects: []runtime.Object{
				newGatewayDeployment(gw, ns, 2, withObservedGeneration(1), withReplicas(2),
					withUpdatedReplicas(2), withAvailableReplicas(2)),
				pod("gw-1", true), pod("gw-2", true),
			},
			wantVerdict: PodsOutOfSync,
			wantDetail:  `the gateway Deployment "gw" has not finished rolling out: its controller has observed generation 1 of 2`,
		},
		{
			name:        "hot reload off: a terminating pod is out of sync with the re-run hint",
			cr:          newGatewayCR(gw, ns, 3, 3, true),
			objects:     []runtime.Object{settled(), pod("gw-1", true), pod("gw-2", true), terminating(pod("gw-0", true))},
			wantVerdict: PodsOutOfSync,
			wantDetail:  "gateway pod gw-0 is still terminating and may still serve the previous config",
			wantEvidence: []string{
				"pod gw-0: terminating",
				"A replaced gateway pod is still terminating (1 pod(s)). If it completes, re-run.",
			},
		},
		{
			name:        "hot reload on: every Ready pod matches is in sync",
			cr:          hotReloadOn("kcp-a"),
			objects:     []runtime.Object{pod("gw-1", true), pod("gw-2", true)},
			probe:       staticProber("kcp-a"),
			wantVerdict: PodsInSync,
			wantDetail:  `every Ready gateway pod reports the CR's configId "kcp-a"`,
			wantEvidence: []string{
				"CR kcp/gw: metadata.generation 3, status.observedGeneration 3, spec.configId kcp-a, hot reload on",
				"pod gw-1: configId kcp-a",
				"pod gw-2: configId kcp-a",
			},
		},
		{
			name:    "hot reload on: an unreachable pod is out of sync",
			cr:      hotReloadOn("kcp-a"),
			objects: []runtime.Object{pod("gw-1", true), pod("gw-2", true)},
			probe: outcomeProber(map[string]ProbeResult{
				"gw-1": applied("kcp-a"),
				"gw-2": {Outcome: ProbeUnreachable, Err: errors.New("connection refused")},
			}),
			wantVerdict:  PodsOutOfSync,
			wantDetail:   "gateway pod gw-2 did not answer GET /config: connection refused",
			wantEvidence: []string{"pod gw-1: configId kcp-a", "pod gw-2: unreachable"},
		},
		{
			name:    "hot reload on: every pod never set is cannot tell",
			cr:      hotReloadOn("kcp-a"),
			objects: []runtime.Object{pod("gw-1", true), pod("gw-2", true)},
			probe: outcomeProber(map[string]ProbeResult{
				"gw-1": {Outcome: ProbeNeverSet},
				"gw-2": {Outcome: ProbeNeverSet},
			}),
			wantVerdict:  PodsCannotTell,
			wantDetail:   "no gateway pod reports a configId yet, so there is nothing to compare with the CR",
			wantEvidence: []string{"pod gw-1: configId never set", "pod gw-2: configId never set"},
		},
		{
			name:    "every pod 404s is cannot tell",
			cr:      hotReloadOn("kcp-a"),
			objects: []runtime.Object{pod("gw-1", true), pod("gw-2", true)},
			probe: outcomeProber(map[string]ProbeResult{
				"gw-1": {Outcome: ProbeEndpointAbsent},
				"gw-2": {Outcome: ProbeEndpointAbsent},
			}),
			wantVerdict:  PodsCannotTell,
			wantDetail:   "the gateway pods do not serve GET /config, so kcp cannot read which config they loaded",
			wantEvidence: []string{"pod gw-1: GET /config not served", "pod gw-2: GET /config not served"},
		},
		{
			name:    "a 404 and a never-set pod is cannot tell with the never-set copy",
			cr:      hotReloadOn("kcp-a"),
			objects: []runtime.Object{pod("gw-1", true), pod("gw-2", true)},
			probe: outcomeProber(map[string]ProbeResult{
				"gw-1": {Outcome: ProbeEndpointAbsent},
				"gw-2": {Outcome: ProbeNeverSet},
			}),
			wantVerdict: PodsCannotTell,
			wantDetail:  "no gateway pod reports a configId yet, so there is nothing to compare with the CR",
		},
		{
			name:    "hot reload on: never set and a value is out of sync",
			cr:      hotReloadOn("kcp-a"),
			objects: []runtime.Object{pod("gw-1", true), pod("gw-2", true)},
			probe: outcomeProber(map[string]ProbeResult{
				"gw-1": applied("kcp-a"),
				"gw-2": {Outcome: ProbeNeverSet},
			}),
			wantVerdict: PodsOutOfSync,
			wantDetail:  "the gateway pods report different configIds: gw-1 kcp-a, gw-2 never set",
		},
		{
			name:    "hot reload on: pods disagree is out of sync",
			cr:      hotReloadOn("kcp-a"),
			objects: []runtime.Object{pod("gw-1", true), pod("gw-2", true)},
			probe: outcomeProber(map[string]ProbeResult{
				"gw-1": applied("kcp-a"),
				"gw-2": applied("kcp-old"),
			}),
			wantVerdict: PodsOutOfSync,
			wantDetail:  "the gateway pods report different configIds: gw-1 kcp-a, gw-2 kcp-old",
		},
		{
			name:        "hot reload on: CR without configId is cannot tell",
			cr:          hotReloadOn(""),
			objects:     []runtime.Object{pod("gw-1", true), pod("gw-2", true)},
			probe:       staticProber("kcp-a"),
			wantVerdict: PodsCannotTell,
			wantDetail:  "the CR has no spec.configId, so there is nothing to compare the pods with",
			wantEvidence: []string{
				"CR kcp/gw: metadata.generation 3, status.observedGeneration 3, spec.configId none, hot reload on",
			},
		},
		{
			name:        "hot reload on: pods agree on another value is out of sync",
			cr:          hotReloadOn("kcp-new"),
			objects:     []runtime.Object{pod("gw-1", true), pod("gw-2", true)},
			probe:       staticProber("kcp-old"),
			wantVerdict: PodsOutOfSync,
			wantDetail:  `every gateway pod reports configId "kcp-old", but the CR's spec.configId is "kcp-new"`,
		},
		{
			name:    "hot reload on: a not-Ready stale pod is not probed",
			cr:      hotReloadOn("kcp-a"),
			objects: []runtime.Object{pod("gw-1", true), pod("gw-2", false)},
			// gw-2 would report the old id, so probing it would flip the verdict.
			probe:        perPodProber(map[string]string{"gw-1": "kcp-a"}, "kcp-old"),
			wantVerdict:  PodsInSync,
			wantDetail:   `every Ready gateway pod reports the CR's configId "kcp-a"`,
			wantEvidence: []string{"pod gw-1: configId kcp-a", "pod gw-2: not Ready, not checked"},
		},
		{
			name:         "hot reload on: a terminating Ready pod is probed",
			cr:           hotReloadOn("kcp-a"),
			objects:      []runtime.Object{pod("gw-1", true), terminating(pod("gw-2", true))},
			probe:        perPodProber(map[string]string{"gw-1": "kcp-a"}, "kcp-old"),
			wantVerdict:  PodsOutOfSync,
			wantDetail:   "the gateway pods report different configIds: gw-1 kcp-a, gw-2 kcp-old",
			wantEvidence: []string{"pod gw-2: configId kcp-old"},
		},
		{
			name:    "hot reload on: a terminating not-Ready pod is out of sync with the re-run hint",
			cr:      hotReloadOn("kcp-a"),
			objects: []runtime.Object{pod("gw-1", true), terminating(pod("gw-0", false))},
			// gw-0 keeps its open connections until its grace period ends, and
			// is not probed, so a matching gw-1 must not make this in sync.
			probe:       perPodProber(map[string]string{"gw-1": "kcp-a"}, "kcp-old"),
			wantVerdict: PodsOutOfSync,
			wantDetail:  "gateway pod gw-0 is still terminating and may still serve the previous config to its open connections",
			wantEvidence: []string{
				"pod gw-0: terminating, not Ready, not checked",
				"pod gw-1: configId kcp-a",
				"A replaced gateway pod is still terminating (1 pod(s)). If it completes, re-run.",
			},
		},
		{
			name:         "no Ready pod is out of sync",
			cr:           hotReloadOn("kcp-a"),
			objects:      []runtime.Object{pod("gw-1", false)},
			probe:        staticProber("kcp-a"),
			wantVerdict:  PodsOutOfSync,
			wantDetail:   "no gateway pod is Ready, so none can be checked",
			wantEvidence: []string{"pod gw-1: not Ready, not checked"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := tt.probe
			if probe == nil {
				probe = func(context.Context, GatewayPodEndpoint) (ProbeResult, error) {
					t.Fatal("the check probed a pod it should not have")
					return ProbeResult{}, nil
				}
			}

			got, err := checkPodSync(context.Background(), newFakeClientset(tt.objects...), probe, ns, gw, tt.cr)
			require.NoError(t, err)

			assert.Equal(t, tt.wantVerdict, got.Verdict)
			assert.Contains(t, got.Detail, tt.wantDetail)

			require.NotEmpty(t, got.Evidence)
			assert.True(t, strings.HasPrefix(got.Evidence[0], "CR "), "first evidence line: %q", got.Evidence[0])
			last := got.Evidence[len(got.Evidence)-1]
			assert.Equal(t, "inspect: kubectl -n kcp get gateway gw -o yaml; kubectl -n kcp get pods -l app=gw", last)
			for _, want := range tt.wantEvidence {
				assert.Contains(t, got.Evidence, want)
			}
			joined := strings.Join(got.Evidence, "\n") + "\n" + got.Detail
			for _, unwanted := range tt.notEvidence {
				assert.NotContains(t, joined, unwanted)
			}
		})
	}

	t.Run("evidence is in display order", func(t *testing.T) {
		got, err := checkPodSync(context.Background(),
			newFakeClientset(newGatewayDeployment(gw, ns, 2, withObservedGeneration(2), withReplicas(2),
				withUpdatedReplicas(1), withAvailableReplicas(2), withProgressing(corev1.ConditionTrue, "ReplicaSetUpdated")),
				terminating(pod("gw-0", true)), pod("gw-1", true)),
			nil, ns, gw, newGatewayCR(gw, ns, 3, 3, true))
		require.NoError(t, err)
		assert.Equal(t, []string{
			"CR kcp/gw: metadata.generation 3, status.observedGeneration 3, spec.configId none, hot reload off",
			"Deployment gw: 2 replicas, 1 updated, 2 available",
			"pod gw-0: terminating",
			"The gateway Deployment reports a rollout in progress (1 of 2 pods updated). If it completes, re-run.",
			"inspect: kubectl -n kcp get gateway gw -o yaml; kubectl -n kcp get pods -l app=gw",
		}, got.Evidence)
	})

	t.Run("the check only reads", func(t *testing.T) {
		cs := newFakeClientset(settled(), pod("gw-1", true), pod("gw-2", true))

		_, err := checkPodSync(context.Background(), cs, nil, ns, gw, newGatewayCR(gw, ns, 3, 3, true))
		require.NoError(t, err)
		_, err = checkPodSync(context.Background(), cs, staticProber("kcp-a"), ns, gw, hotReloadOn("kcp-a"))
		require.NoError(t, err)

		actions := cs.(*kubernetesfake.Clientset).Actions()
		require.NotEmpty(t, actions)
		for _, action := range actions {
			assert.Contains(t, []string{"get", "list"}, action.GetVerb(), "action %s on %s", action.GetVerb(), action.GetResource().Resource)
		}
	})

	t.Run("a failed pod list is an error", func(t *testing.T) {
		cs := newFakeClientset(settled())
		cs.(*kubernetesfake.Clientset).PrependReactor("list", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("apiserver down")
		})
		_, err := checkPodSync(context.Background(), cs, nil, ns, gw, newGatewayCR(gw, ns, 3, 3, true))
		require.Error(t, err)
	})
}

func TestParseGatewayCR(t *testing.T) {
	t.Run("integer fields read as int64", func(t *testing.T) {
		cr, err := parseGatewayCR([]byte(`apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gw
  namespace: kcp
  generation: 3
spec:
  configId: kcp-a
status:
  observedGeneration: 2
`))
		require.NoError(t, err)

		got, err := checkPodSync(context.Background(), newFakeClientset(), nil, "kcp", "gw", cr)
		require.NoError(t, err)
		assert.Equal(t, PodsOutOfSync, got.Verdict)
		assert.Equal(t, "the operator has not caught up with the CR: status.observedGeneration is 2, metadata.generation is 3", got.Detail)
	})

	t.Run("a CR that will not parse is an error", func(t *testing.T) {
		_, err := parseGatewayCR([]byte("metadata: [unterminated"))
		require.Error(t, err)
	})
}

func TestPodSyncProbeResult(t *testing.T) {
	t.Run("a probe that times out with a live parent is unreachable", func(t *testing.T) {
		got, err := podSyncProbeResult(context.Background(), "gw-1", ProbeResult{}, context.DeadlineExceeded)
		require.NoError(t, err)
		assert.Equal(t, ProbeUnreachable, got.Outcome)
		assert.Equal(t, "gw-1", got.Addr)
		assert.ErrorIs(t, got.Err, context.DeadlineExceeded)
	})

	t.Run("a cancelled parent's error passes through", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := podSyncProbeResult(parent, "gw-1", ProbeResult{}, context.Canceled)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("a probe without error is returned unchanged", func(t *testing.T) {
		want := ProbeResult{Addr: "gw-1:9180", Outcome: ProbeApplied, ConfigID: "kcp-a"}
		got, err := podSyncProbeResult(context.Background(), "gw-1", want, nil)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})
}
