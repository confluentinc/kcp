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
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

// The migration id kcp prints is "rc-<name>"; a zero detect omits the commits window.
func (e *env) writeManifest(t *testing.T, name string, detect time.Duration) (path, id string) {
	t.Helper()
	policies := ""
	if detect > 0 {
		policies = fmt.Sprintf("    policies:\n      detectUnroutedCommitsDuration: %s\n", detect)
	}
	return e.writeManifestWithPolicies(t, name, policies)
}

// Takes the conversion's policies block verbatim, for a test that writes an explicit window.
func (e *env) writeManifestWithPolicies(t *testing.T, name, policies string) (path, id string) {
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
  dynamicToStaticConversion:
    route:
      name: %q
      targetStreamingDomain: %q
%s`, id, e.sourceBootstrap, filepath.Join(e.renderedDir, "source-creds.yaml"),
		e.destClusterID, e.destBootstrap, e.restEndpoint, filepath.Join(e.renderedDir, "dest-kafka-creds.yaml"),
		e.linkName, e.destBootstrap, filepath.Join(e.renderedDir, "link-creds.yaml"),
		e.namespace, e.gateway, e.route, e.domains.Dest, policies)
	path = filepath.Join(e.renderedDir, id+".yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600), "write the conversion manifest")
	e.saveReport(t, "manifest.yaml", manifest)
	return path, id
}

// seam is one or more test-only switches (killpoint) set on a single kcp run.
type seam struct {
	env    []string // NAME=value, one per switch
	effect string
}

// cancelAfter stops a run right after the named FSM state, as an abrupt exit would.
func cancelAfter(state string) *seam {
	return &seam{env: []string{killpoint.EnvVar + "=" + state}, effect: "simulated abrupt exit"}
}

// failAt makes the named workflow step fail the way a real failure of it would.
func failAt(step string) *seam {
	return &seam{env: []string{killpoint.FailEnvVar + "=" + step}, effect: "simulated step failure"}
}

// heldAt holds a run after the named FSM state: kcp creates release+".held"
// on reaching it and waits until release exists.
func heldAt(state, release string) *seam {
	return &seam{
		env:    []string{killpoint.HoldEnvVar + "=" + state, killpoint.HoldFileEnvVar + "=" + release},
		effect: "holds after " + state + " until released",
	}
}

// holdPollInterval and holdTimeout bound executeHeldAt's wait for kcp to
// reach its hold point.
const (
	holdPollInterval = 500 * time.Millisecond
	holdTimeout      = 5 * time.Minute
)

// executeHeldAt runs `kcp migration cutover execute --migration-yaml manifest`
// (plus extra flags) held after state: once kcp reaches that state it pauses,
// whileHeld runs against the live world, and kcp is then released and runs
// on. Returns kcp's output and process error, as execute does.
func (e *env) executeHeldAt(t *testing.T, report, manifest, state string, whileHeld func(), extra ...string) (string, error) {
	t.Helper()
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

	args := append([]string{"migration", "cutover", "execute", "--migration-yaml", manifest}, extra...)
	go func() {
		out, err := e.runKCP(t, report, heldAt(state, release), nil, args...)
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
	select {
	case r := <-done:
		received = true
		return r.out, r.err
	case <-time.After(executeTimeout + time.Minute):
		t.Fatalf("the held kcp run did not return within %s of its release", executeTimeout+time.Minute)
		return "", nil
	}
}

// crGenerations reads the live gateway CR's metadata.generation and
// status.observedGeneration: the spec revision the CR holds, and the last one
// the operator observed.
func (e *env) crGenerations(t *testing.T, ctx context.Context) (generation, observed int64) {
	t.Helper()
	var cr struct {
		Metadata struct {
			Generation *int64 `yaml:"generation"`
		} `yaml:"metadata"`
		Status struct {
			ObservedGeneration *int64 `yaml:"observedGeneration"`
		} `yaml:"status"`
	}
	require.NoError(t, yaml.Unmarshal(e.readCR(t, ctx), &cr), "parse the gateway CR")
	require.NotNil(t, cr.Metadata.Generation, "the gateway CR must carry metadata.generation")
	require.NotNil(t, cr.Status.ObservedGeneration, "the gateway CR must carry status.observedGeneration")
	return *cr.Metadata.Generation, *cr.Status.ObservedGeneration
}

// bumpGatewayGeneration changes the gateway CR's spec out from under kcp, the
// way an operator's edit would: it JSON-patches spec.configId to a fresh
// "e2e-bump-<unix nanos>" value. That moves metadata.generation, which the
// call requires to have risen. Returns the new generation. The next test's
// reset writes a fresh configId, so nothing needs undoing.
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
// be Ready, then waits for it to accept the gateway CR, so the next test's
// reset starts from a settled gateway.
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

// execute runs `kcp migration cutover execute --migration-yaml manifest` with s (nil
// for a plain run); see runKCP.
func (e *env) execute(t *testing.T, report, manifest string, s *seam, onLine func(string)) (string, error) {
	t.Helper()
	return e.runKCP(t, report, s, onLine, "migration", "cutover", "execute", "--migration-yaml", manifest)
}

// dryRun runs `kcp migration cutover execute --migration-yaml manifest --dry-run`.
func (e *env) dryRun(t *testing.T, report, manifest string) (string, error) {
	t.Helper()
	return e.runKCP(t, report, nil, nil, "migration", "cutover", "execute", "--migration-yaml", manifest, "--dry-run")
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
		cmd.Env = append(cmd.Env, s.env...)
		desc += "    [" + strings.Join(s.env, " ") + " → " + s.effect + "]"
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
