//go:build e2e

package migration_tbm_e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

// execTBMTimeout covers initialize/wait_for_lags/fence (real, but fast when
// there is nothing to migrate — see TestExecuteTBMThinPosture) plus
// verify_fence/promote/switch, which remain noop and sleep
// TransitionSimulatedDelay (7s) each.
const execTBMTimeout = 5 * time.Minute

// kcpBinary is the in-pod kcp binary run.sh builds and cp's into the runner.
func kcpBinary() string { return envOrDefault("KCP_TBM_KCP_BIN", "/workspace/kcp") }

// TestExecuteTBMThinPosture covers the only unit that drives the execute-tbm
// command (U9). initialize, wait_for_lags and fence are real;
// verify_fence, promote and switch remain noop. TestSuccessBatchesMigrate
// (which runs first, alphabetically, in this same suite) has already fully
// migrated batch-01's topics by the time this runs, so this exercises the
// real FSM on a legitimate steady-state resume: migplan.Reconcile returns
// Refused: false with zero topics (an already-migrated batch is not a
// refusal — see reconcile.go's Refused()-then-len(migratable)==0 split),
// which the real fence transition must treat as "nothing to fence" rather
// than crash on (the exact bug this sub-test now guards against — see
// internal/services/migration/tbm's Fence). Because there is nothing to
// migrate, no real transition has anything to do, so the world stays
// untouched — that is the correct reason nothing moves, not because the FSM
// is a noop. The destination SASL credential is never on the exec argv: it
// reaches the command only through the rendered manifest file it reads.
func TestExecuteTBMThinPosture(t *testing.T) {
	h := newHarness(t)
	manifestPath := h.e.manifestPath("batch-01.yaml")

	t.Run("steady-state-batch-completes-cleanly-leaves-world-unchanged", func(t *testing.T) {
		stateFile := filepath.Join(t.TempDir(), "tbm-state.json")

		routesBefore := gatewayRoutes(t, h)
		mirrorsBefore := mirrorStatuses(t, h)

		out, err := runKCP(t, manifestPath, stateFile)
		require.NoErrorf(t, err, "execute-tbm must exit 0 against the live topology:\n%s", out)
		require.NotContains(t, out, "panic", "execute-tbm must not panic")

		// Engine was invoked (a reconcile I/O failure would have made it exit
		// non-zero) and the command wrote a parseable state file.
		data, readErr := os.ReadFile(stateFile)
		require.NoError(t, readErr, "execute-tbm must write --tbm-state-file")
		require.NotEmpty(t, data)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(data, &parsed), "the TBM state file must be valid JSON")

		// Zero topics to migrate: fence (real, but a no-op here) and the
		// still-noop steps after it find nothing to do, so neither the gateway
		// route nor any mirror may have moved.
		require.Equal(t, string(routesBefore), string(gatewayRoutes(t, h)),
			"a steady-state run (zero topics to migrate) must not edit the gateway route")
		require.Equal(t, mirrorsBefore, mirrorStatuses(t, h),
			"a steady-state run (zero topics to migrate) must not promote any mirror")
	})

	// Abuse: an unwritable --tbm-state-file (missing parent directory) must fail
	// cleanly — non-zero exit, no panic — because the command writes the state file
	// early, before the reconcile.
	t.Run("unwritable-state-file-fails-cleanly", func(t *testing.T) {
		badState := filepath.Join(t.TempDir(), "missing-parent", "tbm-state.json")

		out, err := runKCP(t, manifestPath, badState)
		require.Error(t, err, "an unwritable --tbm-state-file must fail")
		require.NotContains(t, out, "panic", "a write failure must not panic")

		_, statErr := os.Stat(badState)
		require.True(t, os.IsNotExist(statErr), "no state file should be created under the bad path")
	})
}

// runKCP invokes the in-pod kcp binary's execute-tbm with only file-path args (no
// secrets on argv) and returns the combined output.
func runKCP(t *testing.T, manifestPath, stateFile string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), execTBMTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, kcpBinary(),
		"migration", "execute-tbm",
		"--migration-yaml", manifestPath,
		"--tbm-state-file", stateFile,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gatewayRoutes returns the live gateway CR's spec.routes as marshaled YAML, for a
// before/after equality check.
func gatewayRoutes(t *testing.T, h *tbmHarness) []byte {
	t.Helper()
	var cr map[string]any
	require.NoError(t, yaml.Unmarshal(h.e.readCR(t, h.ctx), &cr))
	spec, _ := cr["spec"].(map[string]any)
	out, err := yaml.Marshal(spec["routes"])
	require.NoError(t, err)
	return out
}

// mirrorStatuses maps each mirror topic on the link to its current status.
func mirrorStatuses(t *testing.T, h *tbmHarness) map[string]string {
	t.Helper()
	mirrors, err := h.e.linkSvc.ListMirrorTopics(h.ctx, h.e.linkConfig())
	require.NoError(t, err)
	m := make(map[string]string, len(mirrors))
	for _, mt := range mirrors {
		m[mt.MirrorTopicName] = mt.MirrorStatus
	}
	return m
}
