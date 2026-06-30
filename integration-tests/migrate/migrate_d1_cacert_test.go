//go:build integration

package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMigrateApply_D1_SASLSSL_CACert proves the D1 source-read path
// (spec.source.credentials) through the Task 1 fix: a custom ca_cert in the
// migrate credentials file is honoured for the SASL_SSL source read without
// requiring insecure_skip_tls_verify.
//
// The source broker at localhost:19093 uses a self-signed certificate issued by
// the test CA (certs/ca.crt). Its SAN covers "localhost", so TLS hostname
// verification passes when the CA is trusted via ca_cert without a skip-verify
// bypass.
//
//   - Positive: ca_cert present, no insecure_skip_tls_verify → apply succeeds
//     (source read completes, link reaches ACTIVE).
//   - Negative: no ca_cert, no insecure_skip_tls_verify → apply fails with a
//     TLS/x509 error (self-signed CA not in system roots, proof the CA does the
//     work, not system trust).
func TestMigrateApply_D1_SASLSSL_CACert(t *testing.T) {
	// Wait for the source broker to be up (same guard the matrix uses).
	newRestClient(t, restSource).waitForClusterID(t)

	t.Run("positive/ca_cert_no_skip_verify", func(t *testing.T) {
		dir := t.TempDir()
		linkName := uniqueLinkName("d1cacert")

		// D1: SCRAM-SHA-512 at the SASL_SSL HOST listener; ca_cert present;
		// insecure_skip_tls_verify intentionally absent.
		srcCredsPath := filepath.Join(dir, "source-creds.yaml")
		require.NoError(t, os.WriteFile(srcCredsPath, []byte(
			"sasl_scram: { username: kcp, password: kcp-secret, mechanism: SHA512, ca_cert: ./certs/ca.crt }\n",
		), 0600))

		// D2: plaintext docker listener (same as the matrix baseline).
		linkCredsPath := filepath.Join(dir, "link-source-creds.yaml")
		writeKafkaCreds(t, linkCredsPath, kafkaAuth{authPlaintext, "source:29092"})

		// D3: no-auth destination REST.
		targetCredsPath := writeRestCreds(t, dir, "target-creds.yaml", restDest)

		manifest := "apiVersion: kcp.confluent.io/v1alpha1\n" +
			"kind: Migration\n" +
			"metadata:\n" +
			"  name: mcl-" + linkName + "\n" +
			"spec:\n" +
			"  source:\n" +
			"    type: apache-kafka\n" +
			"    bootstrapServers: [\"localhost:19093\"]\n" +
			"    credentials: " + srcCredsPath + "\n" +
			"  target:\n" +
			"    type: confluent-platform\n" +
			"    credentials: " + targetCredsPath + "\n" +
			"    kafka:\n" +
			"      restEndpoint: " + restDest.baseURL + "\n" +
			"  clusterLink:\n" +
			"    name: " + linkName + "\n" +
			"    mode: destination\n" +
			"    source:\n" +
			"      bootstrapServers: [\"source:29092\"]\n" +
			"      credentials: " + linkCredsPath + "\n"

		manifestPath := filepath.Join(dir, "migration.yaml")
		require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0600))

		poller := newRestClient(t, restDest)
		poller.waitForClusterID(t)
		defer poller.deleteLink(t, destClusterID, linkName)

		// dry-run: source read is triggered during CheckPreconditions; must succeed.
		out, err := runKCP(t, manifestPath, "--dry-run")
		require.NoError(t, err, "dry-run with ca_cert (no skip-verify) must succeed:\n%s", out)
		require.Contains(t, out, "Planned", out)
		require.Empty(t, poller.linkState(destClusterID, linkName), "dry-run must not create the link")

		// apply: link must reach ACTIVE, proving the source read completed.
		out, err = runKCP(t, manifestPath)
		require.NoError(t, err, "apply with ca_cert (no skip-verify) must succeed:\n%s", out)
		require.Contains(t, out, "1 created", out)
		poller.requireLinkActive(t, destClusterID, linkName)

		// re-apply: idempotent.
		out, err = runKCP(t, manifestPath)
		require.NoError(t, err, "re-apply must succeed:\n%s", out)
		require.Contains(t, out, "1 unchanged", out)
	})

	t.Run("negative/no_ca_cert_no_skip_verify", func(t *testing.T) {
		dir := t.TempDir()
		linkName := uniqueLinkName("d1nocert")

		// D1: SCRAM-SHA-512 at the SASL_SSL HOST listener; NO ca_cert, NO
		// insecure_skip_tls_verify. The self-signed test CA is not in the system
		// root store, so TLS verification must fail.
		srcCredsPath := filepath.Join(dir, "source-creds.yaml")
		require.NoError(t, os.WriteFile(srcCredsPath, []byte(
			"sasl_scram: { username: kcp, password: kcp-secret, mechanism: SHA512 }\n",
		), 0600))

		// D2/D3 are valid (plaintext / no-auth) — the failure must come from D1.
		linkCredsPath := filepath.Join(dir, "link-source-creds.yaml")
		writeKafkaCreds(t, linkCredsPath, kafkaAuth{authPlaintext, "source:29092"})

		targetCredsPath := writeRestCreds(t, dir, "target-creds.yaml", restDest)

		manifest := "apiVersion: kcp.confluent.io/v1alpha1\n" +
			"kind: Migration\n" +
			"metadata:\n" +
			"  name: mcl-" + linkName + "\n" +
			"spec:\n" +
			"  source:\n" +
			"    type: apache-kafka\n" +
			"    bootstrapServers: [\"localhost:19093\"]\n" +
			"    credentials: " + srcCredsPath + "\n" +
			"  target:\n" +
			"    type: confluent-platform\n" +
			"    credentials: " + targetCredsPath + "\n" +
			"    kafka:\n" +
			"      restEndpoint: " + restDest.baseURL + "\n" +
			"  clusterLink:\n" +
			"    name: " + linkName + "\n" +
			"    mode: destination\n" +
			"    source:\n" +
			"      bootstrapServers: [\"source:29092\"]\n" +
			"      credentials: " + linkCredsPath + "\n"

		manifestPath := filepath.Join(dir, "migration.yaml")
		require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0600))

		// --dry-run still triggers CheckPreconditions (source read). Must fail.
		out, err := runKCP(t, manifestPath, "--dry-run")
		require.Error(t, err, "apply without ca_cert or skip-verify must fail (self-signed CA not trusted)")
		// The error message must reference the TLS/certificate failure. Sarama wraps
		// the Go TLS error; any of these substrings identify a certificate-chain
		// failure rather than an auth or connectivity problem.
		combined := strings.ToLower(out)
		tlsKeywords := []string{"certificate", "x509", "tls", "handshake", "unknown ca"}
		found := false
		for _, kw := range tlsKeywords {
			if strings.Contains(combined, kw) {
				found = true
				break
			}
		}
		require.True(t, found,
			"error output must mention a TLS/certificate failure (got: %q)", out)

		// No link must have been created (the source read failed before any REST call).
		poller := newRestClient(t, restDest)
		require.Empty(t, poller.linkState(destClusterID, linkName),
			"no link must be created when source TLS verification fails")
	})
}
