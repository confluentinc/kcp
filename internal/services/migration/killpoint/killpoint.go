// Package killpoint is a test-only interruption seam for the migration FSMs.
//
// It lets a live resume test deterministically simulate an abrupt kcp exit
// (Ctrl-C / lost network / power loss) at a chosen point, by cancelling the run
// context after a named checkpoint. The mechanism is exactly the real
// interruption path — a context cancellation — so nothing bespoke is exercised.
//
// It is inert unless the environment variable is set to a checkpoint name, so
// it never fires in production. The env var is a per-invocation switch the e2e
// harness sets on the kcp process it is about to interrupt; a normal run never
// sets it.
package killpoint

import "os"

// EnvVar names the checkpoint after which the run should cancel itself. When
// unset (the production default) the seam is inert. The value is a checkpoint
// token — for the FSM-boundary checkpoints, an FSM state name such as "fenced"
// or "promoted".
const EnvVar = "KCP_TEST_CANCEL_AFTER"

// ShouldCancelAfter reports whether the run should cancel itself now that the
// given checkpoint has been reached. It is true only when EnvVar is set to
// exactly this checkpoint; unset or any other value is false.
func ShouldCancelAfter(checkpoint string) bool {
	want := os.Getenv(EnvVar)
	return want != "" && want == checkpoint
}
