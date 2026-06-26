package types

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// joinErrStrings concatenates error messages for test assertions.
func joinErrStrings(errs []error) string {
	s := make([]string, len(errs))
	for i, e := range errs {
		s[i] = e.Error()
	}
	return strings.Join(s, "; ")
}

// TestLoadMigrateClusterCredentials_SASLScram verifies top-level sasl_scram (no auth_method wrapper,
// no use: flag) is loaded and the mapped AuthMethodConfig has SASLScram.Use == true.
func TestLoadMigrateClusterCredentials_SASLScram(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(ca, []byte("CA"), 0600))
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"sasl_scram: { username: admin, password: secret, mechanism: SHA256, ca_cert: "+ca+" }\n"), 0600))

	creds, errs := LoadMigrateClusterCredentials(p)
	require.Empty(t, errs)
	require.NotNil(t, creds.SASLScram)
	require.Equal(t, "admin", creds.SASLScram.Username)
	require.Equal(t, "secret", creds.SASLScram.Password)
	require.Equal(t, "SHA256", creds.SASLScram.Mechanism)

	// The mapped AuthMethodConfig (via MigrateConn) must have Use == true
	conn := MigrateConn([]string{"b:9092"}, creds)
	require.NotNil(t, conn.AuthMethod.SASLScram)
	require.True(t, conn.AuthMethod.SASLScram.Use)
	require.Equal(t, "admin", conn.AuthMethod.SASLScram.Username)
	require.Equal(t, "secret", conn.AuthMethod.SASLScram.Password)
	require.Equal(t, "SHA256", conn.AuthMethod.SASLScram.Mechanism)
}

// TestLoadMigrateClusterCredentials_Plaintext verifies that unauthenticated_plaintext: {}
// (presence selection, empty block) is parsed correctly and the mapped config has Use == true.
func TestLoadMigrateClusterCredentials_Plaintext(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte("unauthenticated_plaintext: {}\n"), 0600))

	creds, errs := LoadMigrateClusterCredentials(p)
	require.Empty(t, errs)
	require.NotNil(t, creds.UnauthenticatedPlaintext)

	conn := MigrateConn([]string{"b:9092"}, creds)
	require.NotNil(t, conn.AuthMethod.UnauthenticatedPlaintext)
	require.True(t, conn.AuthMethod.UnauthenticatedPlaintext.Use)
}

// TestLoadMigrateClusterCredentials_TLS verifies mTLS top-level block with real temp cert files.
func TestLoadMigrateClusterCredentials_MTLS(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	cert := filepath.Join(dir, "client.crt")
	key := filepath.Join(dir, "client.key")
	require.NoError(t, os.WriteFile(ca, []byte("CA"), 0600))
	require.NoError(t, os.WriteFile(cert, []byte("CERT"), 0600))
	require.NoError(t, os.WriteFile(key, []byte("KEY"), 0600))

	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"mtls:\n"+
			"  ca_cert: "+ca+"\n"+
			"  client_cert: "+cert+"\n"+
			"  client_key: "+key+"\n"), 0600))

	creds, errs := LoadMigrateClusterCredentials(p)
	require.Empty(t, errs)
	require.NotNil(t, creds.MTLS)

	conn := MigrateConn([]string{"b:9092"}, creds)
	require.NotNil(t, conn.AuthMethod.TLS)
	require.True(t, conn.AuthMethod.TLS.Use)
	require.Equal(t, ca, conn.AuthMethod.TLS.CACert)
	require.Equal(t, cert, conn.AuthMethod.TLS.ClientCert)
	require.Equal(t, key, conn.AuthMethod.TLS.ClientKey)
}

// TestLoadMigrateClusterCredentials_NoMethod verifies that a file with zero auth method blocks
// returns an error about "authentication method".
func TestLoadMigrateClusterCredentials_NoMethod(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte("insecure_skip_tls_verify: false\n"), 0600))

	_, errs := LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "authentication method")
}

// TestLoadMigrateClusterCredentials_TwoMethods verifies that two simultaneous method blocks
// produce an error mentioning "only one".
func TestLoadMigrateClusterCredentials_TwoMethods(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"sasl_plain: { username: u, password: p }\n"+
			"unauthenticated_plaintext: {}\n"), 0600))

	_, errs := LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "only one")
}

// TestLoadMigrateClusterCredentials_RejectsOldAuthMethodWrapper verifies that the old
// auth_method: wrapper format is rejected with a hint that auth is now top-level.
func TestLoadMigrateClusterCredentials_RejectsOldAuthMethodWrapper(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"auth_method:\n  sasl_scram: { use: true, username: admin, password: secret }\n"), 0600))

	_, errs := LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
	msg := joinErrStrings(errs)
	// Hint must indicate top-level, not the old wrapper
	require.Contains(t, msg, "top-level")
}

// TestLoadMigrateClusterCredentials_RejectsOldClustersFormat verifies that a file using the
// OSK scan clusters: list is rejected with a hint about single-cluster format.
func TestLoadMigrateClusterCredentials_RejectsOldClustersFormat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"clusters:\n  - id: c1\n    bootstrap_servers: [\"b:9092\"]\n    sasl_scram: { username: u, password: p }\n"), 0600))

	_, errs := LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "single-cluster")
}

// TestLoadMigrateClusterCredentials_RejectsBootstrapServersInFile verifies that a file with
// bootstrap_servers (which belongs in the manifest) is rejected with a hint about the manifest.
func TestLoadMigrateClusterCredentials_RejectsBootstrapServersInFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"bootstrap_servers: [\"b:9092\"]\n"+
			"unauthenticated_plaintext: {}\n"), 0600))

	_, errs := LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "manifest")
}

func TestLoadMigrateClusterCredentials_IAM(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte("iam: { region: us-east-1 }\n"), 0600))

	creds, errs := LoadMigrateClusterCredentials(p)
	require.Empty(t, errs, joinErrStrings(errs))
	require.NotNil(t, creds.IAM)
	require.Equal(t, "us-east-1", creds.IAM.Region)

	conn := MigrateConn([]string{"b:9092"}, creds)
	require.NotNil(t, conn.AuthMethod.IAM)
	require.True(t, conn.AuthMethod.IAM.Use)
	require.Equal(t, "us-east-1", conn.AuthMethod.IAM.Region)
}

func TestLoadMigrateClusterCredentials_IAM_RegionRequired(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte("iam: {}\n"), 0600))

	_, errs := LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "iam.region is required")
}

// TestMigrateConn verifies MigrateConn composes bootstrap servers + creds into a KafkaSourceConn.
func TestMigrateConn(t *testing.T) {
	bootstrapServers := []string{"b1:9092", "b2:9092"}
	creds := MigrateClusterCredentials{SASLScram: &MigrateSASLScram{Username: "u", Password: "p", Mechanism: "SHA256"}}

	got := MigrateConn(bootstrapServers, creds)
	require.Equal(t, bootstrapServers, got.BootstrapServers)
	require.NotNil(t, got.AuthMethod.SASLScram)
	require.True(t, got.AuthMethod.SASLScram.Use)
}
