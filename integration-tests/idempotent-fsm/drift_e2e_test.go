//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// TestDrift_StalledSwitchRerunIsRefused is the regression test for the
// Gateway drift bug (design: ~/claude-plans/kcp/2026-10-23-gateway-cr-pod-sync-design.md
// §8.1). Run 1 is killed after promote. The CFK operator is then stopped, so
// run 2's switchover patch lands on the CR but never reaches the pods: the
// switch times out waiting for the operator, past promote, so nothing rolls
// back. The CR now says switched while the pods still serve the fence, and
// clients can't produce. Before the pod check, a re-run read the CR alone,
// found nothing to do and exited 0. Now the pod check sees the operator hasn't
// caught up with the CR, so the dry-run and the re-run refuse, and the re-run
// makes no gateway write. Static only. Slice tbm-topic-001..005.
func TestDrift_StalledSwitchRerunIsRefused(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: the reproduction takes the AAO rollout-verification path")
	}
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	topics := e.topicRange(1, 5)
	probe := topics[0]
	mani := e.writeManifest(t, "drift-stalled-switch", topics)
	e.saveManifest(t, mani)

	e.requireGatewayCarriesProduce(t, probe)
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source, produce through the gateway works)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpPromoted, "migration", "cutover", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted after promote must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")
	cr := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must be fenced — the interrupt came after promote, before switch", tp)
		require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED — the interrupt came after promote", tp)
	}
	e.snapshot(t, ctx, "after-promote", "AFTER interrupt at promoted (expect fenced, mirrors STOPPED)", topics)

	e.stopOperator(t, ctx)
	gen0, obs0 := e.crGenerations(t, ctx)
	require.Equal(t, gen0, obs0, "the operator must have observed the fenced CR before it stopped")
	depGen0, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	uids0, err := e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway)
	require.NoError(t, err)

	out2, err2 := e.runKCP(t, "kcp-run-2-switch-stalls.log", "", "migration", "cutover", "execute", "--migration-yaml", mani, "--rollout-timeout", "45s")
	require.Error(t, err2, "the switch must fail: the operator never observes it")
	require.Contains(t, out2, "Switchover gateway CR applied", "run 2 must reach the switch and patch the CR")
	require.Contains(t, out2, "to be observed by the operator", "the switch must fail waiting for the operator")
	require.NotContains(t, out2, "removing fence", "past promote there is no rollback")
	cr = e.readCR(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isSwitchedToTarget(t, cr, tp), "%s must be switched in the CR", tp)
		require.Falsef(t, e.isFenced(t, cr, tp), "%s must be unfenced in the CR", tp)
	}
	gen1, obs1 := e.crGenerations(t, ctx)
	require.Greater(t, gen1, gen0, "the switch patch must bump the CR's generation")
	require.Equal(t, obs0, obs1, "the stopped operator must not observe the switch")
	depGen1, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	require.Equal(t, depGen0, depGen1, "the gateway Deployment must be untouched")
	uids1, err := e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	// No new pod, rather than the same set: a pod still terminating from run
	// 1's fence roll can be in uids0 and gone by now.
	for uid := range uids1 {
		_, ok := uids0[uid]
		require.Truef(t, ok, "gateway pod %s is new, but no pod may start while the operator is stopped: the pods must still hold the fenced config", uid)
	}
	e.snapshot(t, ctx, "after-stalled-switch", "AFTER the stalled switch (CR switched and unfenced; pods unchanged)", topics)

	acks, firstErr := produceThroughGateway(t, e.gatewayBootstrap(), probe, 5)
	require.Zerof(t, acks, "the pods must still serve the fence even though the CR says switched")
	t.Logf("\n🔍 DATA PLANE ▸ 0/5 produce attempts acked through %s; first error: %v", e.gatewayBootstrap(), firstErr)

	out3, err3 := e.runKCP(t, "kcp-run-3-dry-run.log", "", "migration", "cutover", "execute", "--migration-yaml", mani, "--dry-run")
	require.Error(t, err3, "the dry-run must refuse: the pods may not serve the CR")
	require.Contains(t, out3, "gateway pods serve the CR", "the dry-run must name the failed pod check")
	require.Contains(t, out3, "the operator has not caught up with the CR", "the dry-run must say the operator is behind the CR")

	out4, err4 := e.runKCP(t, "kcp-run-4-rerun.log", "", "migration", "cutover", "execute", "--migration-yaml", mani)
	require.Error(t, err4, "the re-run must refuse: the pods may not serve the CR")
	require.Contains(t, out4, "reconcile plan refused", "the re-run must exit on the refusal")
	require.NotContains(t, out4, fsmStartBanner, "no state machine may run on a refused plan")
	gen2, obs2 := e.crGenerations(t, ctx)
	require.Equal(t, gen1, gen2, "the re-run must make no gateway write")
	require.Equal(t, obs1, obs2, "the operator is still stopped")
	acks, firstErr = produceThroughGateway(t, e.gatewayBootstrap(), probe, 5)
	require.Zerof(t, acks, "clients are still fenced: the refusal changes nothing (first error: %v)", firstErr)
	e.snapshot(t, ctx, "after-rerun", "AFTER the refused re-run (CR switched; pods still fenced; nothing written)", topics)

	t.Logf("\n✅ RESULT: drift refused — the CR is switched at generation %d, the operator last observed generation %d, "+
		"and the dry-run and the re-run both refused without writing to the gateway.", gen1, obs1)
}

// TestDrift_UnfenceStalledRerunIsRefused is the reverse direction of
// TestDrift_StalledSwitchRerunIsRefused (design §8.3): the CR says unfenced
// while the pods still serve the fence. Run 1 is killed after the fence, and
// the CFK operator is stopped. Run 2's verify_fence step fails (the test-only
// failure hook), nothing is promoted, so it rolls back: the unfence lands on
// the CR, but the operator never observes it, so the rollback's wait times
// out and the run exits non-zero. Producers through the gateway are still
// fenced. kcp's pod check sees the operator hasn't caught up with the CR, so
// a dry-run and a re-run both refuse, and neither writes to the gateway.
// Static only: the data-plane probe needs the static route's plaintext client
// endpoint. Slice tbm-topic-006..010.
func TestDrift_UnfenceStalledRerunIsRefused(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: the data-plane probe needs the static route's plaintext client endpoint")
	}
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	topics := e.topicRange(6, 10)
	probe := topics[0]
	mani := e.writeManifest(t, "drift-unfence-stalled", topics)
	e.saveManifest(t, mani)

	acks0 := e.requireGatewayCarriesProduce(t, probe)
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source, produce through the gateway works)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpFenced, "migration", "cutover", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted after the fence must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")
	cr := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must be fenced — the interrupt came after the fence step", tp)
		require.Equalf(t, "ACTIVE", ms[tp], "%s must be ACTIVE — the interrupt came before promote", tp)
	}
	e.snapshot(t, ctx, "after-fence", "AFTER interrupt at fenced (expect fenced, mirrors ACTIVE)", topics)

	e.stopOperator(t, ctx)
	gen0, obs0 := e.crGenerations(t, ctx)
	require.Equal(t, gen0, obs0, "the operator must have observed the fenced CR before it stopped")

	out2, err2 := e.runKCPFailingAt(t, "kcp-run-2-unfence-stalls.log", failStep, "migration", "cutover", "execute", "--migration-yaml", mani, "--rollout-timeout", "45s")
	require.Error(t, err2, "the run must fail: its verify_fence step fails, and the operator never observes the rollback's unfence")
	require.Contains(t, out2, killpoint.FailEnvVar, "the step failure must be the hook's")
	require.Contains(t, out2, "removing fence to restore traffic", "nothing is promoted, so the run rolls back")
	cr = e.readCR(t, ctx)
	for _, tp := range topics {
		require.Falsef(t, e.isFenced(t, cr, tp), "%s must be unfenced in the CR: the rollback's unfence landed", tp)
	}
	gen1, obs1 := e.crGenerations(t, ctx)
	require.Lessf(t, obs1, gen1, "the stopped operator must not observe the unfence (observedGeneration %d, generation %d)", obs1, gen1)
	e.snapshot(t, ctx, "after-stalled-unfence", "AFTER the stalled unfence (CR unfenced; pods still fenced)", topics)

	acks, firstErr := produceThroughGateway(t, e.gatewayBootstrap(), probe, 5)
	require.Zerof(t, acks, "the pods must still serve the fence even though the CR says unfenced (the same producer got %d acks before run 1)", acks0)
	t.Logf("\n🔍 DATA PLANE ▸ 0/5 produce attempts acked through %s (%d/5 before run 1); first error: %v", e.gatewayBootstrap(), acks0, firstErr)

	out3, err3 := e.runKCP(t, "kcp-run-3-dry-run.log", "", "migration", "cutover", "execute", "--migration-yaml", mani, "--dry-run")
	require.Error(t, err3, "the dry-run must refuse: the pods may not serve the CR")
	require.Contains(t, out3, "the operator has not caught up with the CR", "the dry-run must say the operator is behind the CR")
	gen2, obs2 := e.crGenerations(t, ctx)
	require.Equal(t, gen1, gen2, "the dry-run must make no gateway write")
	require.Equal(t, obs1, obs2, "the operator is still stopped")

	out4, err4 := e.runKCP(t, "kcp-run-4-rerun.log", "", "migration", "cutover", "execute", "--migration-yaml", mani)
	require.Error(t, err4, "the re-run must refuse: the pods may not serve the CR")
	require.Contains(t, out4, "the operator has not caught up with the CR", "the re-run must say the operator is behind the CR")
	require.NotContains(t, out4, fsmStartBanner, "no state machine may run on a refused plan")
	gen3, obs3 := e.crGenerations(t, ctx)
	require.Equal(t, gen1, gen3, "the re-run must make no gateway write")
	require.Equal(t, obs1, obs3, "the operator is still stopped")
	e.snapshot(t, ctx, "after-rerun", "AFTER the refused re-run (CR unfenced; pods still fenced; nothing written)", topics)

	t.Logf("\n✅ RESULT: reverse drift refused — the CR is unfenced at generation %d, the operator last observed generation %d, "+
		"and the dry-run and the re-run both refused without writing to the gateway.", gen1, obs1)
}

// TestDrift_TrialLicenceSwitchRerunIsRefused covers the pod check's configId
// comparison on a hot-reload-on gateway (design §8.3, the hot-reload-on
// path). Stopping the operator never reaches it, because the pod check's first
// rule refuses first. A gateway without an Enterprise licence does: the
// operator observes every CR change, but the pods never load a new config, so
// they keep reporting the configId they started with. Run 1 is killed after
// promote. GATEWAY_LICENSES is then removed from the CR's pod template, which
// rolls the pods onto trial limits; they load the current, fenced config at
// start. The switched route is applied to the CR by hand with a fresh
// configId, as kcp's switch would (kcp's own switch can't create this drift:
// its hot-reload smoke test stops a trial-licence run first). The operator
// observes it, and every pod still reports the previous configId. A dry-run
// and a re-run both refuse with the pod check's configId detail, and the CR's
// generation doesn't move. The licence is put back when the test ends. Dynamic
// only. Slice tbm-topic-091..095.
func TestDrift_TrialLicenceSwitchRerunIsRefused(t *testing.T) {
	e := newEnv()
	if e.mode != "dynamic" {
		t.Skip("hot-reload gateways only: the static gateway has no configId to compare")
	}
	ctx := context.Background()
	topics := e.topicRange(91, 95)
	mani := e.writeManifest(t, "drift-trial-licence", topics)
	e.saveManifest(t, mani)
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, topics route → source)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpPromoted, "migration", "cutover", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted after promote must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")
	cr := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isFenced(t, cr, tp), "%s must be fenced — the interrupt came after promote, before switch", tp)
		require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED — the interrupt came after promote", tp)
	}
	e.snapshot(t, ctx, "after-promote", "AFTER interrupt at promoted (expect fenced, mirrors STOPPED)", topics)

	e.dropGatewayLicence(t, ctx)
	prevID := e.crConfigID(t, ctx)
	e.snapshot(t, ctx, "after-licence-drop", "AFTER the licence is removed (pods rolled onto trial limits; still fenced)", topics)

	depGen0, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	uids0, err := e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway)
	require.NoError(t, err)

	newID := e.switchByHand(t, ctx, topics)
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, 2*time.Second, 3*time.Minute),
		"the operator must accept the switched route")
	gen1, obs1 := e.crGenerations(t, ctx)
	require.Equal(t, gen1, obs1, "the operator must observe the switched route")
	cr = e.readCR(t, ctx)
	for _, tp := range topics {
		require.Truef(t, e.isSwitchedToTarget(t, cr, tp), "%s must be switched in the CR", tp)
		require.Falsef(t, e.isFenced(t, cr, tp), "%s must be unfenced in the CR", tp)
	}

	// Long past a hot reload's few seconds, every Ready pod's GET /config
	// must still report the configId it loaded at start. CheckPodSync is
	// kcp's own read of every pod's /config; the suite has no probe of its
	// own.
	want := fmt.Sprintf("every gateway pod reports configId %q, but the CR's spec.configId is %q", prevID, newID)
	for end := time.Now().Add(30 * time.Second); ; {
		raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
		require.NoError(t, err)
		ps, err := e.svc.CheckPodSync(ctx, e.namespace, e.gateway, raw, gateway.DefaultGatewayConfigPort)
		require.NoError(t, err, "read every gateway pod's /config")
		require.Equalf(t, gateway.PodsOutOfSync, ps.Verdict, "the pods must not load the switched route (detail: %s)", ps.Detail)
		require.Equal(t, want, ps.Detail, "every pod must still report the previous configId")
		if time.Now().After(end) {
			t.Logf("\n🔍 GATEWAY PODS ▸ 30s after the switch every pod still reports configId %s, the CR's is %s:\n%s",
				prevID, newID, indentLines(strings.Join(ps.Evidence, "\n"), "      "))
			break
		}
		time.Sleep(5 * time.Second)
	}
	depGen1, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	require.Equal(t, depGen0, depGen1, "the switched route must not roll the pods: they keep the fenced config")
	uids1, err := e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	for uid := range uids1 {
		_, ok := uids0[uid]
		require.Truef(t, ok, "gateway pod %s is new: no pod may start after the switch, or it would load the switched route", uid)
	}
	e.snapshot(t, ctx, "after-hand-switch", "AFTER the switch by hand (CR switched and unfenced, observed; pods still on the previous configId)", topics)

	out2, err2 := e.runKCP(t, "kcp-run-2-dry-run.log", "", "migration", "cutover", "execute", "--migration-yaml", mani, "--dry-run")
	require.Error(t, err2, "the dry-run must refuse: the pods don't serve the CR")
	require.Contains(t, out2, "gateway pods serve the CR", "the dry-run must name the failed pod check")
	require.Contains(t, out2, "every gateway pod reports configId", "the dry-run must carry the pod check's configId detail")
	require.Contains(t, out2, newID, "the detail must name the CR's configId")
	gen2, obs2 := e.crGenerations(t, ctx)
	require.Equal(t, gen1, gen2, "the dry-run must make no gateway write")
	require.Equal(t, obs1, obs2)

	out3, err3 := e.runKCP(t, "kcp-run-3-rerun.log", "", "migration", "cutover", "execute", "--migration-yaml", mani)
	require.Error(t, err3, "the re-run must refuse: the pods don't serve the CR")
	require.Contains(t, out3, "reconcile plan refused", "the re-run must exit on the refusal")
	require.Contains(t, out3, "every gateway pod reports configId", "the re-run must carry the pod check's configId detail")
	require.NotContains(t, out3, fsmStartBanner, "no state machine may run on a refused plan")
	gen3, obs3 := e.crGenerations(t, ctx)
	require.Equal(t, gen1, gen3, "the re-run must make no gateway write")
	require.Equal(t, obs1, obs3)
	e.snapshot(t, ctx, "after-rerun", "AFTER the refused re-run (CR switched; pods on the previous configId; nothing written)", topics)

	t.Logf("\n✅ RESULT: hot-reload drift refused — the operator observed the switch at generation %d, every pod still reports configId %s "+
		"instead of the CR's %s, and the dry-run and the re-run both refused without writing to the gateway.", gen1, prevID, newID)
}

// licenceEnvVar is the gateway pod-template env var carrying the Enterprise
// licence (testdata/manifests/templates/gateway-dynamic.yaml). Without it the
// gateway runs on trial limits and its hot-reload watcher never starts.
const licenceEnvVar = "GATEWAY_LICENSES"

// gatewayResource is a dynamic client for the gateway CRs in e.namespace.
func (e *env) gatewayResource(t *testing.T) dynamic.ResourceInterface {
	t.Helper()
	cfg, err := gateway.RESTConfig("")
	require.NoError(t, err, "build the in-cluster config")
	dc, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err, "build the dynamic client")
	gvr := schema.GroupVersionResource{Group: gateway.GatewayGroup, Version: gateway.GatewayVersion, Resource: gateway.GatewayResourcePlural}
	return dc.Resource(gvr).Namespace(e.namespace)
}

// licenceEntry finds the licence env entry in the gateway CR's
// spec.podTemplate.envVars. Returns its index and value, or -1 and nil.
func (e *env) licenceEntry(t *testing.T, ctx context.Context) (int, map[string]any) {
	t.Helper()
	obj, err := e.gatewayResource(t).Get(ctx, e.gateway, metav1.GetOptions{})
	require.NoError(t, err, "read the gateway CR")
	envVars, _, err := unstructured.NestedSlice(obj.Object, "spec", "podTemplate", "envVars")
	require.NoError(t, err, "read spec.podTemplate.envVars")
	for i, v := range envVars {
		if m, ok := v.(map[string]any); ok && m["name"] == licenceEnvVar {
			return i, m
		}
	}
	return -1, nil
}

// dropGatewayLicence removes the licence env entry from the gateway CR's pod
// template with a JSON patch, the way the design's live probe did
// (~/claude-plans/kcp/gateway-drift-evidence/trial-probe.sh), and waits for
// the pods to roll onto trial limits and settle. A cleanup registered before
// the patch puts the saved entry back and waits for that roll too.
func (e *env) dropGatewayLicence(t *testing.T, ctx context.Context) {
	t.Helper()
	idx, entry := e.licenceEntry(t, ctx)
	require.GreaterOrEqualf(t, idx, 0, "the gateway CR's pod template must carry %s", licenceEnvVar)

	t.Cleanup(func() { e.restoreGatewayLicence(t, context.Background(), entry) })

	depGen, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	uids, err := e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	patch := fmt.Sprintf(`[{"op":"test","path":"/spec/podTemplate/envVars/%d/name","value":%q},{"op":"remove","path":"/spec/podTemplate/envVars/%d"}]`,
		idx, licenceEnvVar, idx)
	_, err = e.gatewayResource(t).Patch(ctx, e.gateway, types.JSONPatchType, []byte(patch), metav1.PatchOptions{})
	require.NoErrorf(t, err, "remove %s from the gateway CR's pod template", licenceEnvVar)
	t.Logf("\n✍️  GATEWAY LICENCE REMOVED ▸ %s dropped from the pod template; waiting for the pods to roll onto trial limits", licenceEnvVar)
	e.waitForGatewayRolled(t, ctx, depGen, uids)
}

// restoreGatewayLicence puts entry back on the gateway CR's pod template,
// unless an entry with its name is already there, and waits for the pods to
// roll and load the current CR.
func (e *env) restoreGatewayLicence(t *testing.T, ctx context.Context, entry map[string]any) {
	t.Helper()
	if idx, _ := e.licenceEntry(t, ctx); idx >= 0 {
		return
	}
	depGen, err := e.svc.GetGatewayDeploymentGeneration(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	uids, err := e.svc.GetGatewayPodUIDs(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	value, err := json.Marshal(entry)
	require.NoError(t, err)
	patch := fmt.Sprintf(`[{"op":"add","path":"/spec/podTemplate/envVars/-","value":%s}]`, value)
	_, err = e.gatewayResource(t).Patch(ctx, e.gateway, types.JSONPatchType, []byte(patch), metav1.PatchOptions{})
	require.NoErrorf(t, err, "put %s back on the gateway CR's pod template", licenceEnvVar)
	e.waitForGatewayRolled(t, ctx, depGen, uids)
	t.Logf("\n🧹 GATEWAY LICENCE RESTORED ▸ %s back on the pod template; the pods rolled and report the CR's configId", licenceEnvVar)
}

// gatewayRollTimeout bounds waitForGatewayRolled's wait.
const gatewayRollTimeout = 10 * time.Minute

// waitForGatewayRolled waits until a pod-template change has rolled the
// gateway: the operator has observed the CR, the Deployment's generation is
// past baseline and its rollout is complete, none of the old pods is listed,
// no pod is terminating, and every pod is Ready. It then requires kcp's pod
// check to find every pod reporting the CR's configId.
func (e *env) waitForGatewayRolled(t *testing.T, ctx context.Context, baseline int64, old map[types.UID]struct{}) {
	t.Helper()
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, 2*time.Second, 3*time.Minute),
		"the operator must accept the pod-template change")
	cs := kubeClient(t)
	var last string
	rolled := func() bool {
		dep, err := cs.AppsV1().Deployments(e.namespace).Get(ctx, e.gateway, metav1.GetOptions{})
		if err != nil {
			last = "read the gateway Deployment: " + err.Error()
			return false
		}
		s := dep.Status
		if dep.Generation <= baseline || s.ObservedGeneration < dep.Generation || s.Replicas == 0 ||
			s.UpdatedReplicas != s.Replicas || s.AvailableReplicas != s.Replicas {
			last = fmt.Sprintf("Deployment %s: generation %d (baseline %d) observed %d, %d replicas, %d updated, %d available",
				dep.Name, dep.Generation, baseline, s.ObservedGeneration, s.Replicas, s.UpdatedReplicas, s.AvailableReplicas)
			return false
		}
		pods, err := cs.CoreV1().Pods(e.namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + e.gateway})
		if err != nil {
			last = "list the gateway pods: " + err.Error()
			return false
		}
		ready := 0
		for _, p := range pods.Items {
			if _, stale := old[p.UID]; stale || p.DeletionTimestamp != nil {
				last = "gateway pod " + p.Name + " is from before the roll or terminating"
				return false
			}
			for _, c := range p.Status.Conditions {
				if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
					ready++
				}
			}
		}
		last = fmt.Sprintf("%d of %d gateway pods Ready", ready, len(pods.Items))
		return ready == len(pods.Items) && ready == int(s.Replicas)
	}
	start := time.Now()
	for !rolled() {
		require.Falsef(t, time.Since(start) > gatewayRollTimeout, "the gateway pods must roll within %s (last seen: %s)", gatewayRollTimeout, last)
		time.Sleep(2 * time.Second)
	}
	raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	ps, err := e.svc.CheckPodSync(ctx, e.namespace, e.gateway, raw, gateway.DefaultGatewayConfigPort)
	require.NoError(t, err, "read every gateway pod's /config")
	require.Equalf(t, gateway.PodsInSync, ps.Verdict, "the rolled pods must load the current CR (detail: %s)", ps.Detail)
	t.Logf("\n🔁 GATEWAY PODS ROLLED ▸ in %s; %s", time.Since(start).Round(time.Second), ps.Detail)
}

// crConfigID reads the live gateway CR's spec.configId.
func (e *env) crConfigID(t *testing.T, ctx context.Context) string {
	t.Helper()
	obj, err := e.gatewayResource(t).Get(ctx, e.gateway, metav1.GetOptions{})
	require.NoError(t, err, "read the gateway CR")
	id, _, err := unstructured.NestedString(obj.Object, "spec", "configId")
	require.NoError(t, err, "read spec.configId")
	require.NotEmpty(t, id, "a hot-reload gateway CR must carry spec.configId")
	return id
}

// switchByHand applies the switched route for topics to the live gateway CR
// with a fresh spec.configId, the way kcp's TBM switch would, and returns
// that configId. The rules are built with the reconcile core's own
// operations, in the order its switchover artifact applies them
// (migplan/reconcile/reconcile.go): drop kcp's fence for exactly these topics,
// then prepend the exact-name routing condition to the target domain. They are
// written as TBM's switch writes them (tbm.deriveSwitchRoutePatch): one route
// patch replacing the route's `rules` field, carrying the new configId. Using
// kcp's own operations rather than a hand-written JSON patch keeps the
// switched shape identical to what isSwitchedToTarget reads after a real
// switch. No generation guard: the test owns the CR at this point.
func (e *env) switchByHand(t *testing.T, ctx context.Context, topics []string) string {
	t.Helper()
	route := e.routeObj(t, e.readCR(t, ctx))
	require.NotNilf(t, route, "route %q must exist in the live CR", e.route)
	rawRules, _ := route["rules"].(map[string]any)
	rules, err := reconcile.ParseRules(rawRules)
	require.NoError(t, err, "parse the route's rules")
	rules.DropFence(topics)
	rules.PrependCondition(topics, e.destDomain)
	rulesYAML, err := rules.Serialize()
	require.NoError(t, err, "serialize the switched rules")
	value, err := gateway.FragmentValue(rulesYAML, "rules")
	require.NoError(t, err, "extract the rules fragment")

	before, _ := e.crGenerations(t, ctx)
	id, err := gateway.NewConfigID()
	require.NoError(t, err)
	stored, err := e.svc.PatchGatewayRoute(ctx, e.namespace, e.gateway, gateway.RoutePatch{RouteName: e.route, Field: "rules", Value: value}, id, 0)
	require.NoError(t, err, "patch the switched rules onto route %q", e.route)
	require.Equal(t, id, stored.ConfigID, "the gateway CR must store the configId sent")
	t.Logf("\n✍️  SWITCHED BY HAND ▸ route %q routes %d topic(s) to %q, kcp's fence dropped, spec.configId %s: metadata.generation %d → %d",
		e.route, len(topics), e.destDomain, id, before, stored.Generation)
	return id
}

// TestDrift_TerminatingPodRefusesUntilSettled pins the pod check's refusal
// while a replaced gateway pod is still terminating (design §5.2). On a
// hot-reload-off gateway the fence's rollout reports complete while the old
// pod terminates for up to its grace period, and that pod may still serve
// the unfenced config. A dry-run straight after the fenced run must refuse
// with the rollout-in-progress hint; once the gateway settles, the same
// dry-run must pass. The test never promotes, but it still reserves its slice
// so its plan is deterministic. Static only. Slice tbm-topic-091..095.
func TestDrift_TerminatingPodRefusesUntilSettled(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: a hot-reload gateway replaces no pods")
	}
	ctx := context.Background()
	e.resetStaticRoute(t, ctx)
	topics := e.topicRange(91, 95)
	mani := e.writeManifest(t, "drift-terminating-pod", topics)
	e.saveManifest(t, mani)
	e.snapshot(t, ctx, "before", "BEFORE (expect mirrors ACTIVE, route → source)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", cpFenced, "migration", "cutover", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "the run interrupted after the fence must exit non-zero")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be the kill point firing, not a real failure")

	out2, err2 := e.runKCPUnsettled(t, "kcp-run-2-dry-run-unsettled.log", "migration", "cutover", "execute", "--migration-yaml", mani, "--dry-run")
	require.Error(t, err2, "the dry-run must refuse while a replaced gateway pod is terminating")
	require.Contains(t, out2, "is still terminating", "the dry-run must name the terminating pod")
	require.Contains(t, out2, "If it completes, re-run.", "the dry-run must carry the rollout-in-progress hint")
	e.snapshot(t, ctx, "after-fence", "AFTER interrupt at fenced (expect fenced, mirrors ACTIVE)", topics)

	e.waitForGatewaySettled(t, ctx)
	out3, err3 := e.runKCP(t, "kcp-run-3-dry-run-settled.log", "", "migration", "cutover", "execute", "--migration-yaml", mani, "--dry-run")
	require.NoError(t, err3, "once the gateway settles the dry-run must pass")
	require.NotContains(t, out3, "is still terminating", "no gateway pod may be terminating once settled")

	t.Logf("\n✅ RESULT: a dry-run refused while a replaced gateway pod terminated, and passed once the gateway settled.")
}

// TestDrift_GatewayPodRestartRerunProceeds pins that a restarted hot-reload
// gateway pod doesn't deadlock a resume. Run 1 is killed after promote, so
// the fence and the topics on the target both count as done, and a "cannot
// tell" verdict would refuse. Every gateway pod is then deleted. If a pod
// that boots reported no configId until the next hot-reload, the pod check
// would refuse, and a refusal never writes, so nothing would set one. The
// replacement pods must report the CR's configId at boot: the dry-run passes
// the pod check, and the re-run completes. Dynamic only. Slice
// tbm-topic-096..100.
func TestDrift_GatewayPodRestartRerunProceeds(t *testing.T) {
	e := newEnv()
	if e.mode != "dynamic" {
		t.Skip("hot-reload gateways only: the static gateway has no configId to report")
	}
	topics := e.topicRange(96, 100)
	runResumeScenario(t, e, cpPromoted, "drift-pod-restart", topics, func(t *testing.T, ctx context.Context) {
		e.restartGatewayPods(t, ctx)

		out, err := e.runKCP(t, "kcp-run-dry-run-after-restart.log", "", "migration", "cutover", "execute", "--migration-yaml", e.manifestPath("drift-pod-restart.yaml"), "--dry-run")
		require.NoError(t, err, "the dry-run must pass: the restarted pods report the CR's configId")
		require.Contains(t, out, "✓ gateway pods serve the CR", "the pod check must pass, not be skipped")
		require.NotContains(t, out, "nothing to compare", "no restarted pod may report a missing configId")
	})
}

// restartGatewayPods deletes every gateway pod and waits until the
// Deployment's replacements are all Ready, none of the deleted pods is
// listed, and none is terminating.
func (e *env) restartGatewayPods(t *testing.T, ctx context.Context) {
	t.Helper()
	cs := kubeClient(t)
	selector := metav1.ListOptions{LabelSelector: "app=" + e.gateway}
	before, err := cs.CoreV1().Pods(e.namespace).List(ctx, selector)
	require.NoError(t, err, "list the gateway pods")
	require.NotEmpty(t, before.Items, "the gateway must have pods to restart")
	old := map[string]bool{}
	for _, p := range before.Items {
		old[string(p.UID)] = true
		require.NoErrorf(t, cs.CoreV1().Pods(e.namespace).Delete(ctx, p.Name, metav1.DeleteOptions{}), "delete gateway pod %s", p.Name)
	}
	want := len(before.Items)

	var last string
	replaced := func() bool {
		pods, err := cs.CoreV1().Pods(e.namespace).List(ctx, selector)
		if err != nil {
			last = "list the gateway pods: " + err.Error()
			return false
		}
		ready := 0
		for _, p := range pods.Items {
			if old[string(p.UID)] || p.DeletionTimestamp != nil {
				last = "gateway pod " + p.Name + " is still listed or terminating"
				return false
			}
			for _, c := range p.Status.Conditions {
				if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
					ready++
				}
			}
		}
		last = fmt.Sprintf("%d of %d replacement pods Ready", ready, want)
		return ready == want && len(pods.Items) == want
	}
	deadline := time.Now().Add(3 * time.Minute)
	for !replaced() {
		require.Falsef(t, time.Now().After(deadline), "the gateway pods must be replaced within 3m (last seen: %s)", last)
		time.Sleep(2 * time.Second)
	}
	t.Logf("\n🔁 GATEWAY PODS RESTARTED ▸ %d pod(s) deleted and replaced; every replacement Ready", want)
}

// The CFK operator, as e2e_install_operator installs it
// (integration-tests/lib/cp-e2e-env.sh): Helm release confluent-operator, its
// pods labelled app=confluent-operator.
const (
	operatorDeployment = "confluent-operator"
	operatorPodLabel   = "app=confluent-operator"
)

// kubeClient is a clientset for the runner pod's service account.
func kubeClient(t *testing.T) kubernetes.Interface {
	t.Helper()
	cfg, err := rest.InClusterConfig()
	require.NoError(t, err, "in-cluster config (the suite runs in the runner pod)")
	cs, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err, "build the clientset")
	return cs
}

// scaleOperator sets the CFK operator Deployment's replicas.
func (e *env) scaleOperator(t *testing.T, ctx context.Context, cs kubernetes.Interface, replicas int32) {
	t.Helper()
	deps := cs.AppsV1().Deployments(e.namespace)
	scale, err := deps.GetScale(ctx, operatorDeployment, metav1.GetOptions{})
	require.NoError(t, err, "read the operator's scale")
	scale.Spec.Replicas = replicas
	_, err = deps.UpdateScale(ctx, operatorDeployment, scale, metav1.UpdateOptions{})
	require.NoErrorf(t, err, "scale the operator to %d", replicas)
}

// operatorPods lists the CFK operator's pods, terminating ones included. It
// returns the error rather than failing the test, because it's polled from
// require.Eventually, which runs its condition off the test goroutine.
func (e *env) operatorPods(ctx context.Context, cs kubernetes.Interface) ([]corev1.Pod, error) {
	pods, err := cs.CoreV1().Pods(e.namespace).List(ctx, metav1.ListOptions{LabelSelector: operatorPodLabel})
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

// stopOperator scales the CFK operator to 0 and waits until none of its pods
// is listed, so not even a terminating pod can still reconcile a CR change.
// The gateway pods keep running, but no change to the gateway CR reaches
// them. When the test ends it scales the operator back to 1, waits for it to
// be Ready, then waits for it to accept the gateway CR, so the next test
// starts from a settled gateway.
func (e *env) stopOperator(t *testing.T, ctx context.Context) {
	t.Helper()
	cs := kubeClient(t)
	// Registered before the scale-down, so a failure anywhere after it still
	// brings the operator back. Scaling a running operator to 1 is a no-op.
	t.Cleanup(func() {
		ctx := context.Background()
		e.scaleOperator(t, ctx, cs, 1)
		require.Eventually(t, func() bool {
			pods, err := e.operatorPods(ctx, cs)
			if err != nil {
				return false
			}
			for _, p := range pods {
				for _, c := range p.Status.Conditions {
					if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
						return true
					}
				}
			}
			return false
		}, 3*time.Minute, 2*time.Second, "the operator must come back Ready")
		require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, 2*time.Second, 3*time.Minute),
			"the restored operator must accept the gateway CR")
		t.Logf("\n🧹 CFK OPERATOR RESTORED ▸ %s scaled back to 1 and the gateway CR accepted", operatorDeployment)
	})
	e.scaleOperator(t, ctx, cs, 0)
	require.Eventually(t, func() bool {
		pods, err := e.operatorPods(ctx, cs)
		return err == nil && len(pods) == 0
	}, 2*time.Minute, 2*time.Second, "every operator pod must be gone")
	t.Logf("\n✍️  CFK OPERATOR STOPPED ▸ %s scaled to 0, no operator pod left: gateway CR changes no longer reach the pods", operatorDeployment)
}

// crGenerations reads the live gateway CR's metadata.generation and
// status.observedGeneration: the spec revision the CR holds, and the last one
// the operator observed.
func (e *env) crGenerations(t *testing.T, ctx context.Context) (generation, observed int64) {
	t.Helper()
	raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	var cr struct {
		Metadata struct {
			Generation *int64 `yaml:"generation"`
		} `yaml:"metadata"`
		Status struct {
			ObservedGeneration *int64 `yaml:"observedGeneration"`
		} `yaml:"status"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &cr), "parse the gateway CR")
	require.NotNil(t, cr.Metadata.Generation, "the gateway CR must carry metadata.generation")
	require.NotNil(t, cr.Status.ObservedGeneration, "the gateway CR must carry status.observedGeneration")
	return *cr.Metadata.Generation, *cr.Status.ObservedGeneration
}

// gatewayBootstrap is the static route's client endpoint, as
// testdata/manifests/templates/gateway-static.yaml declares it.
func (e *env) gatewayBootstrap() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local:9595", e.gateway, e.namespace)
}

// requireGatewayCarriesProduce retries produceThroughGateway against topic
// for up to 3 minutes until at least one send is acked, and fails the test if
// none is. A static route reset rolls the gateway pods, and an old pod may
// still serve the previous test's switched route for a while. Returns the
// acks of the attempt that succeeded.
func (e *env) requireGatewayCarriesProduce(t *testing.T, topic string) int {
	t.Helper()
	var (
		acks     int
		firstErr error
	)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		acks, firstErr = produceThroughGateway(t, e.gatewayBootstrap(), topic, 5)
		if acks >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	require.GreaterOrEqualf(t, acks, 1, "the gateway must carry produce traffic before the migration, or a fenced-probe later proves nothing (first error: %v)", firstErr)
	t.Logf("\n🔍 DATA PLANE ▸ %d/5 produce attempts acked through %s before the migration", acks, e.gatewayBootstrap())
	return acks
}

// produceThroughGateway sends attempts messages to topic through the gateway
// and counts the acknowledged ones. It never fails the test: a producer that
// can't be created counts as no acks. firstErr is the first failure: after a
// few, sarama's circuit breaker fails later sends without reaching the gateway
// (ErrBreakerOpen). The config matches the migration-hot-reload suite's
// startProducerProbe: no retries, so a send is one attempt against whatever
// the gateway serves right now.
func produceThroughGateway(t *testing.T, bootstrap, topic string, attempts int) (acks int, firstErr error) {
	t.Helper()
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_6_0_0
	cfg.ClientID = "kcp-drift-probe"
	cfg.Producer.Return.Successes = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Retry.Max = 0
	cfg.Metadata.Retry.Max = 0
	cfg.Producer.Timeout = 3 * time.Second
	cfg.Metadata.Full = false
	cfg.Net.DialTimeout = 3 * time.Second
	cfg.Net.ReadTimeout = 3 * time.Second
	cfg.Net.WriteTimeout = 3 * time.Second

	p, err := sarama.NewSyncProducer([]string{bootstrap}, cfg)
	if err != nil {
		return 0, err
	}
	defer func() { _ = p.Close() }()
	for i := 0; i < attempts; i++ {
		_, _, err := p.SendMessage(&sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.StringEncoder(time.Now().UTC().Format(time.RFC3339Nano)),
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		acks++
	}
	return acks, firstErr
}
