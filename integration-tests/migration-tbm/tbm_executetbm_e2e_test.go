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

// execTBMTimeout must exceed the noop FSM's simulated delay: it sleeps
// TransitionSimulatedDelay (7s) per transition across ~6 transitions, plus the
// live reconcile reads.
const execTBMTimeout = 5 * time.Minute

// kcpBinary is the in-pod kcp binary run.sh builds and cp's into the runner.
func kcpBinary() string { return envOrDefault("KCP_TBM_KCP_BIN", "/workspace/kcp") }

// TestExecuteTBMThinPosture covers the only unit that drives the execute-tbm
// command (U9). Its FSM is a noop, so the assertions are deliberately thin: the
// command runs the engine, writes a parseable TBM state file, and — because every
// transition is a noop — leaves the mirror/route world untouched. The destination
// SASL credential is never on the exec argv: it reaches the command only through
// the rendered manifest file it reads.
func TestExecuteTBMThinPosture(t *testing.T) {
	h := newHarness(t)
	manifestPath := h.e.manifestPath("batch-01.yaml")

	t.Run("happy-path-writes-state-leaves-world-unchanged", func(t *testing.T) {
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

		// Noop FSM: neither the gateway route nor any mirror may have moved.
		require.Equal(t, string(routesBefore), string(gatewayRoutes(t, h)),
			"the noop FSM must not edit the gateway route")
		require.Equal(t, mirrorsBefore, mirrorStatuses(t, h),
			"the noop FSM must not promote any mirror")
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
