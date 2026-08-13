package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/targets"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type refHolder struct {
	Credentials CredentialsRef `yaml:"credentials"`
}

func parseRef(t *testing.T, doc string) refHolder {
	t.Helper()
	var h refHolder
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(doc), &h, yaml.Strict()))
	return h
}

func TestCredentialsRef_StringFormIsAPath(t *testing.T) {
	h := parseRef(t, "credentials: /etc/kcp/source-creds.yaml\n")
	assert.Equal(t, "/etc/kcp/source-creds.yaml", h.Credentials.Path)
	assert.Nil(t, h.Credentials.Inline)
	assert.False(t, h.Credentials.IsInline())
}

func TestCredentialsRef_MappingFormIsInline(t *testing.T) {
	h := parseRef(t, "credentials:\n  sasl_scram:\n    username: admin\n    password: secret\n    mechanism: SHA512\n")
	assert.Empty(t, h.Credentials.Path)
	assert.True(t, h.Credentials.IsInline())
}

func TestCredentialsRef_AbsentIsEmpty(t *testing.T) {
	var h refHolder
	require.NoError(t, yaml.UnmarshalWithOptions([]byte("{}\n"), &h, yaml.Strict()))
	assert.True(t, h.Credentials.IsZero())
}

// TestCredentialsRef_InlineAndFileResolveIdentically is the equivalence test
// §7.2 calls the only thing keeping the two spellings of `credentials:` honest.
func TestCredentialsRef_InlineAndFileResolveIdentically(t *testing.T) {
	body := "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA512\n"
	p := filepath.Join(t.TempDir(), "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0600))

	inline := parseRef(t, "credentials:\n  sasl_scram:\n    username: admin\n    password: secret\n    mechanism: SHA512\n")
	file := parseRef(t, "credentials: "+p+"\n")

	fromInline, errs := inline.Credentials.ResolveMigrateCluster(false)
	require.Empty(t, errs)
	fromFile, errs := file.Credentials.ResolveMigrateCluster(false)
	require.Empty(t, errs)

	assert.Equal(t, fromFile, fromInline)
}

// TestCredentialsRef_InlineIsStrict is the goccy trap from §7.2: Strict() is
// NOT propagated into a custom unmarshaler, so the inner decode must re-apply
// it or inline blocks silently accept typos that files reject.
func TestCredentialsRef_InlineIsStrict(t *testing.T) {
	h := parseRef(t, "credentials:\n  sasl_scram:\n    username: admin\n    passwrod: secret\n")
	_, errs := h.Credentials.ResolveMigrateCluster(false)
	require.NotEmpty(t, errs, "an unknown key inside an inline block must be rejected")
}

// TestCredentialsRef_InlineRunsTheSameValidation — a missing mechanism is
// rejected inline exactly as it is in a file.
func TestCredentialsRef_InlineRunsTheSameValidation(t *testing.T) {
	h := parseRef(t, "credentials:\n  sasl_scram:\n    username: admin\n    password: secret\n")
	_, errs := h.Credentials.ResolveMigrateCluster(false)
	require.NotEmpty(t, errs)
}

// TestCredentialsRef_InlineRejectsInterpolateKey — `interpolate` is a
// file-level key. Allowing it inside an inline block would mean the block is
// resolved twice (once here, once by the manifest's own pass), and a secret
// containing "${...}" would then be expanded on the second pass.
func TestCredentialsRef_InlineRejectsInterpolateKey(t *testing.T) {
	h := parseRef(t, "credentials:\n  interpolate: true\n  unauthenticated_plaintext: {}\n")
	_, errs := h.Credentials.ResolveMigrateCluster(false)
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "interpolate")
}

// TestCredentialsRef_InlineInterpolatesWhenManifestOptsIn — an inline block is
// governed by its manifest's interpolate key, since it has no envelope of its own.
func TestCredentialsRef_InlineInterpolatesWhenManifestOptsIn(t *testing.T) {
	t.Setenv("MSK_PASSWORD", "s3cret")
	h := parseRef(t, "credentials:\n  sasl_scram:\n    username: admin\n    password: ${MSK_PASSWORD}\n    mechanism: SHA512\n")

	got, errs := h.Credentials.ResolveMigrateCluster(true)
	require.Empty(t, errs)
	assert.Equal(t, "s3cret", got.SASLScram.Password)
}

// TestCredentialsRef_ReferencedFileGovernsItself is the scope rule from §10:
// a manifest opting in must not change how a shared kcp migrate credentials
// file is read.
func TestCredentialsRef_ReferencedFileGovernsItself(t *testing.T) {
	t.Setenv("MSK_PASSWORD", "s3cret")
	p := filepath.Join(t.TempDir(), "creds.yaml")
	require.NoError(t, os.WriteFile(p,
		[]byte("sasl_scram:\n  username: admin\n  password: ${MSK_PASSWORD}\n  mechanism: SHA512\n"), 0600))

	h := parseRef(t, "credentials: "+p+"\n")
	got, errs := h.Credentials.ResolveMigrateCluster(true)
	require.Empty(t, errs)
	assert.Equal(t, "${MSK_PASSWORD}", got.SASLScram.Password,
		"the manifest's interpolate must not reach into a referenced file")
}

// TestCredentialsRef_PathIsInterpolatedByTheManifestPass — the path string
// itself is a manifest field, so ${SECRETS_DIR}/creds.yaml resolves.
func TestCredentialsRef_PathIsInterpolatedByTheManifestPass(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "creds.yaml"),
		[]byte("unauthenticated_plaintext: {}\n"), 0600))
	t.Setenv("SECRETS_DIR", dir)

	h := parseRef(t, "credentials: ${SECRETS_DIR}/creds.yaml\n")
	require.NoError(t, interpolateInto(&h))
	assert.Equal(t, filepath.Join(dir, "creds.yaml"), h.Credentials.Path)

	_, errs := h.Credentials.ResolveMigrateCluster(true)
	require.Empty(t, errs)
}

func TestCredentialsRef_ResolveTarget_InlineAndFileIdentical(t *testing.T) {
	body := "api_key: KEY\napi_secret: SECRET\n"
	p := filepath.Join(t.TempDir(), "rest.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0600))

	inline := parseRef(t, "credentials:\n  api_key: KEY\n  api_secret: SECRET\n")
	file := parseRef(t, "credentials: "+p+"\n")

	fromInline, err := inline.Credentials.ResolveTarget(false)
	require.NoError(t, err)
	fromFile, err := file.Credentials.ResolveTarget(false)
	require.NoError(t, err)
	assert.Equal(t, fromFile, fromInline)
}

// TestCredentialsRef_RoundTripsThroughYAML keeps the drift check (§13) honest:
// it compares resolved structs, and a ref must survive re-marshalling.
func TestCredentialsRef_RoundTripsThroughYAML(t *testing.T) {
	h := parseRef(t, "credentials: /etc/kcp/creds.yaml\n")
	out, err := yaml.Marshal(h)
	require.NoError(t, err)
	assert.Contains(t, string(out), "/etc/kcp/creds.yaml")
}

// TestMigrationKind_AcceptsInlineCredentials pins the consequence of widening
// the existing credentials fields to CredentialsRef: kind: Migration picks up
// the inline spelling too. YAML-level compatibility is total (a bare string
// still parses), so this is additive, not a break.
func TestMigrationKind_AcceptsInlineCredentials(t *testing.T) {
	doc := `apiVersion: kcp.confluent.io/v1alpha1
kind: Migration
metadata:
  name: inline-creds
spec:
  source:
    type: apache-kafka
    bootstrapServers: ["broker1:9092"]
    credentials:
      sasl_scram:
        username: admin
        password: secret
        mechanism: SHA512
  target:
    type: confluent-cloud
    clusterId: lkc-xxxxx
    clusterCredentials:
      api_key: KEY
      api_secret: SECRET
    kafka:
      restEndpoint: https://pkc-1n6m13.us-east-1.aws.confluent.cloud:443
`
	m, err := Parse([]byte(doc))
	require.NoError(t, err)
	require.Empty(t, m.Validate())

	require.True(t, m.Spec.Source.Credentials.IsInline())
	creds, errs := m.Spec.Source.Credentials.ResolveMigrateCluster(false)
	require.Empty(t, errs)
	assert.Equal(t, "admin", creds.SASLScram.Username)
}

// TestMigrationKind_StringCredentialsStillParse is the compatibility guard:
// every already-shipped kind: Migration manifest keeps working unchanged.
func TestMigrationKind_StringCredentialsStillParse(t *testing.T) {
	m, err := Parse(readFixture(t, "valid_cc.yaml"))
	require.NoError(t, err)
	require.Empty(t, m.Validate())
	assert.Equal(t, "./source-creds.yaml", m.Spec.Source.Credentials.Path)
	assert.False(t, m.Spec.Source.Credentials.IsInline())
}

// --- security review F1: YAML decode errors must not carry a source excerpt ---

// TestCredentialsRef_InlineParseErrorDoesNotEchoNeighbouringSecrets.
// goccy annotates decode errors with a 3-line excerpt of the source around the
// offending token. Under Strict(), a single typo anywhere near a credential
// renders the neighbouring password line into err.Error(), which main.go feeds
// straight to slog.Error and therefore into kcp.log — a support artefact the
// credentials file is not.
func TestCredentialsRef_InlineParseErrorDoesNotEchoNeighbouringSecrets(t *testing.T) {
	h := parseRef(t, "credentials:\n  sasl_scram:\n    username: admin\n    password: SUPER_SECRET_VALUE\n    mechanizm: SHA512\n")
	_, errs := h.Credentials.ResolveMigrateCluster(false)
	require.NotEmpty(t, errs)

	joined := errs[0].Error()
	assert.Contains(t, joined, "mechanizm", "the actionable part must survive")
	assert.NotContains(t, joined, "SUPER_SECRET_VALUE",
		"a decode error must not render neighbouring credential lines")
}

// TestCredentialsRef_FileParseErrorDoesNotEchoNeighbouringSecrets covers the
// referenced-file spelling, which the docs recommend as the safer one.
func TestCredentialsRef_FileParseErrorDoesNotEchoNeighbouringSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "creds.yaml")
	require.NoError(t, os.WriteFile(p,
		[]byte("sasl_scram:\n  username: admin\n  password: FILE_SECRET_VALUE\n  mechanizm: SHA512\n"), 0600))

	h := parseRef(t, "credentials: "+p+"\n")
	_, errs := h.Credentials.ResolveMigrateCluster(false)
	require.NotEmpty(t, errs)
	assert.NotContains(t, errs[0].Error(), "FILE_SECRET_VALUE")
}

func TestParseCredentials_ParseErrorDoesNotEchoNeighbouringSecrets(t *testing.T) {
	_, err := targets.ParseCredentials([]byte("api_key: KEY\napi_secret: REST_SECRET_VALUE\ntypo_field: x\n"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "REST_SECRET_VALUE")
}
