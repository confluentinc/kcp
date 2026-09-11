package manifest

import (
	"bytes"
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
	assert.False(t, h.Credentials.IsZero())
}

func TestCredentialsRef_AbsentIsEmpty(t *testing.T) {
	var h refHolder
	require.NoError(t, yaml.UnmarshalWithOptions([]byte("{}\n"), &h, yaml.Strict()))
	assert.True(t, h.Credentials.IsZero())
}

// --- R1: inline credential mappings are no longer schema-legal (any kind) ---

// TestCredentialsRef_MappingFormIsRejected — a credentials slot spelled as a
// YAML mapping (an inline secret block) must fail at parse time rather than be
// silently stored as inline bytes.
func TestCredentialsRef_MappingFormIsRejected(t *testing.T) {
	var h refHolder
	err := yaml.UnmarshalWithOptions(
		[]byte("credentials:\n  sasl_scram:\n    username: admin\n    mechanism: SHA512\n"),
		&h, yaml.Strict())
	require.Error(t, err, "an inline credentials mapping must be rejected")
	assert.Contains(t, err.Error(), "credentials")
}

// TestCredentialsRef_MappingRejection_DoesNotClaimAFieldNamedCredentials —
// the rejection error fires identically for every credentials slot in the
// manifest (source, clusterCredentials, linkCredentials, ...), so it must not
// read as "the field named credentials" via a literal "credentials:" prefix,
// which names the wrong field whenever the actual slot is any of the others.
func TestCredentialsRef_MappingRejection_DoesNotClaimAFieldNamedCredentials(t *testing.T) {
	var h refHolder
	err := yaml.UnmarshalWithOptions(
		[]byte("credentials:\n  sasl_scram:\n    username: admin\n    mechanism: SHA512\n"),
		&h, yaml.Strict())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "credentials:",
		"the message must not assert a specific field name, since it fires for any credentials slot")
}

// TestCredentialsRef_MappingRejectionDoesNotLeakSecret — the rejection error for
// an inline block must not echo any secret value the block contained.
func TestCredentialsRef_MappingRejectionDoesNotLeakSecret(t *testing.T) {
	var h refHolder
	err := yaml.UnmarshalWithOptions(
		[]byte("credentials:\n  sasl_scram:\n    username: admin\n    password: SUPER_SECRET_VALUE\n    mechanism: SHA512\n"),
		&h, yaml.Strict())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SUPER_SECRET_VALUE",
		"a rejected inline block must not render its secret values")
}

// TestMigrationKind_RejectsInlineCredentials pins the repo-wide scope of R1:
// kind: Migration no longer accepts the inline spelling either.
func TestMigrationKind_RejectsInlineCredentials(t *testing.T) {
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
	_, err := Parse([]byte(doc))
	require.Error(t, err, "kind: Migration must also reject inline credentials")
}

// --- file-path resolution (the only supported spelling) ---

// TestCredentialsRef_FileFormResolves — a path-form ref resolves the referenced
// file exactly as before.
func TestCredentialsRef_FileFormResolves(t *testing.T) {
	body := "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA512\n"
	p := filepath.Join(t.TempDir(), "creds.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0600))

	got, errs := parseRef(t, "credentials: "+p+"\n").Credentials.ResolveMigrateCluster()
	require.Empty(t, errs)
	assert.Equal(t, "admin", got.SASLScram.Username)
}

// TestCredentialsRef_FileFormRunsValidation — a missing mechanism in the
// referenced file is rejected, exactly as before.
func TestCredentialsRef_FileFormRunsValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "creds.yaml")
	require.NoError(t, os.WriteFile(p,
		[]byte("sasl_scram:\n  username: admin\n  password: secret\n"), 0600))

	_, errs := parseRef(t, "credentials: "+p+"\n").Credentials.ResolveMigrateCluster()
	require.NotEmpty(t, errs, "a missing mechanism must be rejected")
}

// TestCredentialsRef_EmptyRefRejected — an omitted credentials slot still
// reports "must not be empty" (IsZero behaviour unchanged).
func TestCredentialsRef_EmptyRefRejected(t *testing.T) {
	var ref CredentialsRef
	_, errs := ref.ResolveMigrateCluster()
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "must not be empty")
}

func TestCredentialsRef_ResolveTargetFromFile(t *testing.T) {
	body := "api_key: KEY\napi_secret: SECRET\n"
	p := filepath.Join(t.TempDir(), "rest.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0600))

	got, err := parseRef(t, "credentials: "+p+"\n").Credentials.ResolveTarget()
	require.NoError(t, err)
	assert.Equal(t, "KEY", got.APIKey)
}

// TestCredentialsRef_RoundTripsThroughYAML — a path-form ref survives
// re-marshalling.
func TestCredentialsRef_RoundTripsThroughYAML(t *testing.T) {
	h := parseRef(t, "credentials: /etc/kcp/creds.yaml\n")
	out, err := yaml.Marshal(h)
	require.NoError(t, err)
	assert.Contains(t, string(out), "/etc/kcp/creds.yaml")
}

// TestMigrationKind_StringCredentialsStillParse is the compatibility guard:
// every already-shipped kind: Migration manifest keeps working unchanged.
func TestMigrationKind_StringCredentialsStillParse(t *testing.T) {
	m, err := Parse(readFixture(t, "valid_cc.yaml"))
	require.NoError(t, err)
	require.Empty(t, m.Validate())
	assert.Equal(t, "./source-creds.yaml", m.Spec.Source.Credentials.Path)
}

// --- security review F1: YAML decode errors must not carry a source excerpt ---

// TestCredentialsRef_FileParseErrorDoesNotEchoNeighbouringSecrets covers the
// referenced-file spelling: a decode error must not render a neighbouring
// credential line into err.Error() (and therefore kcp.log).
func TestCredentialsRef_FileParseErrorDoesNotEchoNeighbouringSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "creds.yaml")
	require.NoError(t, os.WriteFile(p,
		[]byte("sasl_scram:\n  username: admin\n  password: FILE_SECRET_VALUE\n  mechanizm: SHA512\n"), 0600))

	_, errs := parseRef(t, "credentials: "+p+"\n").Credentials.ResolveMigrateCluster()
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "mechanizm", "the actionable part must survive")
	assert.NotContains(t, errs[0].Error(), "FILE_SECRET_VALUE")
}

func TestParseCredentials_ParseErrorDoesNotEchoNeighbouringSecrets(t *testing.T) {
	_, err := targets.ParseCredentials([]byte("api_key: KEY\napi_secret: REST_SECRET_VALUE\ntypo_field: x\n"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "REST_SECRET_VALUE")
}

// --- mega-review PR #438 finding #1: the loose-permission warning must name
// what it actually checked, not misattribute a credentials file to the manifest ---

// TestCredentialsRef_ResolveTarget_WarnsOnLooseCredentialsFilePermissions_NamesCredentialsFile
// covers ResolveTarget (the REST leg): a loosely-permissioned credentials file
// must warn as a "credentials file", never as "migration manifest".
func TestCredentialsRef_ResolveTarget_WarnsOnLooseCredentialsFilePermissions_NamesCredentialsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rest.yaml")
	require.NoError(t, os.WriteFile(p, []byte("api_key: KEY\napi_secret: SECRET\n"), 0644))

	var buf bytes.Buffer
	restore := captureSlog(t, &buf)
	_, err := parseRef(t, "credentials: "+p+"\n").Credentials.ResolveTarget()
	restore()

	require.NoError(t, err)
	assert.Contains(t, buf.String(), "credentials file")
	assert.Contains(t, buf.String(), "group- or world-readable")
	assert.NotContains(t, buf.String(), "migration manifest")
}

// TestCredentialsRef_ResolveMigrateCluster_WarnsOnLooseCredentialsFilePermissions_NamesCredentialsFile
// covers ResolveMigrateCluster (the Kafka leg): same rule.
func TestCredentialsRef_ResolveMigrateCluster_WarnsOnLooseCredentialsFilePermissions_NamesCredentialsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kafka.yaml")
	require.NoError(t, os.WriteFile(p,
		[]byte("sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA512\n"), 0644))

	var buf bytes.Buffer
	restore := captureSlog(t, &buf)
	_, errs := parseRef(t, "credentials: "+p+"\n").Credentials.ResolveMigrateCluster()
	restore()

	require.Empty(t, errs)
	assert.Contains(t, buf.String(), "credentials file")
	assert.NotContains(t, buf.String(), "migration manifest")
}
