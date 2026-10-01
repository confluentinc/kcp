//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBaseline_FullMigrationCompletes proves the happy path end-to-end against
// the live gateway and validates the whole pipeline (run.sh → runner pod →
// execute → live reconcile → fence → promote → switch). It is the control for
// every resume test: an UNinterrupted run completes, a completed migration
// re-reconciles to zero work, and re-running execute on it finds nothing to do,
// runs no state machine and leaves the gateway CR untouched (the baseline of
// idempotency).
//
// Reserved slice: tbm-topic-051..055. Promotion is irreversible, so this
// consumes those topics for the life of the env (one-shot per standup).
//
// Every step emits tangible evidence via the harness: the live world state
// before, the full raw kcp output of each run, and the world state after.
func TestBaseline_FullMigrationCompletes(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	e.resetStaticRoute(t, ctx) // static: start from a pristine (source-bound, unfenced) route
	topics := e.topicRange(51, 55)
	mani := e.writeManifest(t, "baseline", topics)
	e.saveManifest(t, mani)

	e.snapshot(t, ctx, "before", "BEFORE — pristine (expect mirrors ACTIVE, route → source-domain, fencing empty)", topics)

	out, err := e.runKCP(t, "kcp-run-1-execute.log", "", "migration", "execute", "--migration-yaml", mani)
	require.NoError(t, err, "an uninterrupted execute must complete against the live gateway")
	require.Contains(t, strings.ToLower(out), "migration complete", "execute must report completion")

	e.snapshot(t, ctx, "after", "AFTER execute (expect mirrors STOPPED, route → destination-domain, fencing cleared)", topics)

	// Idempotency baseline: with the migration complete, a fresh reconcile must
	// classify every topic Unchanged and plan zero work — the property every
	// resume test relies on.
	out2, err2 := e.runKCP(t, "kcp-run-2-dry-run.log", "", "migration", "execute", "--migration-yaml", mani, "--dry-run")
	require.NoError(t, err2, "a dry-run on a completed migration must succeed")
	assert.Contains(t, out2, "0 to migrate", "a completed migration must plan zero further work")
	assert.Contains(t, out2, "unchanged", "a completed migration's topics must classify as Unchanged")

	// Re-running execute on the completed migration: reconcile finds nothing to
	// do, so no state machine runs and the gateway CR is not touched.
	crBefore := e.readCR(t, ctx)
	out3, err3 := e.runKCP(t, "kcp-run-3-rerun.log", "", "migration", "execute", "--migration-yaml", mani)
	require.NoError(t, err3, "re-running execute on a completed migration must succeed")
	requireNothingToDo(t, out3)
	require.Equal(t, string(crBefore), string(e.readCR(t, ctx)), "a nothing-to-do run must not touch the gateway CR")

	t.Logf("\n✅ RESULT: uninterrupted migration completed; re-reconcile plans zero work and a re-run finds nothing to do (idempotent baseline).")
}
