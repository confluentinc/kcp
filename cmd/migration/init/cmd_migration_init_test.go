package init

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migration"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatewayManifest is a complete, valid GatewayMigration document. Tests write a
// mutated copy of it to a temp file. The CR paths are filled in per test,
// because init reads them.
const gatewayManifest = `apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: msk-prod-to-cc-batch-1
spec:
  source:
    type: msk
    bootstrapServers:
      - b-1.msk.us-east-1.amazonaws.com:9096
    credentials:
      sasl_scram:
        username: admin
        password: secret
        mechanism: SHA512
  target:
    type: confluent-cloud
    clusterId: lkc-abc123
    kafka:
      bootstrapServers:
        - pkc-xxxxx.us-east-1.aws.confluent.cloud:9092
      restEndpoint: https://pkc-xxxxx.us-east-1.aws.confluent.cloud:443
      credentials:
        sasl_plain:
          username: CC_KEY
          password: CC_SECRET
          tls: true
  clusterLink:
    name: msk-to-cc
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

// defaultFixtureCR is the live initial CR the early derivation reads (D1c). It
// declares the domain the manifest targets — confluent-cloud, single-homed with
// id SASL_PLAIN — and the static route migration-route (singular streamingDomain
// binding). TestMain installs it for every test; a test needing a different CR
// calls stubCR first.
const defaultFixtureCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gateway-initial
spec:
  streamingDomains:
    - name: confluent-cloud
      kafkaCluster:
        bootstrapServers:
          - id: SASL_PLAIN
  routes:
    - name: migration-route
      streamingDomain:
        name: source-domain
        bootstrapServerId: SOURCE_ID
`

func TestMain(m *testing.M) {
	// Every init success path now reads the initial CR early (D1c). Default all
	// tests to the working single-homed fixture; tests needing a different CR
	// (or a fetch error) override it via stubCR.
	fetchInitialCR = func(_ context.Context, _, _, _ string) ([]byte, error) {
		return []byte(defaultFixtureCR), nil
	}
	os.Exit(m.Run())
}

// stubCR installs crYAML as the CR the early derivation reads, restoring the
// previous fetch on cleanup.
func stubCR(t *testing.T, crYAML string) {
	t.Helper()
	prev := fetchInitialCR
	fetchInitialCR = func(_ context.Context, _, _, _ string) ([]byte, error) {
		return []byte(crYAML), nil
	}
	t.Cleanup(func() { fetchInitialCR = prev })
}

// stubCRError makes the early CR read fail, restoring the previous fetch on
// cleanup — for proving --skip-validate still performs the read.
func stubCRError(t *testing.T, err error) {
	t.Helper()
	prev := fetchInitialCR
	fetchInitialCR = func(_ context.Context, _, _, _ string) ([]byte, error) {
		return nil, err
	}
	t.Cleanup(func() { fetchInitialCR = prev })
}

// defaultTopicsBlock is the topics selection in the canonical manifest's
// topicGroup entry; tests replace it to vary the selection.
const defaultTopicsBlock = "    - topics:\n        - t1.order\n        - t2.inventory\n"

// matchAllTopicPatterns swaps the literal topics for a match-all topicPatterns
// entry (O4).
func matchAllTopicPatterns(doc string) string {
	return strings.Replace(doc, defaultTopicsBlock, "    - topicPatterns:\n        - '.*'\n", 1)
}

// writeManifest writes the manifest and returns its path. There is no fenced
// or switchover CR file — both are derived from the live initial CR at
// cutover.
func writeManifest(t *testing.T, mutate func(string) string) string {
	t.Helper()
	dir := t.TempDir()

	doc := gatewayManifest
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

// --- manifest → state ---

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

func TestInit_PersistsFenceRoutesAndSwitchoverTargets(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	// Both are derived from the live initial CR at cutover rather than a file:
	// the fence is recorded as route names, and each route's switchover
	// target is projected from the manifest's routes[].streamingDomain.
	assert.Equal(t, []string{"migration-route"}, cfg.FenceRoutes)
	assert.Equal(t, []gateway.RouteSwitchoverTarget{
		{RouteName: "migration-route", StreamingDomainName: "confluent-cloud", BootstrapServerId: "SASL_PLAIN"},
	}, cfg.SwitchoverTargets)
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

func TestInit_TopicsCarryThrough(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"t1.order", "t2.inventory"}, cfg.Topics)
}

// TestInit_MatchAllPatternMeansEveryMirror — a static match-all topicPatterns
// (.*) leaves Topics empty (O4), which the Initialize step back-fills with every
// active mirror topic — the same back-fill the removed omit-topics sentinel used.
func TestInit_MatchAllPatternMeansEveryMirror(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifest(t, matchAllTopicPatterns)
	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	assert.Empty(t, cfg.Topics, "a match-all pattern leaves Topics empty for the FSM to back-fill")
}

// TestInit_NonMatchAllStaticPatternIsNotYetSupported — O4: only the match-all
// pattern is expanded on a static route this piece; any other pattern errors.
func TestInit_NonMatchAllStaticPatternIsNotYetSupported(t *testing.T) {
	manifest := writeManifest(t, func(doc string) string {
		return strings.Replace(doc, defaultTopicsBlock,
			"    - topicPatterns:\n        - 'orders\\..*'\n", 1)
	})
	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", filepath.Join(t.TempDir(), "migration-state.json"), "--skip-validate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet supported")
}

// --- CR-derived bootstrap id (D1) and route mode (D5) ---

// TestInit_DerivesBootstrapServerIdFromCR — the id in the snapshot is DERIVED
// from the live CR's single-homed declaration, not authored in the manifest.
func TestInit_DerivesBootstrapServerIdFromCR(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", stateFile, "--skip-validate")
	require.NoError(t, err)

	state, err := migration.NewMigrationStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("msk-prod-to-cc-batch-1")
	require.NoError(t, err)
	require.Len(t, cfg.SwitchoverTargets, 1)
	assert.Equal(t, "SASL_PLAIN", cfg.SwitchoverTargets[0].BootstrapServerId,
		"the id is derived from the CR's spec.streamingDomains, not the manifest")
}

// TestInit_MultiHomedDomainIsError is D1a: a target domain declaring more than
// one bootstrap server id is a hard error naming the domain and its ids.
func TestInit_MultiHomedDomainIsError(t *testing.T) {
	stubCR(t, strings.Replace(defaultFixtureCR,
		"        bootstrapServers:\n          - id: SASL_PLAIN\n",
		"        bootstrapServers:\n          - id: SASL_PLAIN\n          - id: SASL_SCRAM\n", 1))
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", filepath.Join(t.TempDir(), "migration-state.json"), "--skip-validate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confluent-cloud")
}

// TestInit_TargetDomainAbsentFromCRIsError is the D1 zero case: a
// targetStreamingDomain the CR does not declare (a typo) is a hard error.
func TestInit_TargetDomainAbsentFromCRIsError(t *testing.T) {
	stubCR(t, strings.Replace(defaultFixtureCR, "    - name: confluent-cloud\n", "    - name: other-domain\n", 1))
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", filepath.Join(t.TempDir(), "migration-state.json"), "--skip-validate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confluent-cloud")
}

// TestInit_DynamicRouteIsNotYetImplemented is O1: a route resolving to the
// plural (topic-based) binding cannot be run through the static path — init
// refuses rather than silently treating it as static.
func TestInit_DynamicRouteIsNotYetImplemented(t *testing.T) {
	stubCR(t, strings.Replace(defaultFixtureCR,
		"      streamingDomain:\n        name: source-domain\n        bootstrapServerId: SOURCE_ID\n",
		"      streamingDomains:\n        - name: source-domain\n          bootstrapServerId: SOURCE_ID\n", 1))
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", filepath.Join(t.TempDir(), "migration-state.json"), "--skip-validate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet implement")
}

// TestInit_RouteAbsentFromCRIsError — a manifest route missing from the CR is an
// error (D5), never a silent fall-through.
func TestInit_RouteAbsentFromCRIsError(t *testing.T) {
	stubCR(t, strings.Replace(defaultFixtureCR, "    - name: migration-route\n", "    - name: other-route\n", 1))
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", filepath.Join(t.TempDir(), "migration-state.json"), "--skip-validate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-route")
}

// TestInit_SkipValidateStillReadsCR — D1c: --skip-validate defers credential and
// K8s-resource validation, but NOT the initial-CR read the snapshot's derived id
// depends on. A fetch failure must surface even under --skip-validate.
func TestInit_SkipValidateStillReadsCR(t *testing.T) {
	stubCRError(t, fmt.Errorf("boom: cluster unreachable"))
	_, err := runInit(t, "--migration-yaml", writeManifest(t, nil), "--migration-state-file", filepath.Join(t.TempDir(), "migration-state.json"), "--skip-validate")
	require.Error(t, err, "--skip-validate must still read the initial CR")
	assert.Contains(t, err.Error(), "boom")
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
// Phase 5, so this error surfaces locally, before any gateway/K8s contact is
// even attempted — see TestInit_SkipValidateSkipsCredentialResolution for the
// --skip-validate counterpart.
func TestInit_RejectsInvalidSourceCredentials(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifest(t, func(doc string) string {
		return strings.Replace(doc, "        mechanism: SHA512", "        mechanism: NOPE", 1)
	})
	_, err := runInit(t, "--migration-yaml", manifest, "--migration-state-file", stateFile)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "mechanism")
}

// TestInit_RejectsIAMOnApacheKafkaSource — the §2.4 gating reaches the
// command. No --skip-validate: see TestInit_RejectsInvalidSourceCredentials.
func TestInit_RejectsIAMOnApacheKafkaSource(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "migration-state.json")
	manifest := writeManifest(t, func(doc string) string {
		doc = strings.Replace(doc, "    type: msk", "    type: apache-kafka", 1)
		return strings.Replace(doc,
			"      sasl_scram:\n        username: admin\n        password: secret\n        mechanism: SHA512",
			"      iam:\n        region: us-east-1", 1)
	})
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
	manifest := writeManifest(t, func(doc string) string {
		return strings.Replace(doc, "        mechanism: SHA512", "        mechanism: NOPE", 1)
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
	manifest := writeManifest(t, func(doc string) string {
		doc = strings.Replace(doc, "kind: GatewayMigration", "kind: GatewayMigration\ninterpolate: true", 1)
		return strings.Replace(doc, "        password: secret", "        password: ${KCP_UNSET_PW}", 1)
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
	assert.NotContains(t, err.Error(), "KCP_UNSET_PW",
		"the credentials error must not mask the safety refusal")
}
