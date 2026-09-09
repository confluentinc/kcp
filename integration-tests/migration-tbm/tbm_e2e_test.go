//go:build e2e

package migration_tbm_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

// TestSuccessBatchesMigrate drives batch-01..04 through the real execute-tbm
// command (U7): Decide (a cheap, read-only pre-check confirming the batch is
// genuinely migratable and how many topics it selects) → runKCP (the actual
// kcp binary, exercising internal/services/migration/tbm's real FSM, not the
// harness's own hand-rolled apply methods).
//
// initialize, wait_for_lags and fence are real, so this proves fence
// genuinely mutates the live gateway CR — something nothing else in this
// suite proves, since TestHaltScenarios and TestHarnessAppliesSwitchoverWithoutRoll
// only exercise migplan.Reconcile and the harness's own apply path directly.
// verify_fence, promote and switch remain noop, so each batch's mirror stays
// ACTIVE and the route's routing.conditions are never flipped — this test
// asserts that explicitly rather than assuming otherwise. Once promote and
// switch go real, that assertion (and the two removed sub-tests below) should
// be revisited.
//
// The per-batch assertion is size + disjointness + total (equal disjoint
// batches summing to the success range) rather than a hard-coded topic list,
// so it proves the plan's partition invariant without coupling to which
// topics setup.sh placed in which batch.
func TestSuccessBatchesMigrate(t *testing.T) {
	h := newHarness(t)
	require.Zerof(t, h.e.successHi%numBatches,
		"success range %d must divide evenly into %d batches", h.e.successHi, numBatches)
	perBatch := h.e.successHi / numBatches

	fenced := map[string]bool{}
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
				require.Falsef(t, fenced[tp], "topic %s appeared in more than one batch", tp)
			}
			batchTopics := append([]string(nil), res.Topics...)

			manifestPath := h.e.manifestPath(name + ".yaml")
			stateFile := filepath.Join(t.TempDir(), "tbm-state.json")
			out, err := runKCP(t, manifestPath, stateFile)
			require.NoErrorf(t, err, "execute-tbm must exit 0 for %s:\n%s", name, out)
			require.NotContains(t, out, "panic", "execute-tbm must not panic")

			data, readErr := os.ReadFile(stateFile)
			require.NoError(t, readErr, "execute-tbm must write --tbm-state-file")
			var parsed struct {
				Migrations []struct {
					MigrationId  string `json:"migration_id"`
					CurrentState string `json:"current_state"`
				} `json:"migrations"`
			}
			require.NoError(t, json.Unmarshal(data, &parsed), "the TBM state file must be valid JSON")
			require.Lenf(t, parsed.Migrations, 1, "state file must record exactly one migration for %s", name)
			require.Equal(t, "switched", parsed.Migrations[0].CurrentState,
				"the real FSM must walk every transition through to switched for "+name)

			require.Truef(t, routeFencingContainsAll(t, h, batchTopics),
				"the live gateway route's fencing block must include %s's topics after a real fence", name)

			mirrors := mirrorStatuses(t, h)
			for _, tp := range batchTopics {
				require.Equalf(t, clusterlink.MirrorStatusActive, mirrors[tp],
					"promote is still noop — %s's mirror must remain ACTIVE, got %s", tp, mirrors[tp])
				fenced[tp] = true
			}
		})
	}

	require.Lenf(t, fenced, h.e.successHi,
		"all %d batch-selected topics must end fenced across the %d batches", h.e.successHi, numBatches)

	// steady-state-noop and mixed-already-migrated-and-unmigrated (removed here)
	// both asserted topics classify Unchanged after a full migration, which
	// requires real promote and switch — neither is real yet. Restore both
	// once those two transitions land.
}

// routeFencingContainsAll reports whether the live gateway route's
// rules.fencing block includes every topic in topics — proof that a real
// fence transition (not the harness's own ApplyFence bypass) mutated the live
// cluster, by reading back the same route ReplaceRouteRulesObj wrote to.
func routeFencingContainsAll(t *testing.T, h *tbmHarness, topics []string) bool {
	t.Helper()

	var cr map[string]any
	require.NoError(t, yaml.Unmarshal(h.e.readCR(t, h.ctx), &cr))
	spec, _ := cr["spec"].(map[string]any)
	routes, _ := spec["routes"].([]any)

	fencedTopics := map[string]bool{}
	for _, r := range routes {
		route, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := route["name"].(string); name != h.e.route {
			continue
		}
		rules, _ := route["rules"].(map[string]any)
		fencing, _ := rules["fencing"].([]any)
		for _, e := range fencing {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			entryTopics, _ := entry["topics"].([]any)
			for _, tp := range entryTopics {
				if s, ok := tp.(string); ok {
					fencedTopics[s] = true
				}
			}
		}
	}

	for _, tp := range topics {
		if !fencedTopics[tp] {
			return false
		}
	}
	return true
}
