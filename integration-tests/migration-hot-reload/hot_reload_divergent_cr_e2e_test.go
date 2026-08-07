//go:build e2e

// Hot-reload discrepancies between the live Gateway CR and the CRs a migration is
// about to apply.
//
// kcp applies the operator's fenced and switchover files, so those files can change
// how the running gateway behaves: one that enables hot-reload converts every later
// transition to an in-place apply, one that disables it starts rolling pods that
// were not rolling before. kcp makes neither change on the operator's behalf — it
// refuses and names the file.
//
// Two of the facts these tests rest on are properties of CFK and of server-side
// apply rather than of kcp, which is why they are measured here against a real
// operator and real gateway pods:
//
//   - a hot-reloading gateway moves no Deployment generation, so the rollout wait
//     that a mis-detected migration would rely on observes nothing and reports
//     success anyway;
//   - an apply from kcp's field manager that omits spec.hotReload DELETES the field
//     when an earlier apply from the same manager declared it, which is what makes
//     a fenced CR that declares hot-reload plus a switchover CR that does not a
//     silent mid-migration disable rather than a harmless inconsistency.
package hotreload

import (
	"context"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// withHotReload returns crYAML with spec.hotReload.enabled set to enabled.
func withHotReload(t *testing.T, crYAML []byte, enabled bool) []byte {
	t.Helper()

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crYAML, &obj))

	spec, ok := obj["spec"].(map[string]any)
	require.True(t, ok, "the CR must have a spec to edit")
	spec["hotReload"] = map[string]any{"enabled": enabled}

	out, err := yaml.Marshal(obj)
	require.NoError(t, err)
	return out
}

// withoutHotReload returns crYAML with the spec.hotReload block removed, which is
// the shape of every example under docs/assets/gateway-switchover.
func withoutHotReload(t *testing.T, crYAML []byte) []byte {
	t.Helper()

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crYAML, &obj))

	spec, ok := obj["spec"].(map[string]any)
	require.True(t, ok, "the CR must have a spec to edit")
	delete(spec, "hotReload")

	out, err := yaml.Marshal(obj)
	require.NoError(t, err)
	return out
}

// declaresHotReload reports what a CR file says, so a test can state its premise
// rather than assume the rendered fixtures still hold it.
func declaresHotReload(t *testing.T, crYAML []byte) bool {
	t.Helper()

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crYAML, &obj))

	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return false
	}
	hr, ok := spec["hotReload"].(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := hr["enabled"].(bool)
	return enabled
}

// applyAndSettle applies a CR and waits until the operator has accepted it and the
// Deployment is steady, whichever mechanism CFK chose. Returns the Deployment
// generation captured immediately before the apply.
func (e *env) applyAndSettle(t *testing.T, ctx context.Context, crYAML []byte, configID string) int64 {
	t.Helper()

	baseline, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)

	_, err = e.svc.ApplyGatewayYAML(ctx, e.namespace, e.gateway, crYAML, configID)
	require.NoError(t, err)
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, pollInterval, convergeTimeout))
	require.NoError(t, e.svc.WaitForGatewayReady(ctx, e.namespace, e.gateway, baseline, pollInterval, convergeTimeout, nil))
	e.waitForSteadyPods(t, ctx)

	return baseline
}

// waitForSteadyPods blocks until exactly the configured replica count of gateway
// pods exist and every one of them is Ready.
//
// applyAndSettle needs this on top of kcp's own readiness wait because these tests
// toggle spec.hotReload, which rewrites the pod template. kcp stops looking for a
// roll after gatewayRollConfirmationWindow (10s), and CFK can take longer than that
// to write the Deployment — so the wait returns "no roll observed" and the roll
// starts afterwards. Leaving a surge in flight breaks whatever test runs next, which
// is how this was found: a following test saw 4 pods where it expected 2.
func (e *env) waitForSteadyPods(t *testing.T, ctx context.Context) {
	t.Helper()

	deadline := time.Now().Add(convergeTimeout)
	for {
		pods, err := e.clientset.CoreV1().Pods(e.namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=" + e.gateway,
		})
		require.NoError(t, err)

		ready := 0
		for _, p := range pods.Items {
			for _, c := range p.Status.Conditions {
				if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
					ready++
				}
			}
		}
		if len(pods.Items) == e.replicas && ready == e.replicas {
			return
		}

		require.True(t, time.Now().Before(deadline),
			"gateway pods never settled to %d ready (have %d pods, %d ready)", e.replicas, len(pods.Items), ready)
		time.Sleep(pollInterval)
	}
}

// restoreInitialCR puts the rig back the way setup.sh left it, so a test that
// changes the gateway's hot-reload setting cannot decide what runs next.
func (e *env) restoreInitialCR(t *testing.T, ctx context.Context) {
	t.Helper()

	configID, err := gateway.NewConfigID()
	require.NoError(t, err)
	e.applyAndSettle(t, ctx, mustReadFile(t, "KCP_HR_INITIAL_CR"), configID)
}

// liveHotReload reads spec.hotReload.enabled off the running gateway.
func (e *env) liveHotReload(t *testing.T, ctx context.Context) bool {
	t.Helper()
	return declaresHotReload(t, e.readCR(t, ctx))
}

// TestPlannedCRThatWouldEnableHotReloadIsRefused covers the direction that gave
// this whole area its name: the gateway is not running hot-reload, and a CR the
// migration would apply turns it on. Detection must refuse rather than adopt it.
func TestPlannedCRThatWouldEnableHotReloadIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	fenced := mustReadFile(t, "KCP_HR_FENCED_CR")
	switchover := mustReadFile(t, "KCP_HR_SWITCHOVER_CR")
	require.True(t, declaresHotReload(t, fenced), "the rendered fenced CR must enable hot-reload for this scenario")

	t.Cleanup(func() { e.restoreInitialCR(t, context.Background()) })

	// Hot-reload off on the running gateway, still on in the files.
	live := e.readCR(t, ctx)
	e.applyAndSettle(t, ctx, withHotReload(t, live, false), "")
	require.False(t, e.liveHotReload(t, ctx), "the gateway must really have hot-reload off")

	_, err := e.svc.DetectCapability(ctx, e.namespace, e.gateway,
		gateway.DefaultGatewayConfigPort, fenced, switchover)
	require.Error(t, err, "kcp must not turn hot-reload on for a gateway that is not running it")

	assert.Contains(t, err.Error(), "fenced", "the operator has to know which file to fix")
	assert.Contains(t, err.Error(), "spec.hotReload.enabled")
	t.Logf("refused with: %v", err)

	// And the reason refusing matters rather than quietly adopting: nothing kcp
	// could have watched would have caught the difference. Applying that CR turns
	// hot-reload on and the Deployment generation never moves, so a rollout wait
	// returns success having observed nothing at all.
	before := e.fingerprint(t, ctx)
	_, err = e.svc.ApplyGatewayYAML(ctx, e.namespace, e.gateway, fenced, "")
	require.NoError(t, err)
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, pollInterval, convergeTimeout))

	var progress []gateway.GatewayReadinessProgress
	start := time.Now()
	require.NoError(t, e.svc.WaitForGatewayReady(ctx, e.namespace, e.gateway, before.depGen, pollInterval, convergeTimeout,
		func(p gateway.GatewayReadinessProgress) { progress = append(progress, p) }))

	require.NotEmpty(t, progress, "the readiness wait must report at least once")
	assert.False(t, progress[len(progress)-1].RolloutDetected,
		"the fence turned hot-reload on and hot reloads move no Deployment generation, so this wait confirmed nothing")
	t.Logf("had kcp adopted the CR instead of refusing, the fence would have 'passed' in %s with no rollout observed",
		time.Since(start).Round(time.Millisecond))
}

// TestPlannedCRThatWouldDisableHotReloadIsRefused covers the other direction. It
// is the same principle and it matters just as much: the operator's gateway would
// start rolling pods on every transition because of a file kcp applied.
func TestPlannedCRThatWouldDisableHotReloadIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	require.True(t, e.liveHotReload(t, ctx), "the rig's gateway must be running hot-reload")

	fenced := withHotReload(t, mustReadFile(t, "KCP_HR_FENCED_CR"), false)
	switchover := withHotReload(t, mustReadFile(t, "KCP_HR_SWITCHOVER_CR"), false)

	_, err := e.svc.DetectCapability(ctx, e.namespace, e.gateway,
		gateway.DefaultGatewayConfigPort, fenced, switchover)
	require.Error(t, err, "kcp must not turn hot-reload off for a gateway that is running it")

	assert.Contains(t, err.Error(), "fenced")
	assert.Contains(t, err.Error(), "spec.hotReload.enabled")
	t.Logf("refused with: %v", err)
}

// TestPlannedCRsMustAgreeOnMentioningHotReload is the rule that only exists
// because of what server-side apply does, so this test proves the mechanism as
// well as kcp's response to it: it applies a CR that declares spec.hotReload and
// then one that omits it, both as kcp's own field manager, and shows the field is
// deleted rather than inherited.
func TestPlannedCRsMustAgreeOnMentioningHotReload(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	require.True(t, e.liveHotReload(t, ctx), "the rig's gateway must be running hot-reload")
	t.Cleanup(func() { e.restoreInitialCR(t, context.Background()) })

	fenced := mustReadFile(t, "KCP_HR_FENCED_CR")
	switchoverSilent := withoutHotReload(t, mustReadFile(t, "KCP_HR_SWITCHOVER_CR"))
	require.True(t, declaresHotReload(t, fenced))
	require.False(t, declaresHotReload(t, switchoverSilent))

	t.Run("kcp refuses the mismatched pair", func(t *testing.T) {
		_, err := e.svc.DetectCapability(ctx, e.namespace, e.gateway,
			gateway.DefaultGatewayConfigPort, fenced, switchoverSilent)
		require.Error(t, err, "the switchover apply would delete the field the fence apply declared")

		assert.Contains(t, err.Error(), "switchover")
		t.Logf("refused with: %v", err)
	})

	// The mechanism, proven rather than assumed. This is what the refusal above is
	// protecting against, and if server-side apply ever stopped behaving this way
	// the rule could be relaxed — so the claim is pinned here.
	t.Run("omitting the field after declaring it deletes it", func(t *testing.T) {
		configID, err := gateway.NewConfigID()
		require.NoError(t, err)
		e.applyAndSettle(t, ctx, fenced, configID)

		gw, err := e.dynamicGateway(ctx)
		require.NoError(t, err)
		require.Equal(t, true, gw["spec"].(map[string]any)["hotReload"].(map[string]any)["enabled"],
			"the fenced CR must have left hot-reload declared on the live gateway")

		configID, err = gateway.NewConfigID()
		require.NoError(t, err)
		e.applyAndSettle(t, ctx, switchoverSilent, configID)

		gw, err = e.dynamicGateway(ctx)
		require.NoError(t, err)
		_, stillThere := gw["spec"].(map[string]any)["hotReload"]
		assert.False(t, stillThere,
			"server-side apply must have pruned spec.hotReload — if it survived, the presence rule can be relaxed")
	})
}

// dynamicGateway reads the live Gateway CR as an untyped tree, so a test can assert
// on the presence of a field rather than only on its value.
func (e *env) dynamicGateway(ctx context.Context) (map[string]any, error) {
	raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}
