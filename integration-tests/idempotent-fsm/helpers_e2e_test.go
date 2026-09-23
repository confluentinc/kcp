//go:build e2e

// Package idempotent_fsm_e2e proves the migration execution FSMs are idempotent
// from any interruption point, against a live dynamic-mode Confluent Gateway with
// hot reload (Minikube profile kcp-e2e-idempotent). Each test drives the real
// `kcp migration execute` command, interrupts it at a chosen checkpoint via the
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
)

// kcpBinary is the in-pod path run.sh copies the linux kcp binary to.
const kcpBinary = "/workspace/kcp"

// Kill-point checkpoints — FSM state names the killpoint seam (KCP_TEST_CANCEL_AFTER)
// matches against to cancel a run right after that step. Values mirror the FSM
// state constants in internal/services/migration{,/tbm} (StateFenced etc.); a
// mismatch would surface as the interrupt never firing (the run completes),
// which the resume tests catch by requiring a non-zero exit.
const (
	cpFenced   = "fenced"
	cpPromoted = "promoted"
	cpSwitched = "switched"
)

// executeTimeout bounds a single execute invocation (a full cutover incl.
// gateway rollouts and mirror-STOPPED waits).
const executeTimeout = 10 * time.Minute

// env holds the live topology, read from the KCP_TBM_* environment (setup.sh's
// generated .env plus the two secret-backed pod-spec vars). svc is kcp's own
// gateway service built in-cluster (empty kubeconfig ⇒ in-cluster service account).
type env struct {
	namespace       string
	gateway         string
	route           string
	restEndpoint    string
	destClusterID   string
	linkName        string
	topicPrefix     string
	successHi       int
	reservedTopic   int
	renderedDir     string
	saslUser        string
	saslPassword    string
	sourceBootstrap string

	svc     *gateway.K8sService
	linkSvc clusterlink.Service
}

func newEnv() *env {
	return &env{
		namespace:       envOrDefault("KCP_TBM_NAMESPACE", "confluent"),
		gateway:         envOrDefault("KCP_TBM_GATEWAY_NAME", "tbm-gateway"),
		route:           envOrDefault("KCP_TBM_ROUTE_NAME", "tbm-route"),
		restEndpoint:    os.Getenv("KCP_TBM_REST_ENDPOINT"),
		destClusterID:   os.Getenv("KCP_TBM_DEST_CLUSTER_ID"),
		linkName:        envOrDefault("KCP_TBM_CLUSTER_LINK_NAME", "tbm-link"),
		topicPrefix:     envOrDefault("KCP_TBM_TOPIC_PREFIX", "tbm-topic-"),
		successHi:       envInt("KCP_TBM_SUCCESS_HI", 44),
		reservedTopic:   envInt("KCP_TBM_RESERVED_TOPIC", 45),
		renderedDir:     envOrDefault("KCP_TBM_RENDERED_DIR", "/workspace/rendered"),
		saslUser:        os.Getenv("KCP_TBM_DEST_SASL_USER"),
		saslPassword:    os.Getenv("KCP_TBM_DEST_SASL_PASSWORD"),
		sourceBootstrap: os.Getenv("KCP_TBM_SOURCE_BOOTSTRAP"),

		svc:     gateway.NewK8sService(""),
		linkSvc: clusterlink.NewConfluentCloudService(http.DefaultClient),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n := fallback
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
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

// runKCP execs the in-pod kcp binary as a local subprocess (this test already
// runs inside the cluster). When cancelAfter is non-empty it sets the killpoint
// env var so the run cancels itself right after that checkpoint (simulating an
// abrupt Ctrl-C); empty means a normal, uninterrupted run. Returns combined
// stdout+stderr and the process error.
func (e *env) runKCP(t *testing.T, cancelAfter string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), executeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, kcpBinary, args...)
	cmd.Env = os.Environ()
	desc := "kcp " + strings.Join(args, " ")
	if cancelAfter != "" {
		cmd.Env = append(cmd.Env, killpoint.EnvVar+"="+cancelAfter)
		desc += "    [" + killpoint.EnvVar + "=" + cancelAfter + " → simulated abrupt exit]"
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
	return string(out), err
}

// snapshot logs a tangible before/after picture of the live world for the given
// topics: the gateway route's fence + routing rules (verbatim from the live CR)
// and each topic's live mirror status. Called before and after every run so a
// human reviewer can see exactly what changed.
func (e *env) snapshot(t *testing.T, ctx context.Context, label string, topics []string) {
	t.Helper()
	rules := e.routeRulesYAML(t, e.readCR(t, ctx))
	ms := e.mirrorStatus(t, ctx)
	var mb strings.Builder
	for _, tp := range topics {
		s := ms[tp]
		if s == "" {
			s = "<no-mirror>"
		}
		fmt.Fprintf(&mb, "      %s: %s\n", tp, s)
	}
	t.Logf("\n"+
		"╔═══════════════════════════════════════════════════════════════════════════════╗\n"+
		"║ WORLD STATE ▸ %s\n"+
		"╚═══════════════════════════════════════════════════════════════════════════════╝\n"+
		"  gateway route %q rules (live CR):\n%s\n  mirror status:\n%s",
		label, e.route, indentLines(rules, "      "), mb.String())
}

// routeRulesYAML extracts the named route's rules subtree (fencing + routing)
// from a gateway CR and marshals it back to YAML for display. Guarded against an
// unexpected shape rather than panicking.
func (e *env) routeRulesYAML(t *testing.T, cr []byte) string {
	t.Helper()
	var obj map[string]any
	if err := yaml.Unmarshal(cr, &obj); err != nil {
		return "(unparseable CR)"
	}
	spec, _ := obj["spec"].(map[string]any)
	routes, _ := spec["routes"].([]any)
	for _, r := range routes {
		rm, _ := r.(map[string]any)
		if rm["name"] == e.route {
			out, err := yaml.Marshal(rm["rules"])
			if err != nil {
				return "(unmarshalable rules)"
			}
			return string(out)
		}
	}
	return "(route " + e.route + " not found in CR)"
}

// fencedTopics returns the set of topics named in the route's fencing[] blocked
// entries from a live gateway CR. Used to assert a completed migration leaves no
// fence on the switched topics (regression guard for the stale-fence-on-resume
// bug found live 2026-09-23).
func (e *env) fencedTopics(t *testing.T, cr []byte) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	var obj map[string]any
	if err := yaml.Unmarshal(cr, &obj); err != nil {
		return out
	}
	spec, _ := obj["spec"].(map[string]any)
	routes, _ := spec["routes"].([]any)
	for _, r := range routes {
		rm, _ := r.(map[string]any)
		if rm["name"] != e.route {
			continue
		}
		rules, _ := rm["rules"].(map[string]any)
		fencing, _ := rules["fencing"].([]any)
		for _, fe := range fencing {
			fm, _ := fe.(map[string]any)
			if blocked, _ := fm["blocked"].(bool); !blocked {
				continue
			}
			topics, _ := fm["topics"].([]any)
			for _, tp := range topics {
				if s, ok := tp.(string); ok {
					out[s] = true
				}
			}
		}
	}
	return out
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

// stripServerFields removes the server-managed metadata a fetched CR carries.
// Mirrors the migration workflow's cleanInitialCR; duplicated rather than
// exported because widening kcp's public surface for a test is the wrong trade.
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
