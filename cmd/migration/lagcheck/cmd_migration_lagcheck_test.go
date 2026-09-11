package lagcheck

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/testsupport"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lagCheckManifestTmpl is the canonical lag-check manifest. Credentials are file
// paths; the __*_CRED__ tokens are replaced with real temp files by
// writeLagManifestFull so every leg resolves.
const lagCheckManifestTmpl = `apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: msk-prod-to-cc-batch-1
spec:
  source:
    type: msk
    bootstrapServers:
      - b-1.msk.us-east-1.amazonaws.com:9096
    credentials: __SOURCE_CRED__
  target:
    type: confluent-cloud
    clusterId: lkc-abc123
    kafka:
      bootstrapServers:
        - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
      restEndpoint: https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443
      clusterCredentials: __DEST_CRED__
  clusterLink:
    name: msk-to-cc
    bootstrapServers:
      - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
    linkCredentials: __LINK_CRED__
  gateway:
    namespace: confluent
    cr-name: gateway-initial
  topicGroup:
    - topicPatterns:
        - '.*'
      route: migration-route
      targetStreamingDomain: confluent-cloud
`

// Default credential-file bodies for the canonical lag-check manifest. The
// link leg's api_key/api_secret are what buildLagCheckConfig resolves into the
// cluster-link Auth.
const (
	defLagSourceCred = "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA512\n"
	defLagDestCred   = "sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n  tls: true\n"
	defLagLinkCred   = "api_key: CC_KEY\napi_secret: CC_SECRET\n"
)

// testClientCertPEM / testClientKeyPEM are a matching self-signed EC
// cert/key pair (tls.LoadX509KeyPair requires a real match, unlike a CA pool
// which only needs a valid PEM certificate block).
const testClientCertPEM = `-----BEGIN CERTIFICATE-----
MIIBjDCCATGgAwIBAgIUA/4TGWc0qi/IPJxzmUwI38mlbWgwCgYIKoZIzj0EAwIw
GjEYMBYGA1UEAwwPa2NwLXRlc3QtY2xpZW50MCAXDTI2MDgyODA3NTk0NFoYDzIx
MjYwODA0MDc1OTQ0WjAaMRgwFgYDVQQDDA9rY3AtdGVzdC1jbGllbnQwWTATBgcq
hkjOPQIBBggqhkjOPQMBBwNCAAR4vuBHztIw8/su1HVf+jxMh0F4AqiwU+PcgMDY
T48U6chMykYv0VBh4vquBYLBrC9AlTlIdCZaELbpCe7FU3yOo1MwUTAdBgNVHQ4E
FgQUQJ8HqBMoP5Y1Ufj1yIvYDJkmpNYwHwYDVR0jBBgwFoAUQJ8HqBMoP5Y1Ufj1
yIvYDJkmpNYwDwYDVR0TAQH/BAUwAwEB/zAKBggqhkjOPQQDAgNJADBGAiEAtFEq
b8ddh9vpNTBtpdc9hZ5t+qFoD7SnopH/XLzAx1YCIQDTjYk6/N1n9qaAIjuAvRmw
qE1MOvjjj3nTE4fDY6Em7w==
-----END CERTIFICATE-----
`

const testClientKeyPEM = `-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgj7WeQdDBvKMUoZyY
nLE2wnGyC1mK55bN0QKQGVdBvmShRANCAAR4vuBHztIw8/su1HVf+jxMh0F4Aqiw
U+PcgMDYT48U6chMykYv0VBh4vquBYLBrC9AlTlIdCZaELbpCe7FU3yO
-----END PRIVATE KEY-----
`

// writeLagManifestFull writes the three credentials files (using the given
// bodies or the canonical defaults for empty ones) into a temp dir, renders the
// manifest pointing at them, applies an optional text mutation, and returns the
// manifest path.
func writeLagManifestFull(t *testing.T, source, dest, link string, mutate func(string) string) string {
	t.Helper()
	dir := t.TempDir()
	doc := lagCheckManifestTmpl
	doc = strings.Replace(doc, "__SOURCE_CRED__", testsupport.WriteCredFile(t, dir, "source-creds.yaml", source, defLagSourceCred), 1)
	doc = strings.Replace(doc, "__DEST_CRED__", testsupport.WriteCredFile(t, dir, "dest-kafka-creds.yaml", dest, defLagDestCred), 1)
	doc = strings.Replace(doc, "__LINK_CRED__", testsupport.WriteCredFile(t, dir, "link-creds.yaml", link, defLagLinkCred), 1)
	if mutate != nil {
		doc = mutate(doc)
	}
	p := filepath.Join(dir, "gateway-migration.yaml")
	require.NoError(t, os.WriteFile(p, []byte(doc), 0600))
	return p
}

// writeLagManifest renders the canonical manifest (default credentials), with an
// optional text mutation.
func writeLagManifest(t *testing.T, mutate func(string) string) string {
	return writeLagManifestFull(t, "", "", "", mutate)
}

// writeLagLink renders the canonical manifest with a custom cluster-link (REST)
// credentials file body — the leg lag-check authenticates with.
func writeLagLink(t *testing.T, link string) string {
	return writeLagManifestFull(t, "", "", link, nil)
}

func loadLagGateway(t *testing.T, path string) *manifest.GatewayMigration {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	g, err := manifest.ParseGatewayMigration(data)
	require.NoError(t, err)
	return g
}

// TestLagCheck_FlagSurfaceIsTwoFlags — decision 15: --migration-yaml and
// --poll-interval, nothing else. lag-check reads no state file, so a
// --migration-id would resolve nothing.
func TestLagCheck_FlagSurfaceIsTwoFlags(t *testing.T) {
	cmd := NewMigrationLagCheckCmd()
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	assert.ElementsMatch(t, []string{"migration-yaml", "poll-interval"}, names)
}

func TestLagCheck_RetiredFlagsAreGone(t *testing.T) {
	for _, flag := range []string{
		"--rest-endpoint", "--cluster-id", "--cluster-link-name",
		"--cluster-api-key", "--cluster-api-secret",
	} {
		t.Run(flag, func(t *testing.T) {
			cmd := NewMigrationLagCheckCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"--migration-yaml", writeLagManifest(t, nil), flag, "x"})
			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown flag")
		})
	}
}

func TestLagCheck_RequiresMigrationYaml(t *testing.T) {
	cmd := NewMigrationLagCheckCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(nil)
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-yaml")
}

// TestLagCheck_BuildsConfigFromManifest — the five values it needs map 1:1
// onto clusterlink.Config, and all five are in the manifest (the REST auth from
// spec.clusterLink.linkCredentials).
func TestLagCheck_BuildsConfigFromManifest(t *testing.T) {
	g := loadLagGateway(t, writeLagManifest(t, nil))
	cfg, _, err := buildLagCheckConfig(g)
	require.NoError(t, err)

	assert.Equal(t, "https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443", cfg.RestEndpoint)
	assert.Equal(t, "lkc-abc123", cfg.ClusterID)
	assert.Equal(t, "msk-to-cc", cfg.LinkName)
	assert.Equal(t, clusterlink.BasicAuth{Username: "CC_KEY", Password: "CC_SECRET"}, cfg.Auth)
}

// TestLagCheck_AlwaysWatchesEveryMirrorTopic — the topicGroup topic selection
// has NO effect here; clusterlink.Config.Topics is empty so the TUI shows every
// mirror.
func TestLagCheck_AlwaysWatchesEveryMirrorTopic(t *testing.T) {
	g := loadLagGateway(t, writeLagManifest(t, func(doc string) string {
		return strings.Replace(doc,
			"    - topicPatterns:\n        - '.*'\n", "    - topics:\n        - t1.order\n", 1)
	}))
	cfg, _, err := buildLagCheckConfig(g)
	require.NoError(t, err)
	assert.Empty(t, cfg.Topics, "the topicGroup selection must not narrow the lag view")
}

// TestLagCheck_UsesLinkCredentials — the cluster-link REST auth comes from
// spec.clusterLink.linkCredentials, read exactly as written (no derivation).
func TestLagCheck_UsesLinkCredentials(t *testing.T) {
	g := loadLagGateway(t, writeLagLink(t, "api_key: EXPLICIT_KEY\napi_secret: EXPLICIT_SECRET\n"))
	cfg, _, err := buildLagCheckConfig(g)
	require.NoError(t, err)
	assert.Equal(t, clusterlink.BasicAuth{Username: "EXPLICIT_KEY", Password: "EXPLICIT_SECRET"}, cfg.Auth)
}

// TestLagCheck_BuildsBasicAuthConfig — a basic linkCredentials block maps to
// clusterlink.BasicAuth carrying its own username/password, not the
// api_key-derived ones.
func TestLagCheck_BuildsBasicAuthConfig(t *testing.T) {
	g := loadLagGateway(t, writeLagLink(t, "basic:\n  username: BASIC_USER\n  password: BASIC_PASS\n"))
	cfg, _, err := buildLagCheckConfig(g)
	require.NoError(t, err)
	assert.Equal(t, clusterlink.BasicAuth{Username: "BASIC_USER", Password: "BASIC_PASS"}, cfg.Auth)
}

// TestLagCheck_BuildsBearerAuthConfig — a bearer linkCredentials block maps to
// clusterlink.BearerAuth; the api_key/basic fallback in Config.authenticator()
// must never be reached for this form.
func TestLagCheck_BuildsBearerAuthConfig(t *testing.T) {
	g := loadLagGateway(t, writeLagLink(t, "bearer:\n  token: BEARER_TOKEN\n"))
	cfg, _, err := buildLagCheckConfig(g)
	require.NoError(t, err)
	assert.Equal(t, clusterlink.BearerAuth{Token: "BEARER_TOKEN"}, cfg.Auth)
}

// TestLagCheck_BuildsMTLSAuthConfig — an mtls linkCredentials block maps to
// clusterlink.NoHeaderAuth (auth happens at the TLS layer), and the returned
// HTTP client must actually present the client certificate, not just exist.
func TestLagCheck_BuildsMTLSAuthConfig(t *testing.T) {
	certDir := t.TempDir()
	cert := filepath.Join(certDir, "client.pem")
	key := filepath.Join(certDir, "client-key.pem")
	require.NoError(t, os.WriteFile(cert, []byte(testClientCertPEM), 0600))
	require.NoError(t, os.WriteFile(key, []byte(testClientKeyPEM), 0600))

	g := loadLagGateway(t, writeLagLink(t, "mtls:\n  client_cert: "+cert+"\n  client_key: "+key+"\n"))
	cfg, httpClient, err := buildLagCheckConfig(g)
	require.NoError(t, err)
	assert.Equal(t, clusterlink.NoHeaderAuth{}, cfg.Auth)

	client, ok := httpClient.(*http.Client)
	require.True(t, ok, "expected *http.Client")
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "expected *http.Transport")
	require.NotNil(t, transport.TLSClientConfig)
	assert.NotEmpty(t, transport.TLSClientConfig.Certificates, "mtls must present the client cert")
}

// TestLagCheck_HonoursRestTLSTrust — a private-CA or self-signed destination
// REST endpoint must be reachable, or the linkCredentials TLS-trust field would
// be a silent no-op.
func TestLagCheck_HonoursRestTLSTrust(t *testing.T) {
	g := loadLagGateway(t, writeLagLink(t, "api_key: CC_KEY\napi_secret: CC_SECRET\ninsecure_skip_verify: true\n"))
	_, client, err := buildLagCheckConfig(g)
	require.NoError(t, err)
	require.NotNil(t, client, "an HTTP client carrying the REST leg's TLS trust must be built")
}

// TestLagCheck_HonoursPerBlockRestTLSTrust — basic, bearer and mtls each carry
// their own ca_cert/insecure_skip_verify siblings, distinct from the
// api_key-only top-level fields (internal/targets/credentials.go's
// Credentials.CACert/InsecureSkipVerify). buildLagCheckConfig must read the
// active block's own copy via restCreds.HTTPClient(), not a top-level field
// that doesn't even apply to these forms.
func TestLagCheck_HonoursPerBlockRestTLSTrust(t *testing.T) {
	certDir := t.TempDir()
	cert := filepath.Join(certDir, "client.pem")
	key := filepath.Join(certDir, "client-key.pem")
	require.NoError(t, os.WriteFile(cert, []byte(testClientCertPEM), 0600))
	require.NoError(t, os.WriteFile(key, []byte(testClientKeyPEM), 0600))

	for name, link := range map[string]string{
		"basic":  "basic:\n  username: u\n  password: p\n  insecure_skip_verify: true\n",
		"bearer": "bearer:\n  token: TOK\n  insecure_skip_verify: true\n",
		"mtls":   "mtls:\n  client_cert: " + cert + "\n  client_key: " + key + "\n  insecure_skip_verify: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, httpClient, err := buildLagCheckConfig(loadLagGateway(t, writeLagLink(t, link)))
			require.NoError(t, err)

			client, ok := httpClient.(*http.Client)
			require.True(t, ok, "expected *http.Client")
			transport, ok := client.Transport.(*http.Transport)
			require.True(t, ok, "expected *http.Transport")
			require.NotNil(t, transport.TLSClientConfig)
			assert.True(t, transport.TLSClientConfig.InsecureSkipVerify,
				"the block's own insecure_skip_verify must be honoured")
		})
	}
}

// TestLagCheck_HonoursPerBlockRestCACert — the CA-trust sibling of
// TestLagCheck_HonoursPerBlockRestTLSTrust: basic, bearer and mtls each accept
// their own ca_cert, which must be loaded into the HTTP client's RootCAs, not
// silently dropped because only the api_key-only top-level field was read.
func TestLagCheck_HonoursPerBlockRestCACert(t *testing.T) {
	certDir := t.TempDir()
	caPath := filepath.Join(certDir, "ca.pem")
	require.NoError(t, os.WriteFile(caPath, []byte(testClientCertPEM), 0600)) // any valid PEM certificate is loadable as a CA pool entry

	clientCert := filepath.Join(certDir, "client.pem")
	clientKey := filepath.Join(certDir, "client-key.pem")
	require.NoError(t, os.WriteFile(clientCert, []byte(testClientCertPEM), 0600))
	require.NoError(t, os.WriteFile(clientKey, []byte(testClientKeyPEM), 0600))

	for name, link := range map[string]string{
		"basic":  "basic:\n  username: u\n  password: p\n  ca_cert: " + caPath + "\n",
		"bearer": "bearer:\n  token: TOK\n  ca_cert: " + caPath + "\n",
		"mtls":   "mtls:\n  client_cert: " + clientCert + "\n  client_key: " + clientKey + "\n  ca_cert: " + caPath + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, httpClient, err := buildLagCheckConfig(loadLagGateway(t, writeLagLink(t, link)))
			require.NoError(t, err)

			client, ok := httpClient.(*http.Client)
			require.True(t, ok, "expected *http.Client")
			transport, ok := client.Transport.(*http.Transport)
			require.True(t, ok, "expected *http.Transport")
			require.NotNil(t, transport.TLSClientConfig)
			assert.NotNil(t, transport.TLSClientConfig.RootCAs, "the block's own ca_cert must be loaded into RootCAs")
		})
	}
}

// TestLagCheck_PropagatesRestCredentialsResolutionError — a linkCredentials file
// that fails to resolve (here an api_key with no api_secret) must surface through
// buildLagCheckConfig's wrap, not be swallowed or panic.
func TestLagCheck_PropagatesRestCredentialsResolutionError(t *testing.T) {
	g := loadLagGateway(t, writeLagLink(t, "api_key: LONELY_KEY\n"))
	_, _, err := buildLagCheckConfig(g)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving destination REST credentials")
	assert.Contains(t, err.Error(), "api_key and api_secret must both be set or both omitted")
}

// TestLagCheck_PropagatesHTTPClientBuildError — an mtls client_cert/client_key
// pair that exists on disk (so ValidateCredentials' os.Stat check passes) but
// isn't valid certificate/key material fails at tls.LoadX509KeyPair; that
// error must surface through buildLagCheckConfig's wrap.
func TestLagCheck_PropagatesHTTPClientBuildError(t *testing.T) {
	certDir := t.TempDir()
	cert := filepath.Join(certDir, "client.pem")
	key := filepath.Join(certDir, "client-key.pem")
	require.NoError(t, os.WriteFile(cert, []byte("not a real certificate"), 0600))
	require.NoError(t, os.WriteFile(key, []byte("not a real key"), 0600))

	g := loadLagGateway(t, writeLagLink(t, "mtls:\n  client_cert: "+cert+"\n  client_key: "+key+"\n"))
	_, _, err := buildLagCheckConfig(g)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "building destination REST client")
}

// TestLagCheck_SendsConfiguredAuthOnWire — an end-to-end trip-wire for this
// exact bug: it drives a bearer linkCredentials block all the way through
// buildLagCheckConfig and a real clusterlink.NewConfluentCloudService request,
// and asserts the Authorization header actually placed on the wire, rather
// than the Auth field's shape/value in isolation.
func TestLagCheck_SendsConfiguredAuthOnWire(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"link_name":"msk-to-cc"}`))
	}))
	defer server.Close()

	g := loadLagGateway(t, writeLagManifestFull(t, "", "", "bearer:\n  token: BEARER_WIRE_TOKEN\n",
		func(doc string) string {
			return strings.Replace(doc, "https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443", server.URL, 1)
		}))
	cfg, httpClient, err := buildLagCheckConfig(g)
	require.NoError(t, err)

	svc := clusterlink.NewConfluentCloudService(httpClient)
	_, err = svc.GetClusterLink(context.Background(), cfg)
	require.NoError(t, err)
	assert.Equal(t, "Bearer BEARER_WIRE_TOKEN", gotAuth)
}

// TestLagCheck_WorksBeforeInitHasEverRun — its standalone use is preserved: the
// manifest can exist before any migration is registered, and lag-check reads no
// state file.
func TestLagCheck_WorksBeforeInitHasEverRun(t *testing.T) {
	g := loadLagGateway(t, writeLagManifest(t, nil))
	_, _, err := buildLagCheckConfig(g)
	require.NoError(t, err)
}

func TestLagCheck_RejectsMigrationKindManifest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "migration.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: x\n"), 0600))

	cmd := NewMigrationLagCheckCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--migration-yaml", p})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GatewayMigration")
}

func TestLagCheck_ClampsPollInterval(t *testing.T) {
	assert.Equal(t, 1, clampPollInterval(0))
	assert.Equal(t, 1, clampPollInterval(-5))
	assert.Equal(t, 60, clampPollInterval(999))
	assert.Equal(t, 7, clampPollInterval(7))
}
