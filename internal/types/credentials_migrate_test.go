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

// TestLoadMigrateClusterCredentials_SASLPlainTLS verifies a migrate sasl_plain
// creds file with `tls: true` (no ca_cert) loads and maps onto the shared
// SASLPlainConfig with UseTLS == true — the explicit public/system-CA SASL_SSL
// signal (#2). Without the flag, UseTLS defaults to false (SASL_PLAINTEXT).
func TestLoadMigrateClusterCredentials_SASLPlainTLS(t *testing.T) {
	dir := t.TempDir()

	p := filepath.Join(dir, "creds-tls.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"sasl_plain: { username: admin, password: secret, tls: true }\n"), 0600))
	creds, errs := LoadMigrateClusterCredentials(p)
	require.Empty(t, errs)
	require.NotNil(t, creds.SASLPlain)
	require.True(t, creds.SASLPlain.UseTLS)

	conn := MigrateConn([]string{"b:9092"}, creds)
	require.NotNil(t, conn.AuthMethod.SASLPlain)
	require.True(t, conn.AuthMethod.SASLPlain.Use)
	require.True(t, conn.AuthMethod.SASLPlain.UseTLS, "tls: true must map onto SASLPlainConfig.UseTLS")

	// Default (no tls:) → UseTLS false (SASL_PLAINTEXT), behaviour unchanged.
	p2 := filepath.Join(dir, "creds-plain.yaml")
	require.NoError(t, os.WriteFile(p2, []byte(
		"sasl_plain: { username: admin, password: secret }\n"), 0600))
	creds2, errs2 := LoadMigrateClusterCredentials(p2)
	require.Empty(t, errs2)
	require.NotNil(t, creds2.SASLPlain)
	require.False(t, creds2.SASLPlain.UseTLS)
	conn2 := MigrateConn([]string{"b:9092"}, creds2)
	require.False(t, conn2.AuthMethod.SASLPlain.UseTLS)
}

// TestLoadMigrateClusterCredentials_SCRAMMechanismRequired verifies migrate creds
// require an explicit, valid SCRAM mechanism (no silent SHA256 default — that is
// wrong for MSK, which is SHA-512-only).
func TestLoadMigrateClusterCredentials_SCRAMMechanismRequired(t *testing.T) {
	dir := t.TempDir()
	load := func(body string) []error {
		p := filepath.Join(dir, "c.yaml")
		require.NoError(t, os.WriteFile(p, []byte(body), 0600))
		_, errs := LoadMigrateClusterCredentials(p)
		return errs
	}
	hasMechErr := func(errs []error) bool {
		for _, e := range errs {
			if strings.Contains(e.Error(), "mechanism") {
				return true
			}
		}
		return false
	}
	require.True(t, hasMechErr(load("sasl_scram: { username: u, password: p }\n")), "missing mechanism must be rejected")
	require.True(t, hasMechErr(load("sasl_scram: { username: u, password: p, mechanism: MD5 }\n")), "invalid mechanism must be rejected")
	require.Empty(t, load("sasl_scram: { username: u, password: p, mechanism: SHA512 }\n"), "SHA512 is valid")
	require.Empty(t, load("sasl_scram: { username: u, password: p, mechanism: SHA256 }\n"), "SHA256 is valid")
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

// TestMigrateConn_NilBootstrapServers_AuthMappingUnaffected. Every existing
// MigrateConn call site passes real bootstrap addresses; buildExecutorOpts
// resolving the migration destination auth via MigrateConn(nil, dstCreds) is a
// new usage shape. Bootstrap servers must not affect the auth-type mapping —
// proven here rather than assumed from precedent.
func TestMigrateConn_NilBootstrapServers_AuthMappingUnaffected(t *testing.T) {
	creds := MigrateClusterCredentials{SASLPlain: &MigrateSASLPlain{Username: "u", Password: "p", UseTLS: true}}

	got := MigrateConn(nil, creds)
	require.Nil(t, got.BootstrapServers)
	require.NotNil(t, got.AuthMethod.SASLPlain)
	require.True(t, got.AuthMethod.SASLPlain.Use)
	require.True(t, got.AuthMethod.SASLPlain.UseTLS)

	authType, err := got.GetSelectedAuthType()
	require.NoError(t, err)
	require.Equal(t, AuthTypeSASLPlain, authType)
}

// writeMigrateCreds writes a migrate credentials file and returns its path.
func writeMigrateCreds(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0600))
	return p
}

// --- ${ENV_VAR} interpolation (opt-in, per file) ---

func TestLoadMigrateClusterCredentials_InterpolatesWhenOptedIn(t *testing.T) {
	t.Setenv("MSK_USERNAME", "admin")
	t.Setenv("MSK_PASSWORD", "s3cret")
	creds, errs := LoadMigrateClusterCredentials(writeMigrateCreds(t,
		"interpolate: true\nsasl_scram:\n  username: ${MSK_USERNAME}\n  password: ${MSK_PASSWORD}\n  mechanism: SHA512\n"))
	require.Empty(t, errs)
	require.NotNil(t, creds.SASLScram)
	require.Equal(t, "admin", creds.SASLScram.Username)
	require.Equal(t, "s3cret", creds.SASLScram.Password)
}

// TestLoadMigrateClusterCredentials_NoInterpolationByDefault pins that every
// already-shipped kcp migrate credentials file is read byte-for-byte as before.
func TestLoadMigrateClusterCredentials_NoInterpolationByDefault(t *testing.T) {
	t.Setenv("MSK_PASSWORD", "s3cret")
	creds, errs := LoadMigrateClusterCredentials(writeMigrateCreds(t,
		"sasl_scram:\n  username: admin\n  password: ${MSK_PASSWORD}\n  mechanism: SHA512\n"))
	require.Empty(t, errs)
	require.Equal(t, "${MSK_PASSWORD}", creds.SASLScram.Password)
}

func TestLoadMigrateClusterCredentials_InterpolateUndefinedVariableFails(t *testing.T) {
	t.Setenv("MSK_USERNAME", "admin-name")
	_, errs := LoadMigrateClusterCredentials(writeMigrateCreds(t,
		"interpolate: true\nsasl_scram:\n  username: ${MSK_USERNAME}\n  password: ${KCP_UNSET_PW}\n  mechanism: SHA512\n"))
	require.NotEmpty(t, errs)
	joined := joinErrStrings(errs)
	require.Contains(t, joined, "KCP_UNSET_PW")
	require.NotContains(t, joined, "admin-name", "a resolved value must never reach an error string")
}

// TestLoadMigrateClusterCredentials_InterpolatesBeforeValidation — the loader
// rejects an unrecognised sasl_scram.mechanism, so resolution placed after
// validation would reject the literal "${MECH}".
func TestLoadMigrateClusterCredentials_InterpolatesBeforeValidation(t *testing.T) {
	t.Setenv("MECH", "SHA512")
	creds, errs := LoadMigrateClusterCredentials(writeMigrateCreds(t,
		"interpolate: true\nsasl_scram:\n  username: u\n  password: p\n  mechanism: ${MECH}\n"))
	require.Empty(t, errs)
	require.Equal(t, "SHA512", creds.SASLScram.Mechanism)
}

// TestLoadMigrateClusterCredentials_MechanismErrorDoesNotEchoValue is the §10
// logging hazard: an invalid mechanism must not echo the resolved value, which
// on a mis-set variable is somebody else's secret.
func TestLoadMigrateClusterCredentials_MechanismErrorDoesNotEchoValue(t *testing.T) {
	t.Setenv("MECH", "super-secret-leak")
	_, errs := LoadMigrateClusterCredentials(writeMigrateCreds(t,
		"interpolate: true\nsasl_scram:\n  username: u\n  password: p\n  mechanism: ${MECH}\n"))
	require.NotEmpty(t, errs)
	require.NotContains(t, joinErrStrings(errs), "super-secret-leak")
}

// TestLoadMigrateClusterCredentials_InterpolatedValueIsNotReparsed proves the
// post-parse design: a password carrying YAML structure cannot inject a key.
func TestLoadMigrateClusterCredentials_InterpolatedValueIsNotReparsed(t *testing.T) {
	t.Setenv("EVIL", "p\nusername: attacker")
	creds, errs := LoadMigrateClusterCredentials(writeMigrateCreds(t,
		"interpolate: true\nsasl_scram:\n  username: real-user\n  password: ${EVIL}\n  mechanism: SHA512\n"))
	require.Empty(t, errs)
	require.Equal(t, "real-user", creds.SASLScram.Username)
	require.Equal(t, "p\nusername: attacker", creds.SASLScram.Password)
}

// --- parse / validate split ---

// TestParseMigrateClusterCredentials_AppliesSameValidationAsFile is the point
// of the split: an inline block and a referenced file run identical validation.
func TestParseMigrateClusterCredentials_AppliesSameValidationAsFile(t *testing.T) {
	body := "sasl_scram: { username: u, password: p }\n"
	_, parseErrs := ParseMigrateClusterCredentials([]byte(body))
	_, loadErrs := LoadMigrateClusterCredentials(writeMigrateCreds(t, body))
	require.NotEmpty(t, parseErrs)
	require.Equal(t, joinErrStrings(loadErrs), joinErrStrings(parseErrs))
}

func TestParseMigrateClusterCredentials_RejectsUnknownFields(t *testing.T) {
	_, errs := ParseMigrateClusterCredentials([]byte("unauthenticated_plaintext: {}\ntypo_field: x\n"))
	require.NotEmpty(t, errs)
}

// TestValidateMigrateClusterCredentials_IsReusableOnAnAlreadyBuiltStruct lets
// the manifest path validate an inline block it assembled itself.
func TestValidateMigrateClusterCredentials_IsReusableOnAnAlreadyBuiltStruct(t *testing.T) {
	ok := MigrateClusterCredentials{UnauthenticatedPlaintext: &MigrateUnauthenticatedPlaintext{}}
	require.Empty(t, ValidateMigrateClusterCredentials(ok))
	require.NotEmpty(t, ValidateMigrateClusterCredentials(MigrateClusterCredentials{}))
}

// TestParseMigrateClusterCredentials_HintsDoNotMatchOnFileContent.
// The old-format hints are chosen by substring-matching the decode error. If
// that error still carries the source excerpt, the match runs against the
// FILE'S CONTENT rather than the parser's message — so a password containing
// "clusters" selects the wrong hint. Not a leak, but a secret-content-dependent
// branch, and the hint an operator pastes into a ticket becomes a weak oracle
// over the file.
func TestParseMigrateClusterCredentials_HintsDoNotMatchOnFileContent(t *testing.T) {
	_, errs := ParseMigrateClusterCredentials([]byte(
		"sasl_scram:\n  username: admin\n  password: my-clusters-passw0rd\n  mechanizm: SHA512\n"))
	require.NotEmpty(t, errs)

	joined := joinErrStrings(errs)
	require.Contains(t, joined, "mechanizm", "the real problem must be reported")
	require.NotContains(t, joined, "single-cluster format",
		"the scan-format hint must not fire because the PASSWORD contains 'clusters'")
	require.NotContains(t, joined, "my-clusters-passw0rd")
}

// TestParseMigrateClusterCredentials_RealHintsStillFire — stripping the excerpt
// must not cost the three genuine hints, whose trigger keys survive it.
func TestParseMigrateClusterCredentials_RealHintsStillFire(t *testing.T) {
	_, errs := ParseMigrateClusterCredentials([]byte("clusters:\n  - id: a\n"))
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "single-cluster format")

	_, errs = ParseMigrateClusterCredentials([]byte("auth_method:\n  sasl_scram: {}\n"))
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "top-level")

	_, errs = ParseMigrateClusterCredentials([]byte("bootstrap_servers:\n  - b:9092\n"))
	require.NotEmpty(t, errs)
	require.Contains(t, joinErrStrings(errs), "belong in the manifest")
}
