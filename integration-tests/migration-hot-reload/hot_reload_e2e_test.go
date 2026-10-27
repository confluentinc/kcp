//go:build e2e

// Package hotreload verifies, against a real licensed Confluent Gateway, that
// kcp confirms a gateway transition by config revision rather than by pod
// turnover.
//
// This runs INSIDE the cluster (see manifests/kcp-runner.yaml): kcp dials each
// gateway pod's IP directly on the /config port, and pod IPs are not routable
// from the host under the minikube docker driver. run.sh compiles this package
// for the node's architecture, ships the binary to the runner pod and executes
// it there.
//
// What makes this suite meaningful rather than decorative is that every
// transition it applies is an in-place route edit, which CFK hot-reloads. So a
// pod restart here is a failure, not an expected mechanism, and the assertions
// below can hold kcp to the stronger claim: the config reached every serving
// pod, proven by asking the pods.
package hotreload

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// Convergence was measured at ~4s on a 2-replica gateway, so this is a
	// generous ceiling that still fails in reasonable time.
	convergeTimeout = 90 * time.Second
	pollInterval    = 2 * time.Second
)

type env struct {
	namespace string
	gateway   string
	replicas  int
	// svc is kcp's own gateway service, constructed the way the migration
	// workflow constructs it. An empty kubeconfig path makes client-go fall back
	// to the in-cluster service account.
	svc       *gateway.K8sService
	clientset kubernetes.Interface
}

func newEnv(t *testing.T) *env {
	t.Helper()

	ns := envOrDefault("KCP_HR_NAMESPACE", "confluent")
	gw := envOrDefault("KCP_HR_GATEWAY_NAME", "hotreload-gateway")

	replicas := 2
	if _, err := fmt.Sscanf(envOrDefault("KCP_HR_GATEWAY_REPLICAS", "2"), "%d", &replicas); err != nil {
		t.Fatalf("bad KCP_HR_GATEWAY_REPLICAS: %v", err)
	}

	cfg, err := rest.InClusterConfig()
	require.NoError(t, err, "this suite must run inside the cluster; see run.sh")
	cs, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	return &env{
		namespace: ns,
		gateway:   gw,
		replicas:  replicas,
		svc:       gateway.NewK8sService(""),
		clientset: cs,
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// podFingerprint captures what a pod roll would change: the identity of each pod
// and how many times its container has restarted.
type podFingerprint struct {
	uids     map[k8stypes.UID]int32
	depGen   int64
	podCount int
}

func (e *env) fingerprint(t *testing.T, ctx context.Context) podFingerprint {
	t.Helper()

	pods, err := e.clientset.CoreV1().Pods(e.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=" + e.gateway,
	})
	require.NoError(t, err)

	fp := podFingerprint{uids: map[k8stypes.UID]int32{}, podCount: len(pods.Items)}
	for _, p := range pods.Items {
		var restarts int32
		if len(p.Status.ContainerStatuses) > 0 {
			restarts = p.Status.ContainerStatuses[0].RestartCount
		}
		fp.uids[p.UID] = restarts
	}

	dep, err := e.clientset.AppsV1().Deployments(e.namespace).Get(ctx, e.gateway, metav1.GetOptions{})
	require.NoError(t, err)
	fp.depGen = dep.Generation

	return fp
}

// assertNoPodRoll is the assertion that gives the suite its teeth. Every
// transition it applies is hot-reloadable, so identical pod UIDs, unchanged
// restart counts and an unmoved Deployment generation are all required. The
// Deployment generation is checked too because it is the signal kcp itself
// routes on: if it moved, kcp would have taken the rollout path and the configId
// verification would not have been exercised at all.
func assertNoPodRoll(t *testing.T, before, after podFingerprint) {
	t.Helper()

	assert.Equal(t, before.podCount, after.podCount, "pod count changed")
	assert.Equal(t, before.depGen, after.depGen,
		"the Deployment's metadata.generation moved, so CFK rewrote the pod template — this transition rolled instead of hot-reloading")

	for uid, wasRestarts := range before.uids {
		nowRestarts, stillThere := after.uids[uid]
		assert.True(t, stillThere, "a gateway pod was replaced during a hot-reloadable change")
		assert.Equal(t, wasRestarts, nowRestarts, "a gateway container restarted during a hot-reloadable change")
	}
}

// TestCapabilityIsDetected is the gate everything else depends on. If kcp reads
// this cluster as pre-hot-reload, every later assertion would pass against the
// rollout path and prove nothing about hot reload.
func TestCapabilityIsDetected(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	// No planned CRs: this asks what the live cluster alone supports, which is what
	// the rest of this file depends on. The planned-CR inputs are exercised in
	// hot_reload_divergent_cr_e2e_test.go's DetectCapability refusal tests, where
	// they are the thing under test.
	capability, err := e.svc.DetectCapability(ctx, e.namespace, e.gateway, gateway.DefaultGatewayConfigPort, nil, nil)
	require.NoError(t, err)

	assert.True(t, capability.CRDSupportsConfigID, "the installed CRD must declare spec.configId")
	assert.True(t, capability.HotReloadEnabled, "the gateway CR must have spec.hotReload.enabled=true")
	assert.True(t, capability.ConfigEndpointServed,
		"the running pods must serve GET /config; the operator and the gateway are versioned separately, "+
			"so a capable CRD alone does not imply a capable image")
	assert.Equal(t, gateway.VerifyPerPodConfigID, capability.Mode)
	assert.True(t, capability.InjectsConfigID())
	assert.Empty(t, capability.Advisory, "a fully capable cluster should carry no advisory")
}

// TestConfigIDOnlyApplyHotReloads is the contract's core claim, and the exact
// probe VerifyHotReloadCapability runs before fencing: a change to nothing but
// spec.configId reaches every pod without restarting any of them.
func TestConfigIDOnlyApplyHotReloads(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	before := e.fingerprint(t, ctx)
	require.Equal(t, e.replicas, before.podCount, "expected the configured replica count before the apply")

	configID, err := gateway.NewConfigID()
	require.NoError(t, err)

	stored, err := e.svc.PatchGatewayConfigID(ctx, e.namespace, e.gateway, configID)
	require.NoError(t, err)
	require.Equal(t, configID, stored, "the API server must persist the configId kcp sent")

	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, pollInterval, convergeTimeout))

	start := time.Now()
	require.NoError(t, e.svc.WaitForGatewayConfigID(ctx, e.namespace, e.gateway, gateway.ConfigWaitOptions{
		ConfigID:         configID,
		Port:             gateway.DefaultGatewayConfigPort,
		PollInterval:     pollInterval,
		HotReloadTimeout: convergeTimeout,
	}),
		"every pod must report the new configId — if this times out, the gateway's config watcher is not running, "+
			"which is almost always a trial rather than Enterprise licence")
	t.Logf("configId converged on all %d pods in %s", before.podCount, time.Since(start).Round(time.Millisecond))

	assertNoPodRoll(t, before, e.fingerprint(t, ctx))
}

// TestNoRollIsObservedForAHotReload pins the routing decision W7 added. A
// hot-reload must leave the Deployment's generation untouched, so kcp must
// observe MechanismNoRollObserved and rely on the configId probe — the rollout
// wait has nothing to converge on.
func TestNoRollIsObservedForAHotReload(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	baseline, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)

	configID, err := gateway.NewConfigID()
	require.NoError(t, err)
	_, err = e.svc.PatchGatewayConfigID(ctx, e.namespace, e.gateway, configID)
	require.NoError(t, err)
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, pollInterval, convergeTimeout))
	require.NoError(t, e.svc.WaitForGatewayConfigID(ctx, e.namespace, e.gateway, gateway.ConfigWaitOptions{
		ConfigID:         configID,
		Port:             gateway.DefaultGatewayConfigPort,
		PollInterval:     pollInterval,
		HotReloadTimeout: convergeTimeout,
	}))

	after, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	assert.Equal(t, baseline, after,
		"a hot reload must not move the Deployment's generation; if it did, kcp would route to the rollout wait")
}

// TestStaleConfigIDIsNotAcceptedAsSuccess guards against the failure mode that
// motivated per-pod verification: a wait that passes because the pods already
// report the value being waited for. Waiting for the PREVIOUS revision after a
// new one has landed must time out rather than succeed.
func TestStaleConfigIDIsNotAcceptedAsSuccess(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	first, err := gateway.NewConfigID()
	require.NoError(t, err)
	_, err = e.svc.PatchGatewayConfigID(ctx, e.namespace, e.gateway, first)
	require.NoError(t, err)
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, pollInterval, convergeTimeout))
	require.NoError(t, e.svc.WaitForGatewayConfigID(ctx, e.namespace, e.gateway, gateway.ConfigWaitOptions{
		ConfigID:         first,
		Port:             gateway.DefaultGatewayConfigPort,
		PollInterval:     pollInterval,
		HotReloadTimeout: convergeTimeout,
	}))

	second, err := gateway.NewConfigID()
	require.NoError(t, err)
	_, err = e.svc.PatchGatewayConfigID(ctx, e.namespace, e.gateway, second)
	require.NoError(t, err)
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, pollInterval, convergeTimeout))
	require.NoError(t, e.svc.WaitForGatewayConfigID(ctx, e.namespace, e.gateway, gateway.ConfigWaitOptions{
		ConfigID:         second,
		Port:             gateway.DefaultGatewayConfigPort,
		PollInterval:     pollInterval,
		HotReloadTimeout: convergeTimeout,
	}))

	// The pods now report `second`. Waiting for `first` must fail.
	err = e.svc.WaitForGatewayConfigID(ctx, e.namespace, e.gateway, gateway.ConfigWaitOptions{
		ConfigID:         first,
		Port:             gateway.DefaultGatewayConfigPort,
		PollInterval:     pollInterval,
		HotReloadTimeout: 15 * time.Second,
	})
	require.Error(t, err, "waiting for a superseded configId must not report success")
}

// TestFenceAndSwitchoverAreVerifiedPerPod walks the transitions a migration
// actually performs, in order — fence, switchover, rollback — as the targeted
// route patches the migration workflow now issues rather than the full-CR
// applies the SSA path used. Each transition mints a fresh configId that must be
// confirmed on every pod, and none may roll: all three are in-place route edits,
// so an unmoved Deployment generation and unchanged pods are required
// (assertNoPodRoll). This is the patch-path restoration of the per-pod
// fence/switchover/rollback coverage dropped when the SSA write path was removed.
func TestFenceAndSwitchoverAreVerifiedPerPod(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	seen := map[string]bool{}
	for _, step := range []struct {
		name string
		rp   gateway.RoutePatch
	}{
		{"fence", fenceRoutePatch(t)},
		{"switchover", switchoverRoutePatch(t)},
		{"rollback", unfenceRoutePatch(t)},
	} {
		t.Run(step.name, func(t *testing.T) {
			before := e.fingerprint(t, ctx)

			configID := e.patchRouteAndConverge(t, ctx, step.rp)
			require.False(t, seen[configID], "each transition must carry a distinct configId")
			seen[configID] = true

			// Every transition here is an in-place route edit, so none may roll.
			assertNoPodRoll(t, before, e.fingerprint(t, ctx))
		})
	}
}
