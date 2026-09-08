//go:build e2e

package migration_tbm_e2e

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSuccessBatchesMigrate drives batch-01..04 through the harness loop (U7):
// Decide → assert MIGRATABLE → ApplyFence → PromoteMirrors → ApplySwitchover
// (each apply confirms configId convergence on every pod with no roll) → ReRun and
// assert this batch is now excluded (Unchanged). After the four batches it asserts
// all success-range topics migrated and that a full re-run is a steady-state no-op,
// plus the in-code mixed already-migrated + unmigrated edge.
//
// The per-batch assertion is size + disjointness + total (equal disjoint batches
// summing to the success range) rather than a hard-coded topic list, so it proves
// the plan's partition invariant without coupling to which topics setup.sh placed
// in which batch.
func TestSuccessBatchesMigrate(t *testing.T) {
	h := newHarness(t)
	require.Zerof(t, h.e.successHi%numBatches,
		"success range %d must divide evenly into %d batches", h.e.successHi, numBatches)
	perBatch := h.e.successHi / numBatches

	migrated := map[string]bool{}
	for b := 1; b <= numBatches; b++ {
		name := fmt.Sprintf("batch-%02d", b)
		t.Run(name, func(t *testing.T) {
			g := h.loadManifest(t, name+".yaml")

			res := h.Decide(t, g)
			require.Falsef(t, res.Refused, "batch %s must be migratable: %v", name, res.Reasons)
			require.Lenf(t, res.Topics, perBatch, "batch %s must select %d migratable topics", name, perBatch)
			require.NotEmpty(t, res.FenceYAML)
			require.NotEmpty(t, res.SwitchoverYAML)
			for _, tp := range res.Topics {
				require.Falsef(t, migrated[tp], "topic %s appeared in more than one batch", tp)
			}

			batchTopics := append([]string(nil), res.Topics...)
			h.ApplyFence(t, res)
			h.PromoteMirrors(t, batchTopics)
			h.ApplySwitchover(t, res)

			rerun := h.ReRun(t, g)
			require.False(t, rerun.Refused, "a completed batch must not refuse on re-run")
			require.Empty(t, rerun.Topics, "a completed batch migrates nothing on re-run")
			unchanged := unchangedTopics(rerun.Report)
			for _, tp := range batchTopics {
				require.Truef(t, unchanged[tp],
					"after switchover + promotion, %s must classify Unchanged (excluded), not a refusal", tp)
				migrated[tp] = true
			}
		})
	}

	require.Lenf(t, migrated, h.e.successHi,
		"all %d batch-selected topics must end migrated across the %d batches", h.e.successHi, numBatches)

	t.Run("steady-state-noop", func(t *testing.T) {
		g := h.manifestForTopics(t, "batch-01.yaml", h.e.topicRange(1, h.e.successHi))
		res := h.ReRun(t, g)
		require.Falsef(t, res.Refused, "a steady-state re-run refuses nothing: %v", res.Reasons)
		require.Empty(t, res.Topics, "a steady-state re-run migrates nothing")
		require.Lenf(t, res.Report.Unchanged, h.e.successHi,
			"every batch-selected topic must classify Unchanged at steady state")
	})

	// Edge: a batch mixing one already-migrated topic (001, from batch-01) with an
	// un-migrated headroom topic (047) still succeeds for the un-migrated member;
	// the already-migrated member is Unchanged, not a halt (KTD5). Decide-only, so
	// 047 stays an active mirror for the halt suite.
	t.Run("mixed-already-migrated-and-unmigrated", func(t *testing.T) {
		already := h.e.topicName(1)
		fresh := h.e.topicName(47)
		g := h.manifestForTopics(t, "batch-01.yaml", []string{already, fresh})

		res := h.Decide(t, g)
		require.Falsef(t, res.Refused, "an already-migrated member must not halt the batch: %v", res.Reasons)
		require.Equal(t, []string{fresh}, res.Topics, "only the not-yet-migrated member is promoted")
		require.NotEmpty(t, res.FenceYAML)
		require.NotEmpty(t, res.SwitchoverYAML)
		require.Truef(t, unchangedTopics(res.Report)[already], "%s must classify Unchanged", already)
	})
}
