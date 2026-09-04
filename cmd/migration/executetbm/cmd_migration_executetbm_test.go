package executetbm

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/migration/tbm"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatewayManifestTemplate is a complete, valid GatewayMigration document with
// a templated metadata.name — the only field these tests vary.
const gatewayManifestTemplate = `apiVersion: kcp.confluent.io/v1alpha1
kind: GatewayMigration
metadata:
  name: %s
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
    clusterId: %s
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
    crs:
      initial: gateway-initial
    routes:
      - name: migration-route
        streamingDomain:
          name: confluent-cloud
          bootstrapServerId: SASL_PLAIN
`

func writeManifest(t *testing.T, dir, name, clusterId string) string {
	t.Helper()
	p := filepath.Join(dir, "gateway-migration.yaml")
	doc := fmt.Sprintf(gatewayManifestTemplate, name, clusterId)
	require.NoError(t, os.WriteFile(p, []byte(doc), 0600))
	return p
}

func runExecuteTBM(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewMigrationExecuteTBMCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// withFastTBMTransitions shrinks the tbm package's simulated transition delay
// for the duration of a test, restoring it on cleanup — otherwise a full
// six-step run takes 42 real seconds.
func withFastTBMTransitions(t *testing.T) {
	t.Helper()
	original := tbm.TransitionSimulatedDelay
	tbm.TransitionSimulatedDelay = time.Millisecond
	t.Cleanup(func() { tbm.TransitionSimulatedDelay = original })
}

// --- flag surface ---

func TestExecuteTBM_FlagSurfaceIsMigrationYamlAndTbmStateFile(t *testing.T) {
	cmd := NewMigrationExecuteTBMCmd()
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	assert.ElementsMatch(t, []string{"migration-yaml", "tbm-state-file"}, names)
}

func TestExecuteTBM_RequiresMigrationYaml(t *testing.T) {
	dir := t.TempDir()
	_, err := runExecuteTBM(t, "--tbm-state-file", filepath.Join(dir, "tbm-state.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-yaml")
}

func TestExecuteTBM_RequiresTbmStateFile(t *testing.T) {
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-0", "lkc-abc123")
	_, err := runExecuteTBM(t, "--migration-yaml", manifestPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tbm-state-file")
}

// --- resolveTBMConfig: identity & drift, unit-level (no Execute involved) ---

func TestResolveTBMConfig_NoExistingEntry_CreatesFreshUninitialized(t *testing.T) {
	state := tbm.NewTBMState()

	cfg, err := resolveTBMConfig(state, "new-migration", "hash-1")

	require.NoError(t, err)
	assert.Equal(t, "new-migration", cfg.MigrationId)
	assert.Equal(t, tbm.StateUninitialized, cfg.CurrentState)
	assert.Equal(t, "hash-1", cfg.ManifestHash)
}

func TestResolveTBMConfig_HashMatches_ResumesExisting(t *testing.T) {
	state := tbm.NewTBMState()
	state.UpsertMigration(tbm.TBMConfig{MigrationId: "mig-1", CurrentState: tbm.StateFenced, ManifestHash: "hash-1"})

	cfg, err := resolveTBMConfig(state, "mig-1", "hash-1")

	require.NoError(t, err)
	assert.Equal(t, tbm.StateFenced, cfg.CurrentState)
}

func TestResolveTBMConfig_HashDiffers_RefusesUnconditionally(t *testing.T) {
	for _, currentState := range []string{tbm.StateUninitialized, tbm.StateFenced, tbm.StateSwitched} {
		t.Run(currentState, func(t *testing.T) {
			state := tbm.NewTBMState()
			state.UpsertMigration(tbm.TBMConfig{MigrationId: "mig-1", CurrentState: currentState, ManifestHash: "hash-1"})

			_, err := resolveTBMConfig(state, "mig-1", "hash-2")

			require.Error(t, err, "drift must refuse regardless of CurrentState")
			assert.Contains(t, err.Error(), "changed since it was last run")
		})
	}
}

// --- end-to-end through the command ---

func TestExecuteTBM_SameManifest_ResumesAndThenShortCircuits(t *testing.T) {
	withFastTBMTransitions(t)
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-2", "lkc-abc123")
	stateFile := filepath.Join(dir, "tbm-state.json")

	_, err := runExecuteTBM(t, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.NoError(t, err)

	state, err := tbm.NewTBMStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("tbm-batch-2")
	require.NoError(t, err)
	assert.Equal(t, tbm.StateSwitched, cfg.CurrentState)

	// Second run against the identical file: hash matches, already done ->
	// short-circuits via HasPendingWork without re-running the workflow.
	out, err := runExecuteTBM(t, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.NoError(t, err)
	assert.Contains(t, out, "already complete")
}

func TestExecuteTBM_ChangedManifest_RefusesEvenAfterDone(t *testing.T) {
	withFastTBMTransitions(t)
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, "tbm-batch-3", "lkc-abc123")
	stateFile := filepath.Join(dir, "tbm-state.json")

	_, err := runExecuteTBM(t, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.NoError(t, err)

	// Edit the manifest (still schema-valid, still the same metadata.name).
	mutated := strings.ReplaceAll(fmt.Sprintf(gatewayManifestTemplate, "tbm-batch-3", "lkc-abc123"), "lkc-abc123", "lkc-changed")
	require.NoError(t, os.WriteFile(manifestPath, []byte(mutated), 0600))

	_, err = runExecuteTBM(t, "--migration-yaml", manifestPath, "--tbm-state-file", stateFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "changed since it was last run")

	state, err := tbm.NewTBMStateFromFile(stateFile)
	require.NoError(t, err)
	cfg, err := state.GetMigrationById("tbm-batch-3")
	require.NoError(t, err)
	assert.Equal(t, tbm.StateSwitched, cfg.CurrentState, "the stale entry must be untouched by the refused run")
}
