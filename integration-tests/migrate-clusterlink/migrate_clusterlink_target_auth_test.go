//go:build integration

package migrateclusterlink

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMigrateApply_ClusterLink_TargetAuthMatrix exercises the three target-REST
// authentication methods KCP supports against the embedded Admin REST API:
// HTTP basic, mutual TLS, and an MDS-issued bearer token. The source side is
// fixed (plaintext) so each subtest isolates the target-auth dimension. For
// each: dry-run previews without creating, apply creates a link that reaches
// ACTIVE, and a second apply is an idempotent no-op.
func TestMigrateApply_ClusterLink_TargetAuthMatrix(t *testing.T) {
	// source REST ready => the source broker (link destination's source) is up.
	waitForClusterID(t, "http://localhost:18090")

	for _, m := range []struct {
		name     string
		manifest string
		link     string
		poller   func(t *testing.T) restPoller
	}{
		{"basic", "testdata/target-basic/migration.yaml", "tgt-basic", basicPoller},
		{"mtls", "testdata/target-mtls/migration.yaml", "tgt-mtls", mtlsPoller},
		{"bearer", "testdata/target-bearer/migration.yaml", "tgt-bearer", bearerPoller},
	} {
		t.Run(m.name, func(t *testing.T) {
			p := m.poller(t)
			p.waitForClusterID(t)
			destID := p.clusterID(t)
			require.NotEmpty(t, destID)

			// dry-run previews a create and changes nothing.
			out, err := runKCP(t, m.manifest, "--dry-run")
			require.NoError(t, err, out)
			require.Contains(t, out, "Planned")
			require.Equal(t, "", p.linkState(destID, m.link), "dry-run must not create the link")

			// apply creates the link, which reaches ACTIVE.
			out, err = runKCP(t, m.manifest)
			require.NoError(t, err, out)
			require.Contains(t, out, "1 created")
			p.requireLinkState(t, destID, m.link, "ACTIVE")

			// re-apply is an idempotent no-op.
			out, err = runKCP(t, m.manifest)
			require.NoError(t, err, out)
			require.Contains(t, out, "1 already present")
		})
	}
}

// restPoller queries a dest's embedded REST API with the right authentication,
// mirroring how KCP itself authenticates for each method.
type restPoller struct {
	base   string
	client *http.Client
	header string // Authorization header value; "" for mTLS (cert is the auth)
}

func basicPoller(t *testing.T) restPoller {
	return restPoller{
		base:   "http://localhost:28091",
		client: http.DefaultClient,
		header: "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:admin-secret")),
	}
}

func mtlsPoller(t *testing.T) restPoller {
	cert, err := tls.LoadX509KeyPair("certs/client.crt", "certs/client.key")
	require.NoError(t, err)
	caPEM, err := os.ReadFile("certs/ca.crt")
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(caPEM))
	return restPoller{
		base: "https://localhost:28092",
		client: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      pool,
		}}},
	}
}

// bearerPoller fetches a fresh JWT from MDS (basic auth, file user store) and
// writes the bearer target-creds.yaml the manifest references, then returns a
// poller that presents the token.
func bearerPoller(t *testing.T) restPoller {
	token := fetchMDSToken(t)
	require.NoError(t, os.WriteFile("testdata/target-bearer/target-creds.yaml",
		[]byte("bearer:\n  token: "+token+"\n"), 0600))
	return restPoller{
		base:   "http://localhost:28093",
		client: http.DefaultClient,
		header: "Bearer " + token,
	}
}

// fetchMDSToken obtains an auth token from the dest-bearer MDS, retrying until
// MDS is ready (the broker + RBAC store take time to come up).
func fetchMDSToken(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, "http://localhost:28093/security/1.0/authenticate", nil)
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
	t.Fatal("MDS at :28093 never issued a token")
	return ""
}

func (p restPoller) do(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, p.base+path, nil)
	if err != nil {
		return nil, err
	}
	if p.header != "" {
		req.Header.Set("Authorization", p.header)
	}
	return p.client.Do(req)
}

func (p restPoller) clusterID(t *testing.T) string {
	resp, err := p.do("/kafka/v3/clusters")
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

func (p restPoller) waitForClusterID(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		if p.clusterID(t) != "" {
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("cluster id at %s never became non-empty", p.base)
}

func (p restPoller) linkState(destID, linkName string) string {
	resp, err := p.do("/kafka/v3/clusters/" + destID + "/links/" + linkName)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		LinkState string `json:"link_state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	return body.LinkState
}

func (p restPoller) requireLinkState(t *testing.T, destID, linkName, want string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if p.linkState(destID, linkName) == want {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("link %q did not reach state %q (last: %q)", linkName, want, p.linkState(destID, linkName))
}
