//go:build e2e

package migration_tbm_e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// execTBMTimeout covers initialize/wait_for_lags/fence/promote/switch (all
// real) plus verify_fence, the one remaining noop step, which sleeps
// TransitionSimulatedDelay (7s).
const execTBMTimeout = 5 * time.Minute

// kcpBinary is the in-pod kcp binary run.sh builds and cp's into the runner.
func kcpBinary() string { return envOrDefault("KCP_TBM_KCP_BIN", "/workspace/kcp") }

// TestExecuteTBMThinPosture covers the execute-tbm command's behavior that is
// genuinely independent of batch/topic state. Its per-batch happy path (does
// a real batch actually fence/promote/switch) moved to
// TestSuccessBatchesMigrate, which now drives the real command directly and
// — since fence, promote and switch are all real — also proves the genuine
// zero-topic steady state itself, via its own restored steady-state-noop and
// mixed-already-migrated-and-unmigrated sub-tests (Decide-only, not a second
// execute-tbm run). Re-running execute-tbm here against the same
// already-fully-migrated batch-01 would just be redundant with that
// coverage, so this test is left to what it alone is for: the
// unwritable-state-file failure mode, which doesn't depend on batch/topic
// state at all.
func TestExecuteTBMThinPosture(t *testing.T) {
	h := newHarness(t)
	manifestPath := h.e.manifestPath("batch-01.yaml")

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
