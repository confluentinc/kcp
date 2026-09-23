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
// every resume test: an UNinterrupted run completes, and a completed migration
// re-reconciles to a no-op (the baseline of idempotency).
//
// Reserved slice: batch-01 (tbm-topic-001..011). Promotion is irreversible, so
// this consumes those topics for the life of the env (one-shot per standup).
//
// Every step emits tangible evidence via the harness: the live world state
// before, the full raw kcp output of each run, and the world state after.
func TestBaseline_FullMigrationCompletes(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	topics := e.topicRange(1, 11)
	mani := e.manifestPath("batch-01.yaml")

	e.snapshot(t, ctx, "BEFORE — pristine (expect mirrors ACTIVE, route → source-domain, fencing empty)", topics)

	out, err := e.runKCP(t, "", "migration", "execute", "--migration-yaml", mani)
	require.NoError(t, err, "an uninterrupted execute must complete against the live gateway")
	require.Contains(t, strings.ToLower(out), "migration complete", "execute must report completion")

	e.snapshot(t, ctx, "AFTER execute (expect mirrors STOPPED, route → destination-domain, fencing cleared)", topics)

	// Idempotency baseline: with the migration complete, a fresh reconcile must
	// classify every topic Unchanged and plan zero work — the property every
	// resume test relies on.
	out2, err2 := e.runKCP(t, "", "migration", "execute", "--migration-yaml", mani, "--dry-run")
	require.NoError(t, err2, "a dry-run on a completed migration must succeed")
	assert.Contains(t, out2, "0 to migrate", "a completed migration must plan zero further work")
	assert.Contains(t, out2, "unchanged", "a completed migration's topics must classify as Unchanged")

	t.Logf("\n✅ RESULT: uninterrupted migration completed; re-reconcile is a clean no-op (idempotent baseline).")
}
