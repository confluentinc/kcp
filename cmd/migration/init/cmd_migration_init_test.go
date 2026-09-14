package init

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/confluentinc/kcp/internal/testsupport"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatewayManifest is a complete, valid GatewayMigration document. Tests write a
// mutated copy of it to a temp file. spec.gateway.kubeconfig is left unset, so
// it resolves to ~/.kube/config — a path that does not point at a real cluster
// in a test process, which is what makes the non-skip-validate path fail
// fast and deterministically inside migplan.Reconcile's own live gateway pull
// (see TestInit_NonSkipValidate_CallsReconcile below), the same technique
// cmd/migration/execute's own tests use for the identical call.
const gatewayManifest = `apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: msk-prod-to-cc-batch-1
spec:
  source:
    type: msk
    bootstrapServers:
      - b-1.msk.us-east-1.amazonaws.com:9096
    credentials: SOURCE_CREDS_PATH
  target:
    type: confluent-cloud
    clusterId: lkc-abc123
    kafka:
      bootstrapServers:
        - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
      restEndpoint: https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443
      clusterCredentials: DEST_KAFKA_CREDS_PATH
  clusterLink:
    name: msk-to-cc
    bootstrapServers:
      - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
    linkCredentials: LINK_CREDS_PATH
  gateway:
    namespace: confluent
    cr-name: gateway-initial
  topicGroup:
    - topics:
        - t1.order
        - t2.inventory
      route: migration-route
      targetStreamingDomain: confluent-cloud
`

// Default credentials-file bodies the canonical manifest references. Credentials
// are files now, not inline blocks, so the secret material lives here while the
// manifest carries only paths.
const (
	defaultSourceCreds   = "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: SHA512\n"
	defaultDestKafkaCred = "sasl_plain:\n  username: CC_KEY\n  password: CC_SECRET\n  tls: true\n"
	defaultLinkCred      = "api_key: CC_KEY\napi_secret: CC_SECRET\n"
)

// credOverrides lets a test vary a credentials-FILE body; empty fields use the
// defaults above.
type credOverrides struct {
	source, destKafka, link string
}

// writeManifest writes the manifest (with default credentials files) and returns
// its path.
func writeManifest(t *testing.T, mutate func(string) string) string {
	return writeManifestCreds(t, mutate, credOverrides{})
}

// writeManifestCreds writes the three referenced credentials files into a temp
// dir (using the given bodies, or the defaults for empty ones), substitutes their
// absolute paths into the manifest, applies mutate, and writes the manifest.
func writeManifestCreds(t *testing.T, mutate func(string) string, creds credOverrides) string {
	t.Helper()
	dir := t.TempDir()

	doc := gatewayManifest
	doc = strings.ReplaceAll(doc, "SOURCE_CREDS_PATH", testsupport.WriteCredFile(t, dir, "source-creds.yaml", creds.source, defaultSourceCreds))
	doc = strings.ReplaceAll(doc, "DEST_KAFKA_CREDS_PATH", testsupport.WriteCredFile(t, dir, "dest-kafka-creds.yaml", creds.destKafka, defaultDestKafkaCred))
	doc = strings.ReplaceAll(doc, "LINK_CREDS_PATH", testsupport.WriteCredFile(t, dir, "link-creds.yaml", creds.link, defaultLinkCred))
	if mutate != nil {
		doc = mutate(doc)
	}
	p := filepath.Join(dir, "gateway-migration.yaml")
	require.NoError(t, os.WriteFile(p, []byte(doc), 0600))
	return p
}

// runInit executes the command with the given args, capturing output.
func runInit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewMigrationInitCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// --- flag surface ---

// TestInit_FlagSurfaceIsThreeFlags is the headline of the change: 31 flags
// become 3, and nothing describing the migration itself remains a flag.
func TestInit_FlagSurfaceIsThreeFlags(t *testing.T) {
	cmd := NewMigrationInitCmd()
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	assert.ElementsMatch(t, []string{"migration-yaml", "migration-state-file", "skip-validate"}, names)
}

func TestInit_RequiresMigrationYaml(t *testing.T) {
	_, err := runInit(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-yaml")
}

// TestInit_RetiredFlagsAreGone — the retired flags must fail loudly rather
// than being silently accepted and ignored.
func TestInit_RetiredFlagsAreGone(t *testing.T) {
	for _, flag := range []string{
		"--source-bootstrap", "--cluster-bootstrap", "--cluster-id",
		"--cluster-rest-endpoint", "--cluster-link-name", "--cluster-api-key",
		"--cluster-api-secret", "--k8s-namespace", "--initial-cr-name",
		"--fenced-cr-yaml", "--switchover-cr-yaml", "--use-sasl-iam",
		"--use-sasl-scram", "--sasl-scram-username", "--topics",
		"--pause-consumer-offset-sync", "--kube-path", "--insecure-skip-tls-verify",
		"--cluster-rest-ca-cert", "--tls-ca-cert",
	} {
		t.Run(flag, func(t *testing.T) {
			_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), flag, "x")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown flag")
		})
	}
}

// --- manifest → config (manifest-only fields; no live call under --skip-validate) ---

func TestInit_WritesConfigFromManifest(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifest(t, nil)

	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)

	assert.Equal(t, "b-1.msk.us-east-1.amazonaws.com:9096", cfg.SourceBootstrap)
	assert.Equal(t, "pkc-xxxxx.us-east-1.aws.confluent.cloud:9092", cfg.ClusterBootstrap)
	assert.Equal(t, "lkc-abc123", cfg.ClusterId)
	assert.Equal(t, "https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443", cfg.ClusterRestEndpoint)
	assert.Equal(t, "msk-to-cc", cfg.ClusterLinkName)
	assert.Equal(t, "confluent", cfg.K8sNamespace)
	assert.Equal(t, "gateway-initial", cfg.InitialCrName)
	assert.Equal(t, migration.StateUninitialized, cfg.CurrentState)
	assert.False(t, cfg.PauseConsumerOffsetSync)
}

// TestInit_MetadataNameIsTheMigrationId is decision 8: the name in the file is
// the migration identity, written into the existing migration_id field.
func TestInit_MetadataNameIsTheMigrationId(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.Equal(t, "msk-prod-to-cc-batch-1", cfg.MigrationId)
}

// TestInit_JoinsMultipleBootstrapServers — the manifest takes a list, the state
// file keeps the comma-joined string it always held.
func TestInit_JoinsMultipleBootstrapServers(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifest(t, func(doc string) string {
		return strings.Replace(doc, "      - b-1.msk.us-east-1.amazonaws.com:9096",
			"      - b-1.msk.us-east-1.amazonaws.com:9096\n      - b-2.msk.us-east-1.amazonaws.com:9096", 1)
	})
	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.Equal(t, "b-1.msk.us-east-1.amazonaws.com:9096,b-2.msk.us-east-1.amazonaws.com:9096", cfg.SourceBootstrap)
}

// TestInit_PersistsRouteAndTargetDomain — Route and TargetDomain are pure
// manifest data (spec.topicGroup[0].route / targetStreamingDomain), captured
// at Phase 2 without any live call. Everything migplan.Reconcile itself
// derives (Topics/FenceYAML/SwitchoverYAML/GatewayYAML/Mode) is NOT set here —
// see TestInit_SkipValidateLeavesReconcileDerivedFieldsEmpty.
func TestInit_PersistsRouteAndTargetDomain(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.Equal(t, "migration-route", cfg.Route)
	assert.Equal(t, "confluent-cloud", cfg.TargetDomain)
}

// TestInit_SkipValidateLeavesReconcileDerivedFieldsEmpty is Decision 10:
// --skip-validate makes zero live calls, so migplan.Reconcile never runs and
// none of the fields it alone derives are populated. A migration registered
// this way carries Route/TargetDomain (manifest-only, always known) but empty
// Topics/FenceYAML/SwitchoverYAML/GatewayYAML/Mode until 'kcp migration
// execute' completes initialization later (the StateUninitialized-resume
// path).
func TestInit_SkipValidateLeavesReconcileDerivedFieldsEmpty(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.Empty(t, cfg.Topics)
	assert.Empty(t, cfg.FenceYAML)
	assert.Empty(t, cfg.SwitchoverYAML)
	assert.Empty(t, cfg.GatewayYAML)
	assert.Empty(t, cfg.Mode)
}

// TestInit_PersistsGatewayConfigPort — the init-time capability probe dials the
// gateway /config endpoint on this port; if the manifest's value is not copied
// into the persisted config, the probe falls back to the hardcoded default and
// init fails on any non-default port that execute would resolve correctly.
func TestInit_PersistsGatewayConfigPort(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifest(t, func(doc string) string {
		return doc + `  defaultPolicies:
    gatewayConfigPort: 9099
`
	})
	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.Equal(t, 9099, cfg.GatewayConfigPort)
}

// --- migplan.Reconcile: called once, only on the non-skip-validate path ---
//
// Neither test can reach a successful Reconcile — there is no live Kubernetes
// cluster in this test process (spec.gateway.kubeconfig resolves to
// ~/.kube/config, and the manifest never overrides it to point at a working
// cluster). What distinguishes the two cases is WHETHER migplan.Reconcile is
// reached at all, mirroring cmd/migration/execute's own
// TestExecute_ResumeFrom{Uninitialized,Initialized}_*CallsReconcile tests for
// the identical call.

// TestInit_NonSkipValidate_CallsReconcile proves the non-skip-validate path
// reaches migplan.Reconcile: Reconcile's own live gateway pull fails
// deterministically (no reachable cluster), surfacing through
// runMigrationInit's "failed to produce the reconcile plan" wrap.
func TestInit_NonSkipValidate_CallsReconcile(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to produce the reconcile plan")
}

// TestInit_SkipValidate_NeverCallsReconcile is the converse: --skip-validate
// succeeds even though the same unreachable kubeconfig would make
// migplan.Reconcile fail immediately — proof the call is never made. Every
// other --skip-validate test in this file makes the same point implicitly;
// this one names it.
func TestInit_SkipValidate_NeverCallsReconcile(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err, "--skip-validate must make zero live calls")
}

func TestInit_KubeconfigDefaultsToHomeDir(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".kube", "config"), cfg.KubeConfigPath)
}

func TestInit_PauseConsumerOffsetSyncComesFromManifest(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifest(t, func(doc string) string {
		return strings.Replace(doc, "    name: msk-to-cc",
			"    name: msk-to-cc\n    pauseConsumerOffsetSync: true", 1)
	})
	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.True(t, cfg.PauseConsumerOffsetSync)
}

// --- validation ---

func TestInit_RejectsInvalidManifest(t *testing.T) {
	manifest := writeManifest(t, func(doc string) string {
		return strings.Replace(doc, "    name: msk-to-cc", "    name: \"\"", 1)
	})
	_, err := runInit(t, "--migration-yaml", manifest, "--skip-validate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec.clusterLink.name")
}

// TestInit_RejectsMigrationKindManifest — a kcp migrate manifest must be
// refused with a message that names the problem.
func TestInit_RejectsMigrationKindManifest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "migration.yaml")
	require.NoError(t, os.WriteFile(p, []byte(
		"apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: x\n"), 0600))
	_, err := runInit(t, "--migration-yaml", p, "--skip-validate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GatewayMigration")
}

func TestInit_RejectsMissingFile(t *testing.T) {
	_, err := runInit(t, "--migration-yaml", filepath.Join(t.TempDir(), "nope.yaml"), "--skip-validate")
	require.Error(t, err)
}

// TestInit_ValidatesSourceCredentials closes half of defect 1: source auth is
// now declared once, and init actually validates it rather than only marking
// flags required. No --skip-validate here: checkCredentialsResolve runs in
// Phase 5, before migplan.Reconcile, so this error surfaces locally, before
// any gateway/K8s contact is even attempted — see
// TestInit_SkipValidateSkipsCredentialResolution for the --skip-validate
// counterpart.
func TestInit_RejectsInvalidSourceCredentials(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifestCreds(t, nil, credOverrides{
		source: "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: NOPE\n",
	})
	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "mechanism")
}

// TestInit_RejectsIAMOnApacheKafkaSource — the §2.4 gating reaches the
// command. No --skip-validate: see TestInit_RejectsInvalidSourceCredentials.
func TestInit_RejectsIAMOnApacheKafkaSource(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifestCreds(t, func(doc string) string {
		return strings.Replace(doc, "    type: msk", "    type: apache-kafka", 1)
	}, credOverrides{source: "iam:\n  region: us-east-1\n"})
	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iam")
}

// TestInit_SkipValidateSkipsCredentialResolution — --skip-validate defers
// checkCredentialsResolve (Phase 5) along with the rest of validation, so
// invalid source credentials are no longer caught at init. This is a
// deliberate symmetry with the destination leg, which --skip-validate has
// always exempted from eager resolution (RestCredentials() is Phase-5-only
// too): neither leg is singled out, which matters as both grow more auth
// methods (e.g. mTLS certs) that may need local file access to resolve.
func TestInit_SkipValidateSkipsCredentialResolution(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifestCreds(t, nil, credOverrides{
		source: "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: NOPE\n",
	})
	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err, "credential resolution is deferred, not performed, under --skip-validate")
}

// --- decision 14: the mutual exclusion is dropped, replaced by a warning ---

// TestInit_SkipValidateWithPauseOffsetSyncIsAllowed: a cobra flag cannot be
// grouped with a YAML field, so the constraint became inexpressible. It was not
// safety-bearing — the snapshot is taken unconditionally by the Initialize FSM
// step, two steps before PauseOffsetSync can run.
func TestInit_SkipValidateWithPauseOffsetSyncIsAllowed(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifest(t, func(doc string) string {
		return strings.Replace(doc, "    name: msk-to-cc",
			"    name: msk-to-cc\n    pauseConsumerOffsetSync: true", 1)
	})
	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err, "the combination is accepted, not rejected")

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.True(t, cfg.PauseConsumerOffsetSync, "the setting survives skip-validate")
}

// --- re-init safety (a consequence of decision 8) ---

// TestInit_RefusesToOverwriteAMidFlightMigration. With a generated uuid every
// init created a NEW row, so an overwrite was impossible. Keying on
// metadata.name makes re-running init an upsert — which mid-cutover would
// discard current_state and cluster_link_configs, stranding a resume.
func TestInit_RefusesToOverwriteAMidFlightMigration(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifest(t, nil)

	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	// Advance the persisted migration to a state with irreversible work behind it.
	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	cfg.CurrentState = migration.StateFenced
	state.UpsertMigration(*cfg)
	require.NoError(t, state.WriteToFile(stateFile))

	_, err = runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fenced")

	// The persisted state must be untouched.
	after, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfgAfter, err := after.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.Equal(t, migration.StateFenced, cfgAfter.CurrentState)
}

// TestInit_ReRunIsAllowedBeforeAnythingIrreversible — §13 tells the operator to
// "re-run init" to adopt an edited spec, so that path must work.
func TestInit_ReRunIsAllowedBeforeAnythingIrreversible(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifest(t, nil)

	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)
	_, err = runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	assert.Len(t, state.Migrations, 1, "re-running init must update the row, not add a second")
}

// --- credential persistence boundary ---

// TestInit_NeverPersistsCredentials is the highest-value guard in the change:
// inlining credentials puts them one struct-copy away from the state file.
func TestInit_NeverPersistsCredentials(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	raw, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	for _, secret := range []string{"secret", "CC_SECRET", "CC_KEY", "admin"} {
		assert.NotContains(t, string(raw), secret,
			"credentials must never reach migration-state.json")
	}
}

// TestInit_StateFileIsNotWorldReadable — the state file is written next to a
// secret-bearing manifest and inherits the same handling expectations.
func TestInit_StateFilePermissions(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	info, err := os.Stat(stateFile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

// TestInit_MidFlightRefusalDoesNotDependOnCredentials: the re-init guard is a
// safety check, so it must be reachable even when credentials cannot resolve.
// An operator re-running init mid-cutover needs to hear "this migration is
// fenced", not "your environment variable is unset" — the first is the thing
// that will hurt them.
func TestInit_MidFlightRefusalDoesNotDependOnCredentials(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	// A source credentials file that would fail to resolve (bad mechanism), so the
	// safety refusal must fire before — and instead of — any credential error.
	manifest := writeManifestCreds(t, nil, credOverrides{
		source: "sasl_scram:\n  username: admin\n  password: secret\n  mechanism: NOPE\n",
	})

	// Register the migration, then advance it past the point of no return.
	state := migration.NewMigrationState()
	state.UpsertMigration(migration.MigrationConfig{
		MigrationId:  "msk-prod-to-cc-batch-1",
		CurrentState: migration.StateFenced,
	})
	require.NoError(t, state.WriteToFile(stateFile))

	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fenced")
	assert.NotContains(t, strings.ToLower(err.Error()), "mechanism",
		"the credentials error must not mask the safety refusal")
}
