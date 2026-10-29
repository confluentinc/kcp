//go:build e2e

// Package retries_e2e proves `kcp migration execute` rides out a cluster disappearing
// from its side: a source Kafka, a destination Kafka or the destination's REST endpoint
// that resets or stops answering mid-run. A blip shorter than the retry budget must be
// recovered with the run completing and the world correct; an outage longer than the
// budget must fail safe, leaving a consistent world that a re-run completes.
//
// The tests run INSIDE the cluster (see testdata/manifests/kcp-runner.yaml): kcp reads
// the live gateway CR with in-cluster config, and the runner pod carries NET_ADMIN and
// iptables so a test can cut kcp's connections to one cluster without touching the
// cluster itself. run.sh compiles the package for the node's architecture, ships it plus
// the kcp binary and the rendered manifests into the runner pod, and runs it there.
package retries_e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

// kcpBinary is the in-pod path run.sh copies the linux kcp binary to.
const kcpBinary = "/workspace/kcp"

// executeTimeout bounds a single execute invocation: a full cutover, or a run that
// waits out a 2-minute retry budget or more before giving up.
const executeTimeout = 20 * time.Minute

// env is the live topology, read from the KCP_RETRIES_* environment (setup.sh's
// generated .env plus the two secret-backed pod-spec variables).
type env struct {
	namespace       string
	gateway         string
	route           string
	restEndpoint    string
	destClusterID   string
	destBootstrap   string
	destDomain      string
	sourceBootstrap string
	linkName        string
	topicPrefix     string
	renderedDir     string
	saslUser        string
	saslPassword    string
	reportsDir      string // in-pod folder for this run's per-test reports; "" = off

	svc     *gateway.K8sService
	linkSvc clusterlink.Service
}

func newEnv() *env {
	return &env{
		namespace:       envOrDefault("KCP_RETRIES_NAMESPACE", "confluent"),
		gateway:         envOrDefault("KCP_RETRIES_GATEWAY_NAME", "retries-gateway"),
		route:           envOrDefault("KCP_RETRIES_ROUTE_NAME", "retries-route"),
		restEndpoint:    os.Getenv("KCP_RETRIES_REST_ENDPOINT"),
		destClusterID:   os.Getenv("KCP_RETRIES_DEST_CLUSTER_ID"),
		destBootstrap:   os.Getenv("KCP_RETRIES_DEST_BOOTSTRAP"),
		destDomain:      envOrDefault("KCP_RETRIES_DEST_DOMAIN", "destination-domain"),
		sourceBootstrap: os.Getenv("KCP_RETRIES_SOURCE_BOOTSTRAP"),
		linkName:        envOrDefault("KCP_RETRIES_CLUSTER_LINK_NAME", "retries-link"),
		topicPrefix:     envOrDefault("KCP_RETRIES_TOPIC_PREFIX", "retries-topic-"),
		renderedDir:     envOrDefault("KCP_RETRIES_RENDERED_DIR", "/workspace/rendered"),
		saslUser:        os.Getenv("KCP_RETRIES_DEST_SASL_USER"),
		saslPassword:    os.Getenv("KCP_RETRIES_DEST_SASL_PASSWORD"),
		reportsDir:      os.Getenv("KCP_RETRIES_REPORTS_DIR"),

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

// topicName is the zero-padded source topic name for an index (1 ⇒ retries-topic-001),
// matching setup.sh.
func (e *env) topicName(i int) string {
	return fmt.Sprintf("%s%03d", e.topicPrefix, i)
}

// topicRange returns the sorted topic names for the inclusive index range [lo,hi]. Tests
// reserve disjoint ranges so their promotions (irreversible) never collide.
func (e *env) topicRange(lo, hi int) []string {
	out := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, e.topicName(i))
	}
	sort.Strings(out)
	return out
}

// manifestOpts varies the GatewayMigration manifest a test runs.
type manifestOpts struct {
	policies  string // the policies block, at the section's child indent (4 spaces), or ""
	destCreds string // the destination Kafka credentials file name; "" = the valid one
}

// writeManifest renders a dynamicMigration GatewayMigration manifest for exactly the
// given topics and writes it into the in-pod rendered dir. Returns the in-pod path.
func (e *env) writeManifest(t *testing.T, name string, topics []string, o manifestOpts) string {
	t.Helper()
	var tb strings.Builder
	for _, tp := range topics {
		fmt.Fprintf(&tb, "          - %q\n", tp)
	}
	destCreds := "dest-kafka-creds.yaml"
	if o.destCreds != "" {
		destCreds = o.destCreds
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
      clusterCredentials: /workspace/rendered/%s
  clusterLink:
    name: %q
    bootstrapServers:
      - %q
    linkCredentials: /workspace/rendered/link-creds.yaml
  gateway:
    namespace: %q
    cr-name: %q
  dynamicMigration:
    route:
      name: %q
      targetStreamingDomain: %q
    topicGroup:
      - topics:
%s%s`, name, e.sourceBootstrap, e.destClusterID, e.destBootstrap, e.restEndpoint, destCreds,
		e.linkName, e.destBootstrap, e.namespace, e.gateway, e.route, e.destDomain, tb.String(), o.policies)

	path := filepath.Join(e.renderedDir, name+".yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600), "write generated manifest")
	e.saveReport(t, "manifest.yaml", manifest)
	return path
}

// linkConfig is the destination cluster-link REST config; the SASL user/password (from
// the retries-rest-credentials Secret via pod-spec env) become HTTP basic auth.
func (e *env) linkConfig() clusterlink.Config {
	return clusterlink.Config{
		RestEndpoint: e.restEndpoint,
		ClusterID:    e.destClusterID,
		LinkName:     e.linkName,
		APIKey:       e.saslUser,
		APISecret:    e.saslPassword,
	}
}

// mirrorStatus reads the live per-topic mirror status from the cluster link (ACTIVE /
// PENDING_STOPPED / STOPPED). It goes over the destination REST endpoint, so a test must
// not call it while it has that endpoint cut.
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

// readCR fetches the live Gateway CR and strips server-managed metadata.
func (e *env) readCR(t *testing.T, ctx context.Context) []byte {
	t.Helper()
	raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
	require.NoError(t, err)
	return stripServerFields(t, raw)
}

func (e *env) routeObj(cr []byte) map[string]any { return findRoute(cr, e.route) }

func (e *env) isFenced(cr []byte, topic string) bool {
	return routeFences(e.routeObj(cr), topic)
}

func (e *env) isSwitchedToTarget(cr []byte, topic string) bool {
	return routeTargets(e.routeObj(cr), e.destDomain, topic)
}

// routeYAML renders the migration-relevant route state (its rules) for the report.
func (e *env) routeYAML(cr []byte) string {
	r := e.routeObj(cr)
	if r == nil {
		return "(route " + e.route + " not found in CR)"
	}
	out, err := yaml.Marshal(map[string]any{"rules": r["rules"]})
	if err != nil {
		return "(unmarshalable route)"
	}
	return string(out)
}

// testReportDir is this test's folder in the run's reports dir, created on first use; ""
// when reports are off (KCP_RETRIES_REPORTS_DIR unset).
func (e *env) testReportDir(t *testing.T) string {
	t.Helper()
	if e.reportsDir == "" {
		return ""
	}
	dir := filepath.Join(e.reportsDir, t.Name())
	require.NoError(t, os.MkdirAll(dir, 0o755), "create the test's report folder")
	return dir
}

// saveReport writes one file into the test's report folder; a no-op when reports are off.
func (e *env) saveReport(t *testing.T, name, content string) {
	t.Helper()
	dir := e.testReportDir(t)
	if dir == "" {
		return
	}
	require.NoErrorf(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644), "write report %s", name)
}

// snapshot logs the live world for the given topics (the route's rules from the live CR
// and each topic's mirror status) and saves it as <report>.md in the test's folder. It
// reads the destination REST endpoint, so call it only while that endpoint is reachable.
func (e *env) snapshot(t *testing.T, ctx context.Context, report, label string, topics []string) {
	t.Helper()
	cr := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	var mb strings.Builder
	for _, tp := range topics {
		s := ms[tp]
		if s == "" {
			s = "<no-mirror>"
		}
		fmt.Fprintf(&mb, "| `%s` | %s | fenced=%v | to-destination=%v |\n", tp, s, e.isFenced(cr, tp), e.isSwitchedToTarget(cr, tp))
	}
	body := fmt.Sprintf("# %s\n\n`%s` · captured %s\n\n## Route `%s` rules\n\n```yaml\n%s\n```\n\n## Topics\n\n| Topic | Mirror | Fence | Route |\n|---|---|---|---|\n%s",
		label, t.Name(), time.Now().UTC().Format(time.RFC3339), e.route, strings.TrimRight(e.routeYAML(cr), "\n"), mb.String())
	t.Logf("\n╔═ WORLD STATE ▸ %s\n%s", label, body)
	e.saveReport(t, report+".md", body)
}

// requirePristine asserts the topics are all still ACTIVE mirrors, unfenced and not
// routed to the destination: the world a migration starts from.
func (e *env) requirePristine(t *testing.T, ctx context.Context, topics []string) {
	t.Helper()
	ms := e.mirrorStatus(t, ctx)
	cr := e.readCR(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "ACTIVE", ms[tp], "%s must start as an ACTIVE mirror (promotion is irreversible: a slice serves one test)", tp)
		require.Falsef(t, e.isFenced(cr, tp), "%s must start unfenced", tp)
		require.Falsef(t, e.isSwitchedToTarget(cr, tp), "%s must start routed to the source", tp)
	}
}

// requireMigrated asserts the topics ended fully migrated: every mirror STOPPED, no fence
// left on the route, and routed to the destination. It waits for the mirrors, which the
// cluster link reports with a short lag.
func (e *env) requireMigrated(t *testing.T, ctx context.Context, topics []string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	var ms map[string]string
	for {
		ms = e.mirrorStatus(t, ctx)
		all := true
		for _, tp := range topics {
			if ms[tp] != "STOPPED" {
				all = false
			}
		}
		if all || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	cr := e.readCR(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "STOPPED", ms[tp], "%s must be a STOPPED mirror", tp)
		require.Falsef(t, e.isFenced(cr, tp), "%s must not be fenced after the migration", tp)
		require.Truef(t, e.isSwitchedToTarget(cr, tp), "%s must be routed to the destination", tp)
	}
}
