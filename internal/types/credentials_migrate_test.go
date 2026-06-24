package types

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadMigrateClusterCredentials_SASLScram(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(ca, []byte("CA"), 0600))
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"bootstrap_servers: [\"broker1:9092\", \"broker2:9092\"]\n"+
			"auth_method:\n"+
			"  sasl_scram: { use: true, username: admin, password: secret, mechanism: SHA256, ca_cert: "+ca+" }\n"), 0600))

	cluster, errs := LoadMigrateClusterCredentials(p)
	require.Empty(t, errs)
	require.Equal(t, []string{"broker1:9092", "broker2:9092"}, cluster.BootstrapServers)
	require.NotNil(t, cluster.AuthMethod.SASLScram)
	require.True(t, cluster.AuthMethod.SASLScram.Use)
	require.Equal(t, "admin", cluster.AuthMethod.SASLScram.Username)
	require.Empty(t, cluster.ID, "migrate creds carry no id")
}

func TestLoadMigrateClusterCredentials_Plaintext(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"bootstrap_servers: [\"b:9092\"]\nauth_method: { unauthenticated_plaintext: { use: true } }\n"), 0600))
	cluster, errs := LoadMigrateClusterCredentials(p)
	require.Empty(t, errs)
	require.True(t, cluster.AuthMethod.UnauthenticatedPlaintext.Use)
}

func TestLoadMigrateClusterCredentials_NoBootstrap(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte("auth_method: { unauthenticated_plaintext: { use: true } }\n"), 0600))
	_, errs := LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "bootstrap")
}

func TestLoadMigrateClusterCredentials_NoAuthMethod(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte("bootstrap_servers: [\"b:9092\"]\nauth_method: {}\n"), 0600))
	_, errs := LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "authentication method")
}

func TestLoadMigrateClusterCredentials_MultipleAuthMethods(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"bootstrap_servers: [\"b:9092\"]\nauth_method:\n  unauthenticated_plaintext: { use: true }\n  sasl_plain: { use: true, username: u, password: p }\n"), 0600))
	_, errs := LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "only one")
}

func TestLoadMigrateClusterCredentials_RejectsOldClustersFormat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"clusters:\n  - id: c1\n    bootstrap_servers: [\"b:9092\"]\n    auth_method: { unauthenticated_plaintext: { use: true } }\n"), 0600))
	_, errs := LoadMigrateClusterCredentials(p)
	require.NotEmpty(t, errs)
	// helpful hint pointing at the single-cluster format
	require.Contains(t, joinErrStrings(errs), "single-cluster")
}

// joinErrStrings concatenates error messages for test assertions.
func joinErrStrings(errs []error) string {
	s := make([]string, len(errs))
	for i, e := range errs {
		s[i] = e.Error()
	}
	return strings.Join(s, "; ")
}
