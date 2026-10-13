//go:build e2e

package routeconversion

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration/killpoint"
	"github.com/stretchr/testify/require"
)

// kcpBinary is the in-pod path run.sh copies the linux kcp binary to.
const kcpBinary = "/workspace/kcp"

// executeTimeout bounds one kcp run (fence, a detection window, sync, switch,
// each with a hot reload on every gateway pod).
const executeTimeout = 10 * time.Minute

// minDetect is the shortest detectUnroutedCommitsDuration a manifest accepts;
// every test uses it unless it needs a longer window.
const minDetect = 10 * time.Second

// In-pod paths rc-runner.yaml mounts.
const (
	gatewayTruststore         = "/etc/rc-gw-trust/truststore.jks"
	gatewayTruststorePassword = "changeit"
	destTruststore            = "/etc/rc-dest-tls/truststore.jks"
	destTruststorePassword    = "/etc/rc-dest-tls/jksPassword.txt"
)

// env is the live topology, read from the KCP_RC_* environment run.sh passes
// (setup.sh's .env) plus the two secret-backed pod-spec vars.
type env struct {
	namespace        string
	gateway          string
	route            string
	domains          Domains
	sourceBootstrap  string
	destBootstrap    string
	gatewayBootstrap string
	restEndpoint     string
	destClusterID    string
	linkName         string
	topicPrefix      string
	orphanTopic      string
	renderedDir      string
	reportsDir       string
	saslUser         string
	saslPassword     string

	svc     *gateway.K8sService
	linkSvc *clusterlink.ConfluentCloudService
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		namespace: envOr("KCP_RC_NAMESPACE", "confluent"),
		gateway:   envOr("KCP_RC_GATEWAY_NAME", "rc-gateway"),
		route:     envOr("KCP_RC_ROUTE_NAME", "rc-route"),
		domains: Domains{
			Source: envOr("KCP_RC_SOURCE_DOMAIN", "source-domain"), SourceID: envOr("KCP_RC_SOURCE_BOOTSTRAP_ID", "SOURCE"),
			Dest: envOr("KCP_RC_DEST_DOMAIN", "destination-domain"), DestID: envOr("KCP_RC_DEST_BOOTSTRAP_ID", "DESTINATION"),
		},
		sourceBootstrap:  os.Getenv("KCP_RC_SOURCE_BOOTSTRAP"),
		destBootstrap:    os.Getenv("KCP_RC_DEST_BOOTSTRAP"),
		gatewayBootstrap: envOr("KCP_RC_GATEWAY_BOOTSTRAP", "bootstrap.gw.local:9595"),
		restEndpoint:     os.Getenv("KCP_RC_REST_ENDPOINT"),
		destClusterID:    os.Getenv("KCP_RC_DEST_CLUSTER_ID"),
		linkName:         envOr("KCP_RC_CLUSTER_LINK_NAME", "rc-link"),
		topicPrefix:      envOr("KCP_RC_TOPIC_PREFIX", "rc-topic-"),
		orphanTopic:      envOr("KCP_RC_ORPHAN_TOPIC", "rc-orphan"),
		renderedDir:      envOr("KCP_RC_RENDERED_DIR", "/workspace/rendered"),
		reportsDir:       os.Getenv("KCP_RC_REPORTS_DIR"),
		saslUser:         os.Getenv("KCP_RC_DEST_SASL_USER"),
		saslPassword:     os.Getenv("KCP_RC_DEST_SASL_PASSWORD"),

		svc:     gateway.NewK8sService(""),
		linkSvc: clusterlink.NewConfluentCloudService(http.DefaultClient),
	}
	for name, v := range map[string]string{
		"KCP_RC_SOURCE_BOOTSTRAP": e.sourceBootstrap, "KCP_RC_DEST_BOOTSTRAP": e.destBootstrap,
		"KCP_RC_REST_ENDPOINT": e.restEndpoint, "KCP_RC_DEST_CLUSTER_ID": e.destClusterID,
		"KCP_RC_DEST_SASL_USER": e.saslUser, "KCP_RC_DEST_SASL_PASSWORD": e.saslPassword,
	} {
		require.NotEmptyf(t, v, "%s must be set (run the suite with run.sh)", name)
	}
	return e
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// topic is the zero-padded link topic name for an index (1 -> rc-topic-001),
// matching setup.sh.
func (e *env) topic(i int) string { return fmt.Sprintf("%s%03d", e.topicPrefix, i) }

// writeManifest renders a conversion manifest for this environment into the
// in-pod rendered dir and saves a copy in the test's report folder. Its
// metadata.name, which kcp prints as the migration id, is "rc-<name>".
func (e *env) writeManifest(t *testing.T, name string, detect time.Duration) (path, id string) {
	t.Helper()
	id = "rc-" + name
	manifest := fmt.Sprintf(`apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: %s
spec:
  source:
    type: apache-kafka
    bootstrapServers: [%q]
    credentials: %s
  target:
    type: confluent-platform
    clusterId: %q
    kafka:
      bootstrapServers: [%q]
      restEndpoint: %q
      clusterCredentials: %s
  clusterLink:
    name: %q
    bootstrapServers: [%q]
    linkCredentials: %s
  gateway:
    namespace: %q
    cr-name: %q
  route:
    name: %q
    convertTo: static
    targetStreamingDomain: %q
  defaultPolicies:
    detectUnroutedCommitsDuration: %s
`, id, e.sourceBootstrap, filepath.Join(e.renderedDir, "source-creds.yaml"),
		e.destClusterID, e.destBootstrap, e.restEndpoint, filepath.Join(e.renderedDir, "dest-kafka-creds.yaml"),
		e.linkName, e.destBootstrap, filepath.Join(e.renderedDir, "link-creds.yaml"),
		e.namespace, e.gateway, e.route, e.domains.Dest, detect)
	path = filepath.Join(e.renderedDir, id+".yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600), "write the conversion manifest")
	e.saveReport(t, "manifest.yaml", manifest)
	return path, id
}

// seam is one test-only switch (killpoint) set on a single kcp run.
type seam struct {
	env    string // NAME=value
	effect string
}

// cancelAfter stops a run right after the named FSM state, as an abrupt exit would.
func cancelAfter(state string) *seam {
	return &seam{env: killpoint.EnvVar + "=" + state, effect: "simulated abrupt exit"}
}

// failAt makes the named workflow step fail the way a real failure of it would.
func failAt(step string) *seam {
	return &seam{env: killpoint.FailEnvVar + "=" + step, effect: "simulated step failure"}
}

// execute runs `kcp migration execute --migration-yaml manifest` with s (nil
// for a plain run); see runKCP.
func (e *env) execute(t *testing.T, report, manifest string, s *seam, onLine func(string)) (string, error) {
	t.Helper()
	return e.runKCP(t, report, s, onLine, "migration", "execute", "--migration-yaml", manifest)
}

// dryRun runs `kcp migration execute --migration-yaml manifest --dry-run`.
func (e *env) dryRun(t *testing.T, report, manifest string) (string, error) {
	t.Helper()
	return e.runKCP(t, report, nil, nil, "migration", "execute", "--migration-yaml", manifest, "--dry-run")
}

// runKCP execs the in-pod kcp binary. Its combined output is streamed line by
// line to onLine (nil for none) while it runs, so a test can act at a precise
// point of the run, and is returned, logged in full and saved as report in the
// test's folder, with the exit result.
func (e *env) runKCP(t *testing.T, report string, s *seam, onLine func(string), args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), executeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, kcpBinary, args...)
	cmd.Env = os.Environ()
	desc := "kcp " + strings.Join(args, " ")
	if s != nil {
		cmd.Env = append(cmd.Env, s.env)
		desc += "    [" + s.env + " → " + s.effect + "]"
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	var buf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			buf.WriteString(line + "\n")
			if onLine != nil {
				onLine(line)
			}
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	err := cmd.Start()
	if err == nil {
		err = cmd.Wait()
	}
	_ = pw.Close()
	wg.Wait()
	out := buf.String()

	t.Logf("\n"+
		"┌─────────────────────────────────────────────────────────────────────────────┐\n"+
		"│ RAW KCP RUN ▸ %s\n"+
		"└─────────────────────────────────────────────────────────────────────────────┘\n"+
		"%s\n"+
		"───── process exit: err=%v ─────\n", desc, out, err)
	e.saveReport(t, report, fmt.Sprintf("$ %s\n\n%s\n───── process exit: err=%v ─────\n", desc, out, err))
	return out, err
}

// testReportDir is this test's folder in the run's reports dir, created on first
// use; "" when reports are off (KCP_RC_REPORTS_DIR unset).
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

// workDir is a scratch folder for this test's client configs and logs: the
// test's report folder when reports are on (so the logs are copied out), else
// a temp dir.
func (e *env) workDir(t *testing.T) string {
	t.Helper()
	if dir := e.testReportDir(t); dir != "" {
		return dir
	}
	return t.TempDir()
}
