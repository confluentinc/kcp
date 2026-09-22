//go:build e2e

package migration_tbm_e2e

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

// execTBMTimeout covers every real transition: initialize, wait_for_lags,
// fence, verify_fence, promote, and switch.
const execTBMTimeout = 5 * time.Minute

// kcpBinary is the in-pod kcp binary run.sh builds and cp's into the runner.
func kcpBinary() string { return envOrDefault("KCP_TBM_KCP_BIN", "/workspace/kcp") }

// TestExecuteTBMThinPosture covers the execute command's behavior that is
// genuinely independent of batch/topic state, plus the live regression test
// for the bug that once crashed Fence on a zero-topic result. Its per-batch
// happy path (does a real batch actually fence/promote/switch) moved to
// TestSuccessBatchesMigrate, which now drives the real command directly and
// — since fence, promote and switch are all real — also proves the genuine
// zero-topic steady state at the engine level, via its own restored
// steady-state-noop and mixed-already-migrated-and-unmigrated sub-tests
// (Decide-only, never a second execute run). That is a strictly weaker
// claim than this test's own zero-topic-batch sub-test below, which proves
// the COMMAND — not just the engine — completes cleanly when there is
// nothing to migrate.
//
// The former "unwritable-state-file-fails-cleanly" sub-test is gone (Plan
// 2e): it existed only to prove an unwritable --migration-state-file failed
// cleanly, and that flag — and the early write it guarded — no longer
// exist. Nothing observable survives that premise; there is no replacement.
func TestExecuteTBMThinPosture(t *testing.T) {
	h := newHarness(t)
	manifestPath := h.e.manifestPath("batch-01.yaml")

	// zero-topic-batch-completes-cleanly-leaves-world-unchanged is the live
	// regression test for the bug that once crashed Fence — and would have
	// crashed Promote/Switch too, had they been real at the time — on a
	// fresh migplan.Result with Refused: false and zero topics: the
	// "already fully migrated, nothing to do" steady state, structurally
	// distinct from a refusal (see reconcile.go's
	// Refused()-then-len(migratable)==0 split). By the time this runs,
	// TestSuccessBatchesMigrate (which runs first, alphabetically, in this
	// same suite) has already fully migrated batch-01's topics for real, so
	// a fresh execute run against the same manifest — there is no state file
	// any more (Plan 2e), so this is a first-ever-looking run purely because
	// migplan.Reconcile is called live, every time, against the manifest and
	// the live cluster — sees this as a first-ever run and Reconcile really
	// runs fresh — hits exactly this case. Every real transition (fence,
	// promote, switch) must recognize it and no-op; this proves execute
	// itself does, not just Decide.
	t.Run("zero-topic-batch-completes-cleanly-leaves-world-unchanged", func(t *testing.T) {
		routesBefore := gatewayRoutes(t, h)
		mirrorsBefore := mirrorStatuses(t, h)

		out, err := runKCP(t, manifestPath)
		require.NoErrorf(t, err, "execute must exit 0 against an already-migrated batch:\n%s", out)
		require.NotContains(t, out, "panic", "execute must not panic")

		// Zero topics to migrate: every real transition finds nothing to do,
		// so neither the gateway route nor any mirror may have moved.
		require.Equal(t, string(routesBefore), string(gatewayRoutes(t, h)),
			"a zero-topic run must not edit the gateway route")
		require.Equal(t, mirrorsBefore, mirrorStatuses(t, h),
			"a zero-topic run must not promote or change any mirror")
	})
}

// gatewayRoutes returns the live gateway CR's spec.routes as marshaled YAML,
// for a before/after equality check.
func gatewayRoutes(t *testing.T, h *tbmHarness) []byte {
	t.Helper()
	var cr map[string]any
	require.NoError(t, yaml.Unmarshal(h.e.readCR(t, h.ctx), &cr))
	spec, _ := cr["spec"].(map[string]any)
	out, err := yaml.Marshal(spec["routes"])
	require.NoError(t, err)
	return out
}

// runKCP invokes the in-pod kcp binary's execute with only file-path args (no
// secrets on argv) and returns the combined output. There is no
// --migration-state-file any more (Plan 2e): execute reads only the
// manifest and live cluster state on every run.
func runKCP(t *testing.T, manifestPath string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), execTBMTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, kcpBinary(),
		"migration", "execute",
		"--migration-yaml", manifestPath,
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
