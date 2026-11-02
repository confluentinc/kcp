//go:build e2e

// Package idempotent_fsm_e2e proves the migration execution FSMs are idempotent
// from any interruption point, against a live dynamic-mode Confluent Gateway with
// hot reload (Minikube profile kcp-e2e-idempotent-<mode>). Each test drives the real
// `kcp migration cutover execute` command, interrupts it at a chosen checkpoint via the
// killpoint seam (KCP_TEST_CANCEL_AFTER), inspects the genuine partial world it
// leaves (live gateway CR, live mirror state), then re-runs the SAME manifest and
// asserts it drives to completion.
//
// Like the sibling suites, this binary runs INSIDE the cluster (see
// manifests/kcp-runner.yaml): reconcile reads the live gateway CR with in-cluster
// config and the test execs /workspace/kcp as a local subprocess. run.sh compiles
// the package for the node arch, ships it plus the kcp binary and rendered
// manifests into the runner pod, and executes it there with the non-secret
// KCP_TBM_* env; the destination SASL credential arrives via pod-spec env from the
// tbm-rest-credentials Secret.
package idempotent_fsm_e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// kcpBinary is the in-pod path run.sh copies the linux kcp binary to.
const kcpBinary = "/workspace/kcp"

// Kill-point checkpoints — FSM state names the killpoint seam (KCP_TEST_CANCEL_AFTER)
// matches against to cancel a run right after that step. Values mirror the FSM
// state constants in internal/services/migration{,/tbm} (StateFenced etc.); a
// mismatch would surface as the interrupt never firing (the run completes),
// which the resume tests catch by requiring a non-zero exit.
const (
	cpFenced           = "fenced"
	cpOffsetSyncPaused = "offset_sync_paused" // static only
	cpFenceVerified    = "fence_verified"
	cpPromoted         = "promoted"
	cpSwitched         = "switched"
	cpPromoteAccepted  = killpoint.AfterPromoteAccepted // intra-step: accepted, not yet STOPPED
)

// nothingToDoLine is what execute prints when reconcile finds nothing to do, in
// which case no state machine runs; fsmStartBanner is the first step banner both
// state machines print ("Initializing migration..." / "Initializing TBM
// migration..."), so its presence means a state machine ran.
const (
	nothingToDoLine = "nothing to do: no topic in it still needs migrating"
	fsmStartBanner  = "Initializing"
)

// requireNothingToDo asserts an execute run found nothing to do and ran no
// state machine.
func requireNothingToDo(t *testing.T, out string) {
	t.Helper()
	require.Contains(t, out, nothingToDoLine, "execute must report nothing to do")
	require.NotContains(t, out, fsmStartBanner, "no state machine may run when there is nothing to do")
}

// requireStateMachineRan asserts an execute run had work and ran its state
// machine.
func requireStateMachineRan(t *testing.T, out string) {
	t.Helper()
	require.Contains(t, out, fsmStartBanner, "the state machine must run when there is work left")
	require.NotContains(t, out, nothingToDoLine, "a run with work left must not report nothing to do")
}

// offsetSyncEnableKey is the cluster-link config the static FSM's offset-sync
// pause disables and its restore sets back to the declared baseline.
const offsetSyncEnableKey = "consumer.offset.sync.enable"

// executeTimeout bounds a single execute invocation (a full cutover incl.
// gateway rollouts and mirror-STOPPED waits).
const executeTimeout = 10 * time.Minute

// env holds the live topology, read from the KCP_TBM_* environment (setup.sh's
// generated .env plus the two secret-backed pod-spec vars). svc is kcp's own
// gateway service built in-cluster (empty kubeconfig ⇒ in-cluster service account).
type env struct {
	mode              string // "dynamic" (TBM) or "static" (AAO)
	namespace         string
	gateway           string
	route             string
	restEndpoint      string
	destClusterID     string
	destBootstrap     string
	destDomain        string
	sourceDomain      string // static only: route.streamingDomain.name to reset to
	sourceBootstrapID string // static only: route.streamingDomain.bootstrapServerId
	linkName          string
	topicPrefix       string
	renderedDir       string
	saslUser          string
	saslPassword      string
	sourceBootstrap   string
	reportsDir        string // in-pod folder for this run's per-test reports; "" = off

	svc     *gateway.K8sService
	linkSvc clusterlink.Service
}

func newEnv() *env {
	return &env{
		mode:              envOrDefault("KCP_TBM_GATEWAY_MODE", "dynamic"),
		namespace:         envOrDefault("KCP_TBM_NAMESPACE", "confluent"),
		gateway:           envOrDefault("KCP_TBM_GATEWAY_NAME", "tbm-gateway"),
		route:             envOrDefault("KCP_TBM_ROUTE_NAME", "tbm-route"),
		restEndpoint:      os.Getenv("KCP_TBM_REST_ENDPOINT"),
		destClusterID:     os.Getenv("KCP_TBM_DEST_CLUSTER_ID"),
		destBootstrap:     os.Getenv("KCP_TBM_DEST_BOOTSTRAP"),
		destDomain:        envOrDefault("KCP_TBM_DEST_DOMAIN", "destination-domain"),
		sourceDomain:      envOrDefault("KCP_TBM_SOURCE_DOMAIN", "source-kafka-cluster"),
		sourceBootstrapID: envOrDefault("KCP_TBM_SOURCE_BOOTSTRAP_ID", "UNAUTHED"),
		linkName:          envOrDefault("KCP_TBM_CLUSTER_LINK_NAME", "tbm-link"),
		topicPrefix:       envOrDefault("KCP_TBM_TOPIC_PREFIX", "tbm-topic-"),
		renderedDir:       envOrDefault("KCP_TBM_RENDERED_DIR", "/workspace/rendered"),
		saslUser:          os.Getenv("KCP_TBM_DEST_SASL_USER"),
		saslPassword:      os.Getenv("KCP_TBM_DEST_SASL_PASSWORD"),
		sourceBootstrap:   os.Getenv("KCP_TBM_SOURCE_BOOTSTRAP"),
		reportsDir:        os.Getenv("KCP_TBM_REPORTS_DIR"),

		svc:     gateway.NewK8sService(""),
		linkSvc: clusterlink.NewConfluentCloudService(http.DefaultClient),
	}
}

// resetStaticRoute returns the static/AAO route to its pristine (source-bound,
// unfenced) shape so the next migration in the same env starts clean. A static
// switch is WHOLE-ROUTE (streamingDomain flip + route-level fence), so — unlike
// dynamic's per-topic slices — each migration consumes the whole route.
//
// It reads the live route, flips streamingDomain back to source, drops any fence,
// and whole-route-replaces (RoutePatch.Field == "", as SwitchGateway does),
// preserving everything else (endpoint, security, broker strategy). Promoted
// mirrors stay promoted, so each test still reserves a disjoint slice.
// No-op on dynamic; idempotent.
func (e *env) resetStaticRoute(t *testing.T, ctx context.Context) {
	t.Helper()
	if e.mode != "static" {
		return // dynamic uses disjoint slices; nothing to reset
	}

	before := e.readCR(t, ctx)
	route := e.routeObj(t, before)
	require.NotNilf(t, route, "route %q must exist in the live CR to reset it", e.route)

	route["streamingDomain"] = map[string]any{
		"name":              e.sourceDomain,
		"bootstrapServerId": e.sourceBootstrapID,
	}
	delete(route, "fence") // the pristine (pre-migration) route carries no kcp fence

	rp := gateway.RoutePatch{RouteName: e.route, Value: route} // Field "" ⇒ whole-route replace
	_, err := e.svc.PatchGatewayRoute(ctx, e.namespace, e.gateway, rp, "", 0)
	require.NoErrorf(t, err, "reset static route %q to source domain %q", e.route, e.sourceDomain)

	// Let the operator accept the rewritten route before the next migration
	// reads it, so reconcile sees a settled source-bound, unfenced route.
	require.NoError(t, e.svc.WaitForGatewayAccepted(ctx, e.namespace, e.gateway, 2*time.Second, 2*time.Minute),
		"gateway must accept the route reset")

	t.Logf("\n♻️  STATIC ROUTE RESET ▸ %q flipped back to source domain %q, fence dropped (next migration starts clean)", e.route, e.sourceDomain)
	e.snapshot(t, ctx, "", "AFTER static route reset (expect route → source-domain, fence cleared)", nil)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// topicName is the zero-padded source topic name for an index (1 ⇒ tbm-topic-001),
// matching setup.sh so the Go and shell sides agree on names.
func (e *env) topicName(i int) string {
	return fmt.Sprintf("%s%03d", e.topicPrefix, i)
}

// topicRange returns the sorted topic names for the inclusive index range [lo,hi].
// Tests reserve disjoint ranges so their promotions (irreversible) never collide.
func (e *env) topicRange(lo, hi int) []string {
	out := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, e.topicName(i))
	}
	sort.Strings(out)
	return out
}

// manifestPath resolves a rendered manifest filename to its in-pod path.
func (e *env) manifestPath(name string) string {
	return filepath.Join(e.renderedDir, name)
}

// writeManifest renders a GatewayMigration manifest for exactly the given topics
// and writes it into the in-pod rendered dir (the test runs in-cluster), so each
// test can target its own disjoint slice without a fixed batch template. Reuses
// the rendered credential files setup.sh already staged. Returns the in-pod path.
func (e *env) writeManifest(t *testing.T, name string, topics []string) string {
	t.Helper()
	return e.renderManifest(t, name, topics, "", "")
}

// writeManifestPausingOffsetSync is writeManifest with
// spec.staticMigration.consumerOffsetSync.pause set, declaring an "enabled"
// baseline — the static FSM then disables consumer.offset.sync.enable right
// after fencing and restores it to enabled after switchover. Static only: a
// dynamicMigration cannot carry the pause, so callers gate it on e.mode.
func (e *env) writeManifestPausingOffsetSync(t *testing.T, name string, topics []string) string {
	t.Helper()
	require.Equal(t, "static", e.mode, "the offset-sync pause exists only under staticMigration")
	return e.renderManifest(t, name, topics,
		"    consumerOffsetSync:\n      pause: true\n      baseline: enabled\n", "")
}

// writeManifestWithPromoteBatchSize is writeManifest with
// the declared section's policies.promoteBatchSize set, so the promote step submits at most
// n topics per request and waits for each batch to reach STOPPED before the next.
func (e *env) writeManifestWithPromoteBatchSize(t *testing.T, name string, topics []string, n int) string {
	t.Helper()
	return e.renderManifest(t, name, topics, "",
		fmt.Sprintf("    policies:\n      promoteBatchSize: %d\n", n))
}

// renderManifest writes the manifest for writeManifest and its variants. The
// declared section follows the suite's gateway mode: dynamicMigration (TBM) or
// staticMigration (AAO). offsetSyncExtra is the section's consumerOffsetSync
// block (static only) and policiesExtra its policies block; both are appended
// verbatim after topicGroup, at the section's child indent (4 spaces).
func (e *env) renderManifest(t *testing.T, name string, topics []string, offsetSyncExtra, policiesExtra string) string {
	t.Helper()
	var tb strings.Builder
	for _, tp := range topics {
		fmt.Fprintf(&tb, "          - %q\n", tp)
	}
	section := "dynamicMigration"
	if e.mode == "static" {
		section = "staticMigration"
	}
	manifest := fmt.Sprintf(`apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: %s
spec:
  source:
    type: apache-kafka
    bootstrapServers:
      - %q
    credentials: /workspace/rendered/source-creds.yaml
  target:
    type: confluent-platform
    clusterId: %q
    kafka:
      bootstrapServers:
        - %q
      restEndpoint: %q
      clusterCredentials: /workspace/rendered/dest-kafka-creds.yaml
  clusterLink:
    name: %q
    bootstrapServers:
      - %q
    linkCredentials: /workspace/rendered/link-creds.yaml
  gateway:
    namespace: %q
    cr-name: %q
  %s:
    route:
      name: %q
      targetStreamingDomain: %q
    topicGroup:
      - topics:
%s%s%s`, name, e.sourceBootstrap, e.destClusterID, e.destBootstrap, e.restEndpoint,
		e.linkName, e.destBootstrap, e.namespace, e.gateway, section, e.route, e.destDomain, tb.String(),
		offsetSyncExtra, policiesExtra)

	path := filepath.Join(e.renderedDir, name+".yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600), "write generated manifest")
	return path
}

// linkConfig is the destination cluster-link REST config; the SASL user/password
// (from the tbm-rest-credentials Secret via pod-spec env) become HTTP basic auth.
func (e *env) linkConfig() clusterlink.Config {
	return clusterlink.Config{
		RestEndpoint: e.restEndpoint,
		ClusterID:    e.destClusterID,
		LinkName:     e.linkName,
		APIKey:       e.saslUser,
		APISecret:    e.saslPassword,
	}
}

// mirrorStatus reads the live per-topic mirror status from the cluster link
// (ACTIVE / PENDING_STOPPED / STOPPED). Used to assert the partial world an
// interrupted promote leaves.
func (e *env) mirrorStatus(t *testing.T, ctx context.Context) map[string]string {
	t.Helper()
	mirrors, err := e.linkSvc.ListMirrorTopics(ctx, e.linkConfig())
	require.NoError(t, err, "list mirror topics")
	out := make(map[string]string, len(mirrors))
	for _, m := range mirrors {
		out[m.MirrorTopicName] = m.MirrorStatus
	}
	return out
}

// promoteStatusTimeout bounds mirrorStatusAfterPromote's wait.
const promoteStatusTimeout = time.Minute

// mirrorStatusAfterPromote polls the live mirror status until at least n of
// topics have left ACTIVE (PENDING_STOPPED or STOPPED), then returns it. The
// cluster link reports an accepted promote with a short lag, so a read made
// straight after a kill at the promote point can still see every mirror
// ACTIVE. After promoteStatusTimeout it returns the last status read, for the
// caller's assertions to report.
func (e *env) mirrorStatusAfterPromote(t *testing.T, ctx context.Context, topics []string, n int) map[string]string {
	t.Helper()
	deadline := time.Now().Add(promoteStatusTimeout)
	for {
		ms := e.mirrorStatus(t, ctx)
		left := 0
		for _, tp := range topics {
			if s := ms[tp]; s == "PENDING_STOPPED" || s == "STOPPED" {
				left++
			}
		}
		if left >= n || time.Now().After(deadline) {
			return ms
		}
		time.Sleep(2 * time.Second)
	}
}

// linkOffsetSync reads the live cluster link's consumer.offset.sync.enable value
// ("true" or "false").
func (e *env) linkOffsetSync(t *testing.T, ctx context.Context) string {
	t.Helper()
	cfgs, err := e.linkSvc.ListConfigs(ctx, e.linkConfig())
	require.NoError(t, err, "list cluster link configs")
	v, ok := cfgs[offsetSyncEnableKey]
	require.Truef(t, ok, "cluster link has no %s config", offsetSyncEnableKey)
	return v
}

// runKCP execs the in-pod kcp binary as a local subprocess (this test already
// runs inside the cluster). When cancelAfter is non-empty it sets the killpoint
// env var so the run cancels itself right after that checkpoint (simulating an
// abrupt Ctrl-C); empty means a normal, uninterrupted run. The command, its full
// output and its exit result are also saved as reportName in the test's report
// folder. Returns combined stdout+stderr and the process error.
func (e *env) runKCP(t *testing.T, reportName, cancelAfter string, args ...string) (string, error) {
	t.Helper()
	var seam *testSeam
	if cancelAfter != "" {
		seam = &testSeam{env: []string{killpoint.EnvVar + "=" + cancelAfter}, effect: "simulated abrupt exit"}
	}
	return e.runKCPWith(t, reportName, seam, args...)
}

// runKCPFailingAt is runKCP with the test-only failure hook set to failAt (a
// workflow step such as "verify_fence"): that step fails the way a real failure
// of it would, instead of running.
func (e *env) runKCPFailingAt(t *testing.T, reportName, failAt string, args ...string) (string, error) {
	t.Helper()
	return e.runKCPWith(t, reportName, &testSeam{env: []string{killpoint.FailEnvVar + "=" + failAt}, effect: "simulated step failure"}, args...)
}

// testSeam is one of kcp's test-only environment switches (see the killpoint
// package) set on a single run.
type testSeam struct {
	env    []string // NAME=value, one per switch
	effect string   // what it does to the run, for the logs
}

// runKCPWith runs kcp with seam (nil for a normal run) in its environment,
// once the gateway has settled (waitForGatewaySettled).
func (e *env) runKCPWith(t *testing.T, reportName string, seam *testSeam, args ...string) (string, error) {
	t.Helper()
	e.waitForGatewaySettled(t, context.Background())
	return e.execKCP(t, reportName, seam, args...)
}

// holdPollInterval and holdTimeout bound runKCPHeldAt's wait for kcp to reach
// its hold point.
const (
	holdPollInterval = 500 * time.Millisecond
	holdTimeout      = 5 * time.Minute
)

// runKCPHeldAt runs kcp with the test-only hold seam set to state (an FSM
// state name such as "fence_verified"): once kcp reaches that state it pauses,
// whileHeld runs against the live world, and kcp is then released and runs on.
// The gateway settles first, as for runKCPWith. Returns kcp's output and
// process error.
func (e *env) runKCPHeldAt(t *testing.T, reportName, state string, whileHeld func(), args ...string) (string, error) {
	t.Helper()
	return e.runKCPHeldAtWith(t, reportName, state, nil, whileHeld, args...)
}

// runKCPHeldAtWith is runKCPHeldAt with extraEnv (NAME=value, such as another
// test seam) added to kcp's environment alongside the hold.
func (e *env) runKCPHeldAtWith(t *testing.T, reportName, state string, extraEnv []string, whileHeld func(), args ...string) (string, error) {
	t.Helper()
	e.waitForGatewaySettled(t, context.Background())

	release := filepath.Join(t.TempDir(), "release")
	held := release + ".held"
	releaseKCP := func() {
		if f, err := os.OpenFile(release, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
		}
	}
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	received := false
	// Registered before kcp starts, so a failed require below never leaves kcp
	// blocked until the execute timeout. It also waits for the kcp goroutine,
	// which logs through t, so that it never outlives the test.
	t.Cleanup(func() {
		releaseKCP()
		if received {
			return
		}
		select {
		case <-done:
		case <-time.After(executeTimeout):
		}
	})

	seam := &testSeam{
		env:    append([]string{killpoint.HoldEnvVar + "=" + state, killpoint.HoldFileEnvVar + "=" + release}, extraEnv...),
		effect: "holds after " + state + " until released",
	}
	go func() {
		out, err := e.execKCP(t, reportName, seam, args...)
		done <- result{out, err}
	}()

	deadline := time.Now().Add(holdTimeout)
	for {
		if _, err := os.Stat(held); err == nil {
			break
		}
		require.Falsef(t, time.Now().After(deadline), "kcp must reach its hold point after %q within %s", state, holdTimeout)
		// kcp exiting before its hold point ends the wait at once, with its
		// output; the result is recorded so the cleanup doesn't wait again.
		select {
		case r := <-done:
			received = true
			require.FailNowf(t, "kcp exited before its hold point",
				"kcp must reach its hold point after %q, but exited first (error: %v). Output:\n%s", state, r.err, r.out)
		case <-time.After(holdPollInterval):
		}
	}
	t.Logf("\nKCP HELD ▸ paused after %q; changing the world now", state)
	if whileHeld != nil {
		whileHeld()
	}
	releaseKCP()
	t.Logf("\nKCP RELEASED ▸ the run continues from %q", state)
	r := <-done
	received = true
	return r.out, r.err
}

// bumpGatewayGeneration changes the gateway CR's spec out from under kcp, the
// way an operator's edit would: it JSON-patches spec.configId to a fresh
// "e2e-bump-<unix nanos>" value. That moves metadata.generation, which the
// call requires to have risen. Returns the new generation.
func (e *env) bumpGatewayGeneration(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	before, _ := e.crGenerations(t, ctx)

	cfg, err := gateway.RESTConfig("")
	require.NoError(t, err, "build the in-cluster config")
	dc, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err, "build the dynamic client")
	gvr := schema.GroupVersionResource{Group: gateway.GatewayGroup, Version: gateway.GatewayVersion, Resource: gateway.GatewayResourcePlural}

	value := fmt.Sprintf("e2e-bump-%d", time.Now().UnixNano())
	patch := fmt.Sprintf(`[{"op":"add","path":"/spec/configId","value":%q}]`, value)
	_, err = dc.Resource(gvr).Namespace(e.namespace).Patch(ctx, e.gateway, types.JSONPatchType, []byte(patch), metav1.PatchOptions{})
	require.NoError(t, err, "patch spec.configId on the gateway CR")

	after, _ := e.crGenerations(t, ctx)
	require.Greaterf(t, after, before, "patching spec.configId must raise metadata.generation (was %d)", before)
	t.Logf("\nGATEWAY CR CHANGED ▸ spec.configId set to %s: metadata.generation %d → %d", value, before, after)
	return after
}

// runKCPUnsettled is a normal run that doesn't wait for the gateway to
// settle first, for the test that pins kcp's refusal while it hasn't.
func (e *env) runKCPUnsettled(t *testing.T, reportName string, args ...string) (string, error) {
	t.Helper()
	return e.execKCP(t, reportName, nil, args...)
}

// settleTimeout bounds waitForGatewaySettled's wait.
const settleTimeout = 2 * time.Minute

// waitForGatewaySettled waits until the gateway Deployment reports a complete
// rollout and no gateway pod is terminating. On a hot-reload-off gateway the
// rollout reports complete while a replaced pod still terminates, and kcp's
// pod check refuses until it's gone, so a test that resumes straight after an
// interrupted run waits here first. It doesn't wait for the operator to
// observe the CR, so it returns while the operator is stopped. Static only; a
// no-op on dynamic, whose gateway hot-reloads without replacing pods.
func (e *env) waitForGatewaySettled(t *testing.T, ctx context.Context) {
	t.Helper()
	if e.mode != "static" {
		return
	}
	cs := kubeClient(t)
	var last string
	settled := func() bool {
		dep, err := cs.AppsV1().Deployments(e.namespace).Get(ctx, e.gateway, metav1.GetOptions{})
		if err != nil {
			last = "read the gateway Deployment: " + err.Error()
			return false
		}
		s := dep.Status
		if s.ObservedGeneration < dep.Generation || s.UpdatedReplicas != s.Replicas || s.AvailableReplicas != s.Replicas || s.Replicas == 0 {
			last = fmt.Sprintf("Deployment %s rolling out: generation %d observed %d, %d replicas, %d updated, %d available",
				dep.Name, dep.Generation, s.ObservedGeneration, s.Replicas, s.UpdatedReplicas, s.AvailableReplicas)
			return false
		}
		pods, err := cs.CoreV1().Pods(e.namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + e.gateway})
		if err != nil {
			last = "list the gateway pods: " + err.Error()
			return false
		}
		for _, p := range pods.Items {
			if p.DeletionTimestamp != nil {
				last = "gateway pod " + p.Name + " is terminating"
				return false
			}
		}
		return true
	}
	start := time.Now()
	deadline := start.Add(settleTimeout)
	for !settled() {
		require.Falsef(t, time.Now().After(deadline), "the gateway must settle within %s before the next kcp run (last seen: %s)", settleTimeout, last)
		time.Sleep(2 * time.Second)
	}
	if waited := time.Since(start); waited >= time.Second {
		t.Logf("\n⏳ GATEWAY SETTLED ▸ waited %s for the rollout to complete and no gateway pod to be terminating", waited.Round(time.Second))
	}
}

// execKCP runs kcp with seam (nil for a normal run) in its environment.
func (e *env) execKCP(t *testing.T, reportName string, seam *testSeam, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), executeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, kcpBinary, args...)
	cmd.Env = os.Environ()
	desc := "kcp " + strings.Join(args, " ")
	if seam != nil {
		cmd.Env = append(cmd.Env, seam.env...)
		desc += "    [" + strings.Join(seam.env, " ") + " → " + seam.effect + "]"
	}
	out, err := cmd.CombinedOutput()

	// Tangible evidence: log the FULL raw kcp output of every run, never
	// truncated, never suppressed. Surfaces in `go test -v` (run.sh), which is
	// captured to a host report file. The exit result is shown too — an
	// interrupted run is EXPECTED to exit non-zero.
	t.Logf("\n"+
		"┌─────────────────────────────────────────────────────────────────────────────┐\n"+
		"│ RAW KCP RUN ▸ %s\n"+
		"└─────────────────────────────────────────────────────────────────────────────┘\n"+
		"%s\n"+
		"───── process exit: err=%v ─────\n", desc, out, err)
	e.saveReport(t, reportName, fmt.Sprintf("$ %s\n\n%s\n───── process exit: err=%v ─────\n", desc, out, err))
	return string(out), err
}

// testReportDir is this test's folder in the run's reports dir, created on first
// use; "" when reports are off (KCP_TBM_REPORTS_DIR unset).
func (e *env) testReportDir(t *testing.T) string {
	t.Helper()
	if e.reportsDir == "" {
		return ""
	}
	dir := filepath.Join(e.reportsDir, t.Name())
	require.NoError(t, os.MkdirAll(dir, 0o755), "create the test's report folder")
	return dir
}

// saveReport writes one file into the test's report folder; a no-op when
// reports are off.
func (e *env) saveReport(t *testing.T, name, content string) {
	t.Helper()
	dir := e.testReportDir(t)
	if dir == "" {
		return
	}
	require.NoErrorf(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644), "write report %s", name)
}

// saveManifest copies the manifest at path into the test's report folder as
// manifest.yaml.
func (e *env) saveManifest(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read the generated manifest")
	e.saveReport(t, "manifest.yaml", string(raw))
}

// offsetSyncForReport reads the cluster link's consumer.offset.sync.enable for
// a checkpoint report without failing the test when it can't.
func (e *env) offsetSyncForReport(ctx context.Context) string {
	cfgs, err := e.linkSvc.ListConfigs(ctx, e.linkConfig())
	if err != nil {
		return "(unavailable: " + err.Error() + ")"
	}
	if v, ok := cfgs[offsetSyncEnableKey]; ok {
		return v
	}
	return "(not set)"
}

// snapshot logs a tangible before/after picture of the live world for the given
// topics: the gateway route's fence + routing rules (verbatim from the live CR),
// each topic's live mirror status, and the cluster link's
// consumer.offset.sync.enable. Called before and after every run so a human
// reviewer can see exactly what changed. When report is non-empty the same
// picture, plus the full gateway CR, is saved as <report>.md in the test's
// report folder.
func (e *env) snapshot(t *testing.T, ctx context.Context, report, label string, topics []string) {
	t.Helper()
	cr := e.readCR(t, ctx)
	rules := e.routeYAML(t, cr)
	ms := e.mirrorStatus(t, ctx)
	offsetSync := e.offsetSyncForReport(ctx)
	status := func(tp string) string {
		if s := ms[tp]; s != "" {
			return s
		}
		return "<no-mirror>"
	}
	var mb strings.Builder
	for _, tp := range topics {
		fmt.Fprintf(&mb, "      %s: %s\n", tp, status(tp))
	}
	t.Logf("\n"+
		"╔═══════════════════════════════════════════════════════════════════════════════╗\n"+
		"║ WORLD STATE ▸ %s\n"+
		"╚═══════════════════════════════════════════════════════════════════════════════╝\n"+
		"  gateway route %q rules (live CR):\n%s\n  mirror status:\n%s"+
		"  cluster link %s: %s\n",
		label, e.route, indentLines(rules, "      "), mb.String(), offsetSyncEnableKey, offsetSync)

	if report == "" {
		return
	}
	var md strings.Builder
	fmt.Fprintf(&md, "# %s\n\n", label)
	fmt.Fprintf(&md, "`%s` · captured %s\n\n", t.Name(), time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&md, "## Gateway route `%s`\n\n```yaml\n%s\n```\n\n", e.route, strings.TrimRight(rules, "\n"))
	fmt.Fprintf(&md, "<details>\n<summary>Full Gateway CR <code>%s/%s</code></summary>\n\n```yaml\n%s\n```\n\n</details>\n\n",
		e.namespace, e.gateway, strings.TrimRight(string(cr), "\n"))
	md.WriteString("## Mirror topics\n\n| Topic | Status |\n|---|---|\n")
	for _, tp := range topics {
		fmt.Fprintf(&md, "| `%s` | %s |\n", tp, status(tp))
	}
	fmt.Fprintf(&md, "\n## Cluster link `%s`\n\n| Config | Value |\n|---|---|\n| `%s` | `%s` |\n",
		e.linkName, offsetSyncEnableKey, offsetSync)
	e.saveReport(t, report+".md", md.String())
}

// routeObj returns the suite's route from a gateway CR (nil if absent).
func (e *env) routeObj(t *testing.T, cr []byte) map[string]any {
	t.Helper()
	return findRoute(cr, e.route)
}

// routeYAML renders the migration-relevant route state for the snapshot: a
// static route's `fence` + `streamingDomain`, or a dynamic route's `rules`.
func (e *env) routeYAML(t *testing.T, cr []byte) string {
	t.Helper()
	r := e.routeObj(t, cr)
	if r == nil {
		return "(route " + e.route + " not found in CR)"
	}
	view := map[string]any{}
	for _, k := range []string{"fence", "streamingDomain", "rules"} {
		if v, ok := r[k]; ok {
			view[k] = v
		}
	}
	out, err := yaml.Marshal(view)
	if err != nil {
		return "(unmarshalable route)"
	}
	return string(out)
}

// isFenced reports whether the suite's route fences the given topic.
func (e *env) isFenced(t *testing.T, cr []byte, topic string) bool {
	t.Helper()
	return routeFences(e.routeObj(t, cr), topic)
}

// isSwitchedToTarget reports whether the suite's route routes the topic to the
// target domain.
func (e *env) isSwitchedToTarget(t *testing.T, cr []byte, topic string) bool {
	t.Helper()
	return routeTargets(e.routeObj(t, cr), e.destDomain, topic)
}

func indentLines(s, prefix string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// readCR fetches the live Gateway CR and strips server-managed metadata.
func (e *env) readCR(t *testing.T, ctx context.Context) []byte {
	t.Helper()
	raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	return stripServerFields(t, raw)
}
