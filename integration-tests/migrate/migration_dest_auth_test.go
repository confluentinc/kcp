//go:build integration

// This file is the LIVE proof for `kcp migration init | execute`'s new
// destination auth methods. Unlike migrate_clusterlink_*_test.go (which shell
// out to `kcp migrate apply`), the `kcp migration` init/execute commands cannot
// run against docker-compose alone — both require a live Kubernetes gateway CR
// (GetGatewayYAML / ResolveGatewayCapability), which only the Minikube e2e tier
// (integration-tests/migration/) provides. But the destination-auth code those
// commands run is NOT gated by the gateway: createDestinationOffset dials the
// destination broker BEFORE the gateway is ever touched, and the REST leg's auth
// wiring is the same clusterlink.ConfluentCloudService the FSM uses. So this
// file proves those exact production paths directly, in-process, against the
// same cp-server brokers `make test-migrate` brings up — no CLI, no k8s.
//
//   - Kafka leg: MigrateConn → AdminOptionForAuthMethod → NewKafkaClient, the
//     chain buildExecutorOpts + createDestinationOffset run. Reuses the source
//     broker's listeners (which already expose the full SASL_SSL/SSL/plaintext
//     matrix); the destination Kafka client is broker-agnostic, so pointing it
//     at those listeners exercises identical code.
//   - REST leg: targets.Credentials.HTTPClient()/Authenticator() driving a real
//     clusterlink call against dest-basic/dest-mtls/dest-bearer, the wiring
//     migration init (ListMirrorTopics) and execute (PromoteTopics) use.

package migrate

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// destKafkaCase is one destination Kafka-leg auth permutation: the migration
// destination credentials block (spec.target.kafka.credentials) and the source
// HOST listener it dials.
type destKafkaCase struct {
	name      string
	credsYAML string
	bootstrap string
}

// resolveDestKafkaAuth mirrors buildExecutorOpts
// (cmd/migration/execute/cmd_migration_execute.go:401-421): it resolves the
// destination Kafka auth from the migration destination credentials via
// MigrateConn and applies the SASL_SSL backward-compat default. Kept in lockstep
// with that function so this live test proves the same resolution the CLI runs.
func resolveDestKafkaAuth(t *testing.T, credsYAML string) (types.AuthType, types.AuthMethodConfig, bool) {
	t.Helper()
	creds, errs := types.ParseMigrateClusterCredentials([]byte(credsYAML))
	require.Empty(t, errs, "destination credentials must parse + validate")

	destConn := types.MigrateConn(nil, creds)
	authType, err := destConn.GetSelectedAuthType()
	require.NoError(t, err)
	method := destConn.AuthMethod

	// SASL_SSL compat default: a sasl_plain destination with neither ca_cert nor
	// tls dials SASL_SSL (a managed/CC destination is always TLS), never cleartext
	// SASL_PLAINTEXT. buildExecutorOpts applies this and
	// TestExecute_DestSASLPlainDefaultsToTLS asserts it at the unit level — here we
	// prove the defaulted method actually connects to a SASL_SSL listener.
	if sp := method.SASLPlain; sp != nil && sp.CACert == "" && !sp.UseTLS {
		sp.UseTLS = true
	}
	return authType, method, creds.InsecureSkipTLSVerify
}

// TestMigrationDestAuth_KafkaLeg proves every new destination Kafka auth method
// completes a real SASL/TLS handshake and an authenticated metadata read via the
// exact mapper createDestinationOffset uses. Listeners (source HOST): SASL_SSL
// 19093 (accepts SCRAM-256/512 + PLAIN), SSL 19094 (client-auth "requested", so
// both mtls and unauthenticated_tls), SASL_PLAINTEXT 19095, PLAINTEXT 19092.
func TestMigrationDestAuth_KafkaLeg(t *testing.T) {
	// Self-signed certs (source.keystore CN) → skip verification, exactly as the
	// migrate-apply matrix does for the same listeners.
	const skip = "\ninsecure_skip_tls_verify: true\n"
	cases := []destKafkaCase{
		{"sasl_scram_256", "sasl_scram: { username: kcp, password: kcp-secret, mechanism: SHA256, ca_cert: ./certs/ca.crt }" + skip, "localhost:19093"},
		{"sasl_scram_512", "sasl_scram: { username: kcp, password: kcp-secret, mechanism: SHA512, ca_cert: ./certs/ca.crt }" + skip, "localhost:19093"},
		{"mtls", "mtls: { ca_cert: ./certs/ca.crt, client_cert: ./certs/client.crt, client_key: ./certs/client.key }" + skip, "localhost:19094"},
		{"unauthenticated_tls", "unauthenticated_tls: { ca_cert: ./certs/ca.crt }" + skip, "localhost:19094"},
		{"unauthenticated_plaintext", "unauthenticated_plaintext: {}\n", "localhost:19092"},
		// sasl_plain over SASL_SSL — the managed/Confluent Cloud destination shape.
		{"sasl_plain_tls", "sasl_plain: { username: kcp, password: kcp-secret, tls: true }" + skip, "localhost:19093"},
		// THE compat regression (plan §A.3): a sasl_plain destination with neither
		// ca_cert nor tls. resolveDestKafkaAuth applies the SASL_SSL default, so it
		// MUST dial the SASL_SSL listener (19093) — without the default it would try
		// cleartext SASL_PLAINTEXT and the TLS listener would reject the handshake.
		{"sasl_plain_compat_default", "sasl_plain: { username: kcp, password: kcp-secret }" + skip, "localhost:19093"},
	}

	// Source HOST REST reachable => the source broker and all its listeners are up.
	newRestClient(t, restSource).waitForClusterID(t)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			authType, method, skipVerify := resolveDestKafkaAuth(t, c.credsYAML)

			authOpt, err := client.AdminOptionForAuthMethod(authType, method, skipVerify)
			require.NoError(t, err)

			kc, err := client.NewKafkaClient([]string{c.bootstrap}, "", authOpt)
			require.NoError(t, err, "destination auth handshake to %s (%s) must succeed", c.bootstrap, c.name)
			svc := offset.NewOffsetService(kc)
			defer func() { _ = svc.Close() }()

			// An authenticated metadata read (RefreshMetadata + list topics): proves
			// createDestinationOffset's client can actually talk to the broker, not
			// merely complete the SASL/TLS handshake.
			_, err = svc.Exists("__consumer_offsets")
			require.NoError(t, err, "authenticated metadata read against %s must succeed", c.bootstrap)
		})
	}
}

// TestMigrationDestAuth_RESTLeg proves every new destination REST auth method
// authenticates a real clusterlink.ConfluentCloudService call. GetKafkaClusterID
// (GET /kafka/v3/clusters) is an authenticated request that returns 200 with the
// cluster id — the exact HTTPClient() + Config.Auth wiring migration init
// (ListMirrorTopics) and execute (PromoteTopics) drive, without needing a
// pre-existing cluster link.
func TestMigrationDestAuth_RESTLeg(t *testing.T) {
	ctx := context.Background()
	// Each dest* service varies only its REST auth surface (Kafka is plaintext);
	// restDest (no-auth) is covered by the existing api_key/basic path, so this
	// sweeps only the newly-unlocked kinds.
	for _, e := range []restEndpoint{restDestBasic, restDestMTLS, restDestBearer} {
		t.Run(e.kind.String(), func(t *testing.T) {
			newRestClient(t, e).waitForClusterID(t) // dest REST reachable

			dir := t.TempDir()
			credsPath := writeRestCreds(t, dir, "target-creds.yaml", e)
			creds, err := targets.LoadCredentials(credsPath)
			require.NoError(t, err)

			httpClient, err := creds.HTTPClient()
			require.NoError(t, err)

			svc := clusterlink.NewConfluentCloudService(httpClient)
			cfg := clusterlink.Config{
				RestEndpoint: e.baseURL,
				ClusterID:    e.clusterID,
				Auth:         creds.Authenticator(),
			}
			id, err := svc.GetKafkaClusterID(ctx, cfg)
			require.NoError(t, err, "authenticated REST call to %s (%s) must succeed", e.baseURL, e.kind)
			require.Equal(t, e.clusterID, id)
		})
	}
}
