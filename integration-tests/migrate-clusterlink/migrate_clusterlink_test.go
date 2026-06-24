//go:build integration

// Package migrateclusterlink is a live, end-to-end auth matrix for
// `kcp migrate apply` against cluster links, run against the cp-server brokers
// brought up by `make test-migrate-clusterlink`.
//
// The matrix sweeps each independent authentication surface on its own, holding
// every other surface fixed, so a failure points at exactly one surface. The
// surfaces are:
//
//   - D1: spec.source.credentials — KCP's read of the (migration-)source cluster
//     id (apache-kafka creds).
//   - D2: spec.clusterLink.sourceCredentials — the link→source connection in
//     DESTINATION mode (apache-kafka creds).
//   - D3: spec.target.{credentials,kafka.restEndpoint} — the target REST where
//     the link is created (target REST creds).
//   - D4: spec.clusterLink.sourceRest — the migration-source REST where the
//     OUTBOUND link is created in SOURCE mode (target REST creds).
//   - D5: spec.clusterLink.destinationCredentials — the source→destination
//     connection in SOURCE mode (apache-kafka creds).
//
// Each test case: dry-run (asserts "Planned", nothing created) → apply (asserts
// the created count + link(s) ACTIVE) → re-apply (asserts "already present") →
// the link(s) are deleted to keep the concurrent link count low.
package migrateclusterlink

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- broker cluster ids (from docker-compose CLUSTER_ID) ---
const (
	sourceClusterID     = "6ub6fPVJRzKjE4i-REkq-A"
	destClusterID       = "LKsbYRvfTM-TVXKjdjgdxA"
	destBasicClusterID  = "gFoyBzWLw8b-Q-4UDIXcvw"
	destMTLSClusterID   = "CA6bwVKcQR_Mn-E_v1oPlw"
	destBearerClusterID = "nCfjnCUInkjOvoJqi621RA"
)

// ---------------------------------------------------------------------------
// apache-kafka credential auth specs (D1 read / D2 link→source / D5 link→dest)
// ---------------------------------------------------------------------------

// kafkaAuthKind enumerates the apache-kafka auth methods the matrix exercises.
type kafkaAuthKind int

const (
	authPlaintext kafkaAuthKind = iota // unauthenticated_plaintext
	authScram256                       // sasl_scram SHA256
	authScram512                       // sasl_scram SHA512
	authPlain                          // sasl_plain
	authTLS                            // unauthenticated_tls (encryption only)
	authMTLS                           // tls (mutual)
)

func (k kafkaAuthKind) String() string {
	switch k {
	case authPlaintext:
		return "plaintext"
	case authScram256:
		return "scram256"
	case authScram512:
		return "scram512"
	case authPlain:
		return "plain"
	case authTLS:
		return "tls"
	case authMTLS:
		return "mtls"
	}
	return "unknown"
}

// kafkaAuth is one apache-kafka credentials cluster: an auth method bound to a
// bootstrap address. The same struct is used for the source read (D1, host
// listeners) and for the link's source/dest connections (D2/D5, docker
// listeners) — only the addresses differ.
type kafkaAuth struct {
	kind      kafkaAuthKind
	bootstrap string
}

// authMethodYAML renders the `auth_method:` block (and any sibling top-level
// keys like insecure_skip_tls_verify) for this auth kind, at 0-base indent
// (suitable for the flat single-cluster credential format).
func (a kafkaAuth) authMethodYAML() string {
	switch a.kind {
	case authPlaintext:
		return "unauthenticated_plaintext: {}\n"
	case authScram256:
		return "sasl_scram: { username: kcp, password: kcp-secret, mechanism: SHA256, ca_cert: ./certs/ca.crt }\n" +
			"insecure_skip_tls_verify: true\n"
	case authScram512:
		return "sasl_scram: { username: kcp, password: kcp-secret, mechanism: SHA512, ca_cert: ./certs/ca.crt }\n" +
			"insecure_skip_tls_verify: true\n"
	case authPlain:
		return "sasl_plain: { username: kcp, password: kcp-secret }\n"
	case authTLS:
		return "unauthenticated_tls: { ca_cert: ./certs/ca.crt }\n" +
			"insecure_skip_tls_verify: true\n"
	case authMTLS:
		return "tls: { ca_cert: ./certs/ca.crt, client_cert: ./certs/client.crt, client_key: ./certs/client.key }\n" +
			"insecure_skip_tls_verify: true\n"
	}
	panic("unknown kafka auth kind")
}

// writeKafkaCreds writes a flat single-cluster migrate credentials file.
func writeKafkaCreds(t *testing.T, path string, a kafkaAuth) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(a.authMethodYAML()), 0600))
}

// ---------------------------------------------------------------------------
// target REST auth specs (D3 target / D4 source REST)
// ---------------------------------------------------------------------------

// restAuthKind enumerates the target-REST auth methods the matrix exercises.
type restAuthKind int

const (
	restNone   restAuthKind = iota // no-auth REST (creds block accepted-but-ignored: basic)
	restBasic                      // HTTP basic
	restMTLS                       // mutual TLS
	restBearer                     // MDS-issued bearer JWT
)

func (k restAuthKind) String() string {
	switch k {
	case restNone:
		return "none"
	case restBasic:
		return "basic"
	case restMTLS:
		return "mtls"
	case restBearer:
		return "bearer"
	}
	return "unknown"
}

// restEndpoint is a target REST surface: a base URL, its auth kind, and the
// cluster id of the broker behind it (for polling link_state).
type restEndpoint struct {
	baseURL   string
	kind      restAuthKind
	clusterID string
}

var (
	restDest       = restEndpoint{"http://localhost:28090", restNone, destClusterID}
	restDestBasic  = restEndpoint{"http://localhost:28091", restBasic, destBasicClusterID}
	restDestMTLS   = restEndpoint{"https://localhost:28092", restMTLS, destMTLSClusterID}
	restDestBearer = restEndpoint{"http://localhost:28093", restBearer, destBearerClusterID}
	// restSource is the source broker's no-auth REST (used as a migration-dest
	// or migration-source REST in source mode).
	restSource = restEndpoint{"http://localhost:18090", restNone, sourceClusterID}
)

// writeRestCreds writes a target-creds.yaml for the given REST endpoint and
// returns the path. For bearer it mints a fresh MDS JWT at call time.
func writeRestCreds(t *testing.T, dir, name string, e restEndpoint) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var body string
	switch e.kind {
	case restNone, restBasic:
		// A no-auth REST ignores the basic block; a basic REST requires it.
		body = "basic:\n  username: admin\n  password: admin-secret\n"
	case restMTLS:
		body = "mtls:\n" +
			"  ca_cert: ./certs/ca.crt\n" +
			"  client_cert: ./certs/client.crt\n" +
			"  client_key: ./certs/client.key\n" +
			"  insecure_skip_verify: true\n"
	case restBearer:
		token := fetchMDSToken(t, e.baseURL)
		body = "bearer:\n  token: " + token + "\n"
	}
	require.NoError(t, os.WriteFile(path, []byte(body), 0600))
	return path
}

// ---------------------------------------------------------------------------
// kcp invocation
// ---------------------------------------------------------------------------

// runKCP runs the built ../../kcp binary with `migrate apply -f <manifest>` from
// this directory (so the manifest's ./certs/* relative paths resolve).
func runKCP(t *testing.T, manifest string, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"migrate", "apply", "-f", manifest}, extra...)
	cmd := exec.Command("../../kcp", args...)
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// ---------------------------------------------------------------------------
// REST poller — reads link_state / cluster id with the right auth, and deletes.
// ---------------------------------------------------------------------------

type restClient struct {
	base   string
	client *http.Client
	header string // Authorization header value; "" for none/mtls
}

// newRestClient builds an HTTP client + auth header for a REST endpoint. For
// bearer it mints a fresh token (the poller's token can differ from the one KCP
// used; both are valid MDS JWTs).
func newRestClient(t *testing.T, e restEndpoint) restClient {
	t.Helper()
	switch e.kind {
	case restNone:
		return restClient{base: e.baseURL, client: http.DefaultClient}
	case restBasic:
		return restClient{base: e.baseURL, client: http.DefaultClient,
			header: "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:admin-secret"))}
	case restMTLS:
		cert, err := tls.LoadX509KeyPair("certs/client.crt", "certs/client.key")
		require.NoError(t, err)
		caPEM, err := os.ReadFile("certs/ca.crt")
		require.NoError(t, err)
		pool := x509.NewCertPool()
		require.True(t, pool.AppendCertsFromPEM(caPEM))
		return restClient{base: e.baseURL, client: &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: pool},
		}}}
	case restBearer:
		token := fetchMDSToken(t, e.baseURL)
		return restClient{base: e.baseURL, client: http.DefaultClient, header: "Bearer " + token}
	}
	t.Fatalf("unknown rest auth kind %v", e.kind)
	return restClient{}
}

func (c restClient) do(method, path string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	if c.header != "" {
		req.Header.Set("Authorization", c.header)
	}
	return c.client.Do(req)
}

func (c restClient) clusterID() string {
	resp, err := c.do(http.MethodGet, "/kafka/v3/clusters")
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		Data []struct {
			ClusterID string `json:"cluster_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Data) == 0 {
		return ""
	}
	return body.Data[0].ClusterID
}

func (c restClient) waitForClusterID(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		if c.clusterID() != "" {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("cluster id at %s never became non-empty", c.base)
}

// link returns the named link's (link_state, link_error), or ("","") if absent
// or momentarily unreachable (so the poll retries through transient blips).
func (c restClient) link(clusterID, name string) (state, linkErr string) {
	resp, err := c.do(http.MethodGet, "/kafka/v3/clusters/"+clusterID+"/links/"+name)
	if err != nil {
		return "", ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	var body struct {
		LinkState string `json:"link_state"`
		LinkError string `json:"link_error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", ""
	}
	return body.LinkState, body.LinkError
}

func (c restClient) linkState(clusterID, name string) string {
	s, _ := c.link(clusterID, name)
	return s
}

// requireLinkActive polls until the link reaches ACTIVE, failing with the last
// observed state + link_error on timeout (link_error explains a FAILED link).
func (c restClient) requireLinkActive(t *testing.T, clusterID, name string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var state, linkErr string
	for time.Now().Before(deadline) {
		state, linkErr = c.link(clusterID, name)
		if state == "ACTIVE" {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("link %q on %s did not reach ACTIVE (last state %q, link_error %q)", name, clusterID, state, linkErr)
}

// deleteLink removes the link so concurrent links stay sparse. Best-effort:
// failures are logged, not fatal (a later run recreates idempotently).
func (c restClient) deleteLink(t *testing.T, clusterID, name string) {
	t.Helper()
	resp, err := c.do(http.MethodDelete, "/kafka/v3/clusters/"+clusterID+"/links/"+name)
	if err != nil {
		t.Logf("delete link %q on %s: %v", name, clusterID, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Logf("delete link %q on %s: unexpected status %d", name, clusterID, resp.StatusCode)
	}
}

// fetchMDSToken obtains an MDS auth token from a dest-bearer-style endpoint,
// retrying until MDS is ready.
func fetchMDSToken(t *testing.T, baseURL string) string {
	t.Helper()
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/security/1.0/authenticate", nil)
		req.SetBasicAuth("kcp", "kcp-secret")
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var body struct {
				AuthToken string `json:"auth_token"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()
			if body.AuthToken != "" {
				return body.AuthToken
			}
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("MDS at %s never issued a token", baseURL)
	return ""
}

// ---------------------------------------------------------------------------
// manifest generation
// ---------------------------------------------------------------------------

// runID is a per-process suffix so link names are unique not just across test
// cases in one run but across runs — a previous run that failed before cleanup
// leaves stale links on the brokers, and a fresh run must not collide with them.
var runID = fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)

// linkSeqCh hands out monotonic sequence numbers (concurrency-safe, so it is
// correct whether test cases run serially or in parallel).
var linkSeqCh = func() chan int {
	ch := make(chan int)
	go func() {
		for i := 1; ; i++ {
			ch <- i
		}
	}()
	return ch
}()

// uniqueLinkName makes link names unique per test case (and per run) so a re-run
// never collides with links left by a prior run, and concurrent test cases never
// share a name (cp-server keys links by name within a cluster).
func uniqueLinkName(prefix string) string {
	return fmt.Sprintf("%s-%s-%d", prefix, runID, <-linkSeqCh)
}

// ---------------------------------------------------------------------------
// cluster-link config helpers
// ---------------------------------------------------------------------------

// getLinkConfigs reads the link's live config map via GET .../links/{name}/configs.
func getLinkConfigs(t *testing.T, c restClient, clusterID, name string) map[string]string {
	t.Helper()
	resp, err := c.do(http.MethodGet, "/kafka/v3/clusters/"+clusterID+"/links/"+name+"/configs")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Data []struct{ Name, Value string } `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	out := map[string]string{}
	for _, c := range body.Data {
		out[c.Name] = c.Value
	}
	return out
}

// linkConfigsJSON returns the link's /configs response as pretty JSON for the
// evidence report. Only called when reportEnabled.
func linkConfigsJSON(c restClient, clusterID, name string) string {
	resp, err := c.do(http.MethodGet, "/kafka/v3/clusters/"+clusterID+"/links/"+name+"/configs")
	if err != nil {
		return fmt.Sprintf("<configs GET failed: %v>", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Sprintf("<configs GET decode failed (status %d): %v>", resp.StatusCode, err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return string(raw)
	}
	return pretty.String()
}
