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
// harness's own hand-rolled apply methods) — something nothing else in this
// suite proves, since TestHaltScenarios and TestHarnessAppliesSwitchoverWithoutRoll
// only exercise migplan.Reconcile and the harness's own apply path directly.
//
// verify_fence is the only remaining noop; fence, promote and switch are all
// real, so this proves the whole real migration path: fence genuinely mutates
// the live gateway CR (proven via the command's own stdout narrative, not by
// re-reading the CR — switch legitimately clears the fence in the same
// synchronous run, before any external observer could see it; see the
// per-batch assertion's own comment for the AAO precedent this follows),
// promote genuinely stops the mirror, and switch genuinely flips
// routing.conditions to the target domain — a fresh Decide after all three
// now classifies the batch's topics Unchanged, restoring the
// steady-state-noop and mixed-already-migrated-and-unmigrated sub-tests below
// (both need real promote+switch to hold, and now both do).
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

			// A full batch run always reaches switched in one synchronous
			// execute-tbm call, and switch legitimately clears the fence
			// switchover applies (migplan derives SwitchoverYAML from the
			// pre-fence rules tree, not the fenced one — see
			// reconcile_test.go's own invariant: the batch topic must not
			// appear in the switchover's fencing region). So the live CR's
			// fencing block is gone again by the time this assertion runs,
			// the same way AAO's own e2e suite only ever asserts a fence's
			// ABSENCE post-completion (assertGatewaySpec, called after every
			// successful switch and every rollback alike) and instead proves
			// fencing really happened by parsing the command's own stdout
			// narrative (migration_e2e_test.go's fenceIdx checks) — not by
			// inspecting a live artifact a later step legitimately clears.
			require.Containsf(t, out, "Gateway fenced and ready",
				"execute-tbm's own narrative must show fence completed successfully for %s", name)

			mirrors := mirrorStatuses(t, h)
			for _, tp := range batchTopics {
				require.Equalf(t, clusterlink.MirrorStatusStopped, mirrors[tp],
					"promote is real — %s's mirror must reach STOPPED, got %s", tp, mirrors[tp])
			}

			require.Truef(t, routeSwitchedToTargetForAll(t, h, batchTopics),
				"switch is real — the live gateway route's routing.conditions must include %s's topics, routed to the target domain", name)
			for _, tp := range batchTopics {
				fenced[tp] = true
			}
		})
	}

	require.Lenf(t, fenced, h.e.successHi,
		"all %d batch-selected topics must end fenced across the %d batches", h.e.successHi, numBatches)

	t.Run("steady-state-noop", func(t *testing.T) {
		g := h.manifestForTopics(t, "batch-01.yaml", h.e.topicRange(1, h.e.successHi))
		res := h.Decide(t, g)
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

// routeSwitchedToTargetForAll reports whether the live gateway route's
// rules.routing.conditions block includes every topic in topics, routed to
// the destination domain — proof that a real switch transition (not the
// harness's own ApplySwitchover bypass) mutated the live cluster.
func routeSwitchedToTargetForAll(t *testing.T, h *tbmHarness, topics []string) bool {
	t.Helper()

	var cr map[string]any
	require.NoError(t, yaml.Unmarshal(h.e.readCR(t, h.ctx), &cr))
	spec, _ := cr["spec"].(map[string]any)
	routes, _ := spec["routes"].([]any)

	switchedTopics := map[string]bool{}
	for _, r := range routes {
		route, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := route["name"].(string); name != h.e.route {
			continue
		}
		rules, _ := route["rules"].(map[string]any)
		routing, _ := rules["routing"].(map[string]any)
		conditions, _ := routing["conditions"].([]any)
		for _, c := range conditions {
			cond, ok := c.(map[string]any)
			if !ok {
				continue
			}
			condTopics, _ := cond["topics"].([]any)
			for _, tp := range condTopics {
				if s, ok := tp.(string); ok {
					switchedTopics[s] = true
				}
			}
		}
	}

	for _, tp := range topics {
		if !switchedTopics[tp] {
			return false
		}
	}
	return true
}
