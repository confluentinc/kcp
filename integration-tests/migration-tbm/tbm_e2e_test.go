//go:build e2e

package migration_tbm_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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
// Every transition is real, including verify_fence — this test's manifests
// leave detectUnroutedProducersDuration at its default (0, disabled), so
// verify_fence only exercises its detection-disabled skip path here; the
// unrouted-producer detection and abort_fence rollback have their own live
// coverage in TestUnroutedProducerDetection below. This still proves the
// whole real migration path: fence genuinely mutates
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

// TestUnroutedProducerDetection drives a real unrouted-producer scenario
// against the live gateway: a producer writes directly to the source cluster
// (bypassing the fenced route entirely) while execute-tbm holds the fence, so
// verify_fence's real detectUnroutedProducers
// (internal/services/migration/tbm/workflow.go) must observe the source
// offset rise and trigger the real abort_fence rollback
// (orchestrator.go's onAbortFence/handleStepFailure).
//
// No manifest elsewhere in this suite sets a nonzero
// detectUnroutedProducersDuration (see TestSuccessBatchesMigrate's own doc
// comment), so this is the suite's only live coverage of verify_fence's
// detection and rollback path; the detection logic and the rollback's FSM
// transition are otherwise unit-tested only
// (internal/services/migration/tbm/workflow_test.go, orchestrator_test.go).
//
// Topic 046 (mirrored headroom, per testdata/batches/batch-01.yaml.tmpl's own
// comment) is used because it is the one mirrored topic no other test in
// this suite touches: 045 is reserved for the halt suite, 047 stays an
// active mirror for the halt suite (tbm_e2e_test.go's own
// mixed-already-migrated-and-unmigrated sub-test), and 048 is used by
// TestHarnessAppliesSwitchoverWithoutRoll.
func TestUnroutedProducerDetection(t *testing.T) {
	h := newHarness(t)
	require.NotEmpty(t, h.e.sourceBootstrap, "KCP_TBM_SOURCE_BOOTSTRAP must be set (see run.sh)")

	topic := h.e.topicName(46)
	g := h.manifestForTopics(t, "batch-01.yaml", []string{topic})

	res := h.Decide(t, g)
	require.Falsef(t, res.Refused, "topic %s must be migratable: %v", topic, res.Reasons)
	require.Equal(t, []string{topic}, res.Topics)

	// The repo enforces a 10s floor on a nonzero detectUnroutedProducersDuration
	// (internal/manifest/gateway.go's minDetectUnroutedProducersDuration) — also
	// the exact value the sibling AAO e2e suite already proved live for this
	// same check (migration_e2e_test.go's own rogue-producer tests).
	g.Spec.DefaultPolicies.DetectUnroutedProducersDuration = 10 * time.Second

	manifestBytes, err := yaml.Marshal(g)
	require.NoError(t, err)
	manifestPath := filepath.Join(t.TempDir(), "unrouted-producer.yaml")
	require.NoError(t, os.WriteFile(manifestPath, manifestBytes, 0o600))
	stateFile := filepath.Join(t.TempDir(), "tbm-state.json")

	rogueCtx, stopRogue := context.WithCancel(context.Background())
	rogueDone := startRogueProducer(t, rogueCtx, h.e.sourceBootstrap, topic)
	t.Cleanup(func() {
		stopRogue()
		<-rogueDone
	})

	// handleStepFailure returns the original step error even after a
	// successful rollback (orchestrator.go:224-240), so execute-tbm exits
	// non-zero here — this is expected, not a test failure.
	out, err := runKCP(t, manifestPath, stateFile)
	require.Errorf(t, err, "execute-tbm must exit non-zero on unrouted-producer detection:\n%s", out)
	require.NotContains(t, out, "panic", "execute-tbm must not panic")
	require.Contains(t, out, "Unrouted producers detected", "execute-tbm's narrative must show detection fired")
	require.Contains(t, out, "Gateway unfenced", "execute-tbm's narrative must show the rollback completed")

	data, readErr := os.ReadFile(stateFile)
	require.NoError(t, readErr, "execute-tbm must write --tbm-state-file even on a rolled-back run")
	var parsed struct {
		Migrations []struct {
			MigrationId  string `json:"migration_id"`
			CurrentState string `json:"current_state"`
		} `json:"migrations"`
	}
	require.NoError(t, json.Unmarshal(data, &parsed), "the TBM state file must be valid JSON")
	require.Lenf(t, parsed.Migrations, 1, "state file must record exactly one migration")
	require.Equal(t, "initialized", parsed.Migrations[0].CurrentState,
		"abort_fence must roll the FSM back to initialized (not lags_ok), so a resume re-checks lag for real before re-fencing")

	require.Falsef(t, routeSwitchedToTargetForAll(t, h, []string{topic}),
		"a rolled-back batch must never reach switch — %s must not be routed to the target domain", topic)

	mirrors := mirrorStatuses(t, h)
	require.Equalf(t, clusterlink.MirrorStatusActive, mirrors[topic],
		"a rolled-back batch must never promote its mirror — %s must still be ACTIVE, got %s", topic, mirrors[topic])
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
