// Package killpoint holds the migration FSMs' three test-only seams.
//
// The kill point lets a live resume test deterministically simulate an abrupt
// kcp exit (a Ctrl-C, lost network, power loss) at a chosen point: it cancels
// the run context right after a named checkpoint and the run returns, leaving
// the world an exit at that point would leave. A real Ctrl-C ends the process
// wherever it is (kcp installs no signal handler); the kill point makes the
// stopping point exact.
//
// The failure hook makes a named workflow step fail instead of running, so a
// live test can drive a step failure (and the rollback it triggers)
// deterministically. The step fails through the same handling a real failure of
// that step takes.
//
// The hold point pauses the run right after a named checkpoint until a release
// file appears, so a live test can change the world mid-run (edit the Gateway CR,
// say) and then let the run carry on. The run signals it is holding by creating
// "<file>.held"; the test creates "<file>" to release it.
//
// All three are inert unless their environment variable is set, so they never
// fire in production. Each env var is a per-invocation switch the e2e harness
// sets on the kcp process under test; a normal run never sets them.
package killpoint

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// EnvVar names the checkpoint after which the run should cancel itself. When
// unset (the production default) the seam is inert. The value is a checkpoint
// token — for the FSM-boundary checkpoints, an FSM state name such as "fenced"
// or "promoted".
const EnvVar = "KCP_TEST_CANCEL_AFTER"

// AfterPromoteAccepted is an intra-step checkpoint (not an FSM state name): it
// fires inside the promote loop right after a promote request is accepted
// (error_code 0) but before the mirror is confirmed STOPPED, leaving mirrors in
// PENDING_STOPPED — the state a resume must handle without re-promoting.
const AfterPromoteAccepted = "promote_accepted"

// ShouldCancelAfter reports whether the run should cancel itself now that the
// given checkpoint has been reached. It is true only when EnvVar is set to
// exactly this checkpoint; unset or any other value is false.
func ShouldCancelAfter(checkpoint string) bool {
	want := os.Getenv(EnvVar)
	return want != "" && want == checkpoint
}

// FailEnvVar names the workflow step (an FSM event such as "verify_fence") the
// failure hook fails. When unset (the production default) the hook is inert.
const FailEnvVar = "KCP_TEST_FAIL_AT"

// FailAt returns an error when FailEnvVar names step, and nil otherwise. The
// FSMs call it as each step starts; a non-nil error fails the step in place of
// its action.
func FailAt(step string) error {
	if want := os.Getenv(FailEnvVar); want != "" && want == step {
		return fmt.Errorf("test failure hook: %s failed (%s=%s)", step, FailEnvVar, want)
	}
	return nil
}

// HoldEnvVar names the checkpoint (an FSM state name) after which the run holds.
// When unset (the production default) the hold point is inert.
const HoldEnvVar = "KCP_TEST_HOLD_AFTER"

// HoldFileEnvVar names the release file the hold waits for. The run creates
// "<file>.held" on reaching the hold; the test creates "<file>" to release it.
const HoldFileEnvVar = "KCP_TEST_HOLD_FILE"

const holdPollInterval = 200 * time.Millisecond

// HoldAfter holds the run at checkpoint when HoldEnvVar names it: it creates
// "<file>.held", then polls until "<file>" exists (nil) or ctx ends (ctx.Err()).
// It errors when HoldFileEnvVar is unset or "<file>.held" cannot be created, and
// is a no-op (nil, no I/O) when HoldEnvVar is unset or names another checkpoint.
func HoldAfter(ctx context.Context, checkpoint string) error {
	if want := os.Getenv(HoldEnvVar); want == "" || want != checkpoint {
		return nil
	}
	file := os.Getenv(HoldFileEnvVar)
	if file == "" {
		return fmt.Errorf("test hold point %s=%s requires %s to be set", HoldEnvVar, checkpoint, HoldFileEnvVar)
	}
	if err := os.WriteFile(file+".held", nil, 0o600); err != nil {
		return fmt.Errorf("test hold point: creating %s.held: %w", file, err)
	}
	slog.Warn("⚠️ test hold point reached — waiting for release", "afterState", checkpoint, "file", file)

	ticker := time.NewTicker(holdPollInterval)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(file); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
