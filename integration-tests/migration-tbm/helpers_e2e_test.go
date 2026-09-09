//go:build e2e

// Package migration_tbm_e2e runs a Topic-Based Migration against a live
// dynamic-mode Confluent Gateway with hot reload (Minikube profile kcp-e2e-tbm).
// TestSuccessBatchesMigrate and TestExecuteTBMThinPosture drive the real
// execute-tbm command (internal/services/migration/tbm's FSM): initialize,
// wait_for_lags and fence are real; verify_fence, promote and switch remain
// noop, so a batch's mirror is never actually promoted nor its route actually
// switched yet — see each test's own doc comment for what it can and cannot
// prove today. TestHaltScenarios and TestHarnessAppliesSwitchoverWithoutRoll
// instead exercise migplan.Reconcile and the gateway hot-reload apply path
// directly, independent of the FSM.
//
// Like the hot-reload suite, this binary runs INSIDE the cluster (see
// manifests/kcp-runner.yaml): the gateway service dials each pod's /config port
// directly, and pod IPs are not routable from the host under the docker driver.
// run.sh compiles the package for the node arch, ships it to the runner pod, and
// executes it there with the non-secret KCP_TBM_* env; the destination SASL
// credential arrives via pod-spec env from the tbm-rest-credentials Secret.
package migration_tbm_e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// convergeTimeout is a generous ceiling on per-pod configId convergence
	// (measured ~4s on a 2-replica gateway); pollInterval paces every wait loop.
	convergeTimeout = 90 * time.Second
	pollInterval    = 2 * time.Second
	// promoteTimeout bounds the wait for a promoted mirror to reach STOPPED.
	promoteTimeout = 120 * time.Second
	// numBatches is the count of success batch manifests (batch-01..04); the
	// success topic range 001..SUCCESS_HI is partitioned across them.
	numBatches = 4
)

// env holds the live topology the suite operates against, read from the KCP_TBM_*
// environment (see setup.sh's generated .env, plus the two secret-backed pod-spec
// vars). svc is kcp's own gateway service constructed in-cluster (empty kubeconfig
// ⇒ in-cluster service account), the same way the migration workflow builds it.
type env struct {
	namespace     string
	gateway       string
	route         string
	restEndpoint  string
	destClusterID string
	linkName      string
	topicPrefix   string
	successHi     int
	reservedTopic int
	renderedDir   string
	saslUser      string
	saslPassword  string

	svc       *gateway.K8sService
	clientset kubernetes.Interface
	linkSvc   clusterlink.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()

	cfg, err := rest.InClusterConfig()
	require.NoError(t, err, "this suite must run inside the cluster; see run.sh")
	cs, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	return &env{
		namespace:     envOrDefault("KCP_TBM_NAMESPACE", "confluent"),
		gateway:       envOrDefault("KCP_TBM_GATEWAY_NAME", "tbm-gateway"),
		route:         envOrDefault("KCP_TBM_ROUTE_NAME", "tbm-route"),
		restEndpoint:  os.Getenv("KCP_TBM_REST_ENDPOINT"),
		destClusterID: os.Getenv("KCP_TBM_DEST_CLUSTER_ID"),
		linkName:      envOrDefault("KCP_TBM_CLUSTER_LINK_NAME", "tbm-link"),
		topicPrefix:   envOrDefault("KCP_TBM_TOPIC_PREFIX", "tbm-topic-"),
		successHi:     envInt(t, "KCP_TBM_SUCCESS_HI", 44),
		reservedTopic: envInt(t, "KCP_TBM_RESERVED_TOPIC", 45),
		renderedDir:   envOrDefault("KCP_TBM_RENDERED_DIR", "/workspace/rendered"),
		saslUser:      os.Getenv("KCP_TBM_DEST_SASL_USER"),
		saslPassword:  os.Getenv("KCP_TBM_DEST_SASL_PASSWORD"),

		svc:       gateway.NewK8sService(""),
		clientset: cs,
		linkSvc:   clusterlink.NewConfluentCloudService(http.DefaultClient),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n := fallback
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		t.Fatalf("bad %s: %v", key, err)
	}
	return n
}

// topicName is the zero-padded source topic name for an index (1 ⇒ tbm-topic-001),
// matching setup.sh's topic_name so the Go and shell sides agree on names.
func (e *env) topicName(i int) string {
	return fmt.Sprintf("%s%03d", e.topicPrefix, i)
}

// manifestPath resolves a rendered manifest filename to its in-pod path under
// KCP_TBM_RENDERED_DIR (run.sh kubectl cp's the host .rendered/* there).
func (e *env) manifestPath(name string) string {
	return filepath.Join(e.renderedDir, name)
}

// linkConfig is the destination cluster-link REST config. The SASL user/password
// (from the tbm-rest-credentials Secret via pod-spec env) become HTTP basic auth;
// the REST proxy is plaintext http, so http.DefaultClient suffices.
func (e *env) linkConfig() clusterlink.Config {
	return clusterlink.Config{
		RestEndpoint: e.restEndpoint,
		ClusterID:    e.destClusterID,
		LinkName:     e.linkName,
		APIKey:       e.saslUser,
		APISecret:    e.saslPassword,
	}
}

// podFingerprint captures what a pod roll would change: each pod's identity and
// container restart count, plus the Deployment generation.
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

// assertNoPodRoll gives the suite its teeth: every route edit here is a
// hot-reloadable change, so identical pod UIDs, unchanged restart counts and an
// unmoved Deployment generation are all required. The generation is checked
// because it is the signal kcp routes on — had it moved, kcp would have taken the
// rollout path and the configId verification would never have been exercised.
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

// readCR fetches the live Gateway CR and strips the server-managed metadata that
// server-side apply rejects, giving a spec that can be re-applied as-is.
func (e *env) readCR(t *testing.T, ctx context.Context) []byte {
	t.Helper()

	raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	return stripServerFields(t, raw)
}

// stripServerFields removes the server-managed metadata a fetched CR carries,
// which server-side apply rejects. Mirrors the migration workflow's cleanInitialCR
// (internal/services/migration/workflow.go); duplicated rather than exported
// because widening kcp's public surface for a test is the wrong trade.
func stripServerFields(t *testing.T, crYAML []byte) []byte {
	t.Helper()

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crYAML, &obj))

	delete(obj, "status")
	if md, ok := obj["metadata"].(map[string]any); ok {
		for _, k := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp", "generation", "selfLink"} {
			delete(md, k)
		}
	}

	out, err := yaml.Marshal(obj)
	require.NoError(t, err)
	return out
}

// hasFailedPrecondition reports whether the report carries a failed precondition
// whose name contains nameSubstr. Copied from
// integration-tests/migplan/gateway_permutations_e2e_test.go.
func hasFailedPrecondition(r reconcile.Report, nameSubstr string) bool {
	for _, p := range r.Preconditions {
		if !p.OK && strings.Contains(p.Name, nameSubstr) {
			return true
		}
	}
	return false
}

// --- tbmHarness: the decide → apply → promote → advance seam ---------------

// FSM seam: the real TBM orchestrator (internal/services/migration/tbm) will own
// Decide, ApplyFence, ApplySwitchover, PromoteMirrors and ReRun once it consumes
// migplan.Result. Until then this harness stands in, wiring the live engine to the
// gateway hot-reload apply and the cluster-link REST promotion so the batch loop
// (tbm_e2e_test.go) and halt assertions (tbm_halt_e2e_test.go) can drive a real
// migration through the same shape the FSM will keep.
type tbmHarness struct {
	e   *env
	ctx context.Context
}

func newHarness(t *testing.T) *tbmHarness {
	return &tbmHarness{e: newEnv(t), ctx: context.Background()}
}

// Decide runs the engine against the live gateway CR (spec.gateway ⇒ live pull;
// no WithGatewaySource). A returned error is an I/O failure; a refusal is data on
// the Result. The report is discarded — the tests read the Result fields.
func (h *tbmHarness) Decide(t *testing.T, g *manifest.GatewayMigration) *migplan.Result {
	t.Helper()
	res, err := migplan.Reconcile(h.ctx, g, migplan.WithOutput(io.Discard))
	require.NoError(t, err, "Decide: engine I/O failure (a refusal is not an error)")
	return res
}

// DecideHermetic runs the engine with the gateway CR supplied from a file fixture
// instead of the live pull, for route-shape checks that must not mutate the live
// migration route. Source/target/cluster-link reads are still live.
func (h *tbmHarness) DecideHermetic(t *testing.T, g *manifest.GatewayMigration, gatewayFile string) *migplan.Result {
	t.Helper()
	route := g.Spec.TopicGroup[0].Route
	res, err := migplan.Reconcile(h.ctx, g,
		migplan.WithGatewaySource(migplan.NewGatewayFile(gatewayFile, route)),
		migplan.WithOutput(io.Discard),
	)
	require.NoError(t, err, "DecideHermetic: engine I/O failure")
	return res
}

// ReRun is Decide by another name, kept as a distinct verb so the batch loop reads
// as decide → … → re-run and the FSM boundary stays legible.
func (h *tbmHarness) ReRun(t *testing.T, g *manifest.GatewayMigration) *migplan.Result {
	t.Helper()
	return h.Decide(t, g)
}

// ApplyFence splices the engine's fenced rules block into the live gateway CR's
// route and applies it by hot reload. Returns the configId minted for the apply.
func (h *tbmHarness) ApplyFence(t *testing.T, res *migplan.Result) string {
	t.Helper()
	return h.applyRules(t, "fence", res.FenceYAML)
}

// ApplySwitchover splices the engine's switchover rules block into the live
// gateway CR's route and applies it by hot reload. Returns the configId minted.
func (h *tbmHarness) ApplySwitchover(t *testing.T, res *migplan.Result) string {
	t.Helper()
	return h.applyRules(t, "switchover", res.SwitchoverYAML)
}

// applyRules is the shared apply mechanic: read the live CR, wholesale-replace the
// migration route's `rules` subtree with the engine's artifact (the artifact is
// cumulative — it already preserves prior edits — so a whole-subtree replace is
// correct), apply with a fresh configId, then confirm the configId on every pod
// and assert no pod rolled.
func (h *tbmHarness) applyRules(t *testing.T, label, rulesYAML string) string {
	t.Helper()
	require.NotEmpty(t, rulesYAML, "%s: engine produced an empty rules artifact", label)

	before := h.e.fingerprint(t, h.ctx)
	spliced := spliceRouteRules(t, h.e.readCR(t, h.ctx), h.e.route, rulesYAML)

	configID, err := gateway.NewConfigID()
	require.NoError(t, err)

	stored, err := h.e.svc.ApplyGatewayYAML(h.ctx, h.e.namespace, h.e.gateway, spliced, configID)
	require.NoError(t, err, "%s: ApplyGatewayYAML", label)
	require.Equal(t, configID, stored, "the API server must persist the configId kcp sent")

	require.NoError(t, h.e.svc.WaitForGatewayAccepted(h.ctx, h.e.namespace, h.e.gateway, pollInterval, convergeTimeout))
	require.NoError(t, h.e.svc.WaitForGatewayConfigID(h.ctx, h.e.namespace, h.e.gateway, gateway.ConfigWaitOptions{
		ConfigID:         configID,
		Port:             gateway.DefaultGatewayConfigPort,
		PollInterval:     pollInterval,
		HotReloadTimeout: convergeTimeout,
	}), "%s: every gateway pod must report the new configId", label)

	assertNoPodRoll(t, before, h.e.fingerprint(t, h.ctx))
	return configID
}

// ensureFencingBlocked fills in the CRD-required `blocked` on any fencing entry
// the engine emitted without it. The engine renders a batch fence as
// {topics: [...]} — fence = block those topics during cutover — but the CFK
// 0.1838.0 Gateway CRD marks fencing[].blocked a required field, so a raw apply is
// rejected. The harness fills it (blocked: true matches the fence's intent) the way
// the future TBM FSM will have to; it is not an engine defect the tests should mask
// elsewhere.
func ensureFencingBlocked(rules map[string]any) {
	fencing, ok := rules["fencing"].([]any)
	if !ok {
		return
	}
	for _, e := range fencing {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if _, has := entry["blocked"]; !has {
			entry["blocked"] = true
		}
	}
}

// spliceRouteRules replaces the named route's `rules` subtree in a parsed gateway
// CR with the inner value of a `rules:`-wrapped engine artifact, then re-marshals.
// It inverts findRoute (internal/services/migplan/gatewayfile.go), which reads the
// same spec.routes[].rules path, so the write the engine will read back is
// consistent with the read that produced the artifact.
func spliceRouteRules(t *testing.T, baseCR []byte, routeName, rulesYAML string) []byte {
	t.Helper()

	var cr map[string]any
	require.NoError(t, yaml.Unmarshal(baseCR, &cr))

	var wrap map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(rulesYAML), &wrap))
	rules, ok := wrap["rules"].(map[string]any)
	require.True(t, ok, "rules artifact must be wrapped under a top-level rules: key")
	ensureFencingBlocked(rules)

	spec, ok := cr["spec"].(map[string]any)
	require.True(t, ok, "live gateway CR has no spec")
	routes, ok := spec["routes"].([]any)
	require.True(t, ok, "live gateway CR has no spec.routes")

	found := false
	for _, r := range routes {
		route, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := route["name"].(string); name == routeName {
			route["rules"] = rules
			found = true
		}
	}
	require.True(t, found, "route %q not found in the live gateway CR", routeName)

	out, err := yaml.Marshal(cr)
	require.NoError(t, err)
	return out
}

// PromoteMirrors stops+promotes the cluster-link mirrors for topics via the
// destination REST mirrors:promote verb, then waits until each reports STOPPED —
// the terminal state the engine reads as MirrorStopped. A no-op for an empty list.
func (h *tbmHarness) PromoteMirrors(t *testing.T, topics []string) {
	t.Helper()
	if len(topics) == 0 {
		return
	}

	cfg := h.e.linkConfig()
	cfg.Topics = topics
	want := map[string]bool{}
	for _, tp := range topics {
		want[tp] = true
	}

	// Promote only mirrors not already STOPPED, so the step is idempotent: a mirror
	// promotion is irreversible, and a resumed migration (or a re-run against a used
	// cluster) must treat an already-promoted mirror as done rather than error.
	current, err := h.e.linkSvc.ListMirrorTopics(h.ctx, cfg)
	require.NoError(t, err, "list mirror topics")
	var toPromote []string
	for _, tp := range topics {
		promoted := false
		for _, m := range current {
			if m.MirrorTopicName == tp && m.MirrorStatus == clusterlink.MirrorStatusStopped {
				promoted = true
				break
			}
		}
		if !promoted {
			toPromote = append(toPromote, tp)
		}
	}
	if len(toPromote) > 0 {
		resp, err := h.e.linkSvc.PromoteMirrorTopics(h.ctx, h.e.linkConfig(), toPromote)
		require.NoError(t, err, "promote mirror topics")
		for _, d := range resp.Data {
			require.Zerof(t, d.ErrorCode, "promote %s: %s", d.MirrorTopicName, d.ErrorMessage)
		}
	}

	deadline := time.Now().Add(promoteTimeout)
	for {
		mirrors, err := h.e.linkSvc.ListMirrorTopics(h.ctx, cfg)
		require.NoError(t, err, "list mirror topics")
		stopped := 0
		for _, m := range mirrors {
			if want[m.MirrorTopicName] && m.MirrorStatus == clusterlink.MirrorStatusStopped {
				stopped++
			}
		}
		if stopped == len(topics) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d mirror(s) to reach STOPPED (got %d)", len(topics), stopped)
		}
		time.Sleep(pollInterval)
	}
}

// setOffsetSync flips consumer.offset.sync.enable on the live cluster link. Used
// by the offset-sync halt, which toggles it on and restores it via t.Cleanup, so
// no suite ordering is assumed.
func (h *tbmHarness) setOffsetSync(t *testing.T, enabled bool) {
	t.Helper()
	value := "false"
	if enabled {
		value = "true"
	}
	err := h.e.linkSvc.AlterConfigs(h.ctx, h.e.linkConfig(), []clusterlink.ConfigAlteration{
		{Name: "consumer.offset.sync.enable", Value: value, Operation: clusterlink.OperationSet},
	})
	require.NoError(t, err, "set consumer.offset.sync.enable=%s", value)
}

// loadManifest reads and validates a rendered GatewayMigration manifest by name.
func (h *tbmHarness) loadManifest(t *testing.T, name string) *manifest.GatewayMigration {
	t.Helper()
	g, err := manifest.LoadGatewayMigrationFile(h.e.manifestPath(name))
	require.NoError(t, err, "load rendered manifest %s", name)
	return g
}

// manifestForTopics loads a rendered manifest and replaces its selector with an
// explicit topic list, for in-code batches (the mixed already-migrated edge and
// the harness's own thin test) that no committed fixture expresses.
func (h *tbmHarness) manifestForTopics(t *testing.T, baseName string, topics []string) *manifest.GatewayMigration {
	t.Helper()
	g := h.loadManifest(t, baseName)
	list := append([]string(nil), topics...)
	g.Spec.TopicGroup[0].Topics = &list
	g.Spec.TopicGroup[0].TopicPatterns = nil
	return g
}

// topicRange returns the sorted source topic names for the inclusive index range
// [lo, hi]. Zero-padding makes lexical order match numeric order.
func (e *env) topicRange(lo, hi int) []string {
	out := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, e.topicName(i))
	}
	sort.Strings(out)
	return out
}

// unchangedTopics returns the topic names the report classified Unchanged.
func unchangedTopics(r reconcile.Report) map[string]bool {
	out := map[string]bool{}
	for _, tv := range r.Unchanged {
		out[tv.Topic] = true
	}
	return out
}

// reasonsContain reports whether any refusal reason contains sub.
func reasonsContain(res *migplan.Result, sub string) bool {
	for _, r := range res.Reasons {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

// TestHarnessAppliesSwitchoverWithoutRoll is the seam's own thin test (U5): it
// drives one already-mirrored headroom topic through ApplyFence then
// ApplySwitchover and asserts each apply converges its configId on every pod with
// no pod roll, and that the two applies mint distinct configIds. It runs before
// the multi-batch tests depend on the seam. Topic 048 is used (headroom, outside
// the success range and the reserved topic) so this does not perturb U7's
// accounting; it is not promoted, so no cluster-link state changes here.
func TestHarnessAppliesSwitchoverWithoutRoll(t *testing.T) {
	h := newHarness(t)

	g := h.manifestForTopics(t, "batch-01.yaml", []string{h.e.topicName(48)})
	res := h.Decide(t, g)
	require.False(t, res.Refused, "an active-mirror headroom topic must be migratable: %v", res.Reasons)
	require.NotEmpty(t, res.FenceYAML)
	require.NotEmpty(t, res.SwitchoverYAML)

	fenceID := h.ApplyFence(t, res)
	switchID := h.ApplySwitchover(t, res)
	require.NotEqual(t, fenceID, switchID, "each transition must carry a distinct configId")
}
