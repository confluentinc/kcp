//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResume_InterruptAfterFence_CompletesOnRerun proves the core promise: a run
// killed mid-cutover leaves a genuine partial world, and re-running the SAME
// manifest drives it to completion.
//
// It interrupts execute right after the fence step (KCP_TEST_CANCEL_AFTER=fenced,
// a real context cancellation — the Ctrl-C path), then shows the live gateway
// route is fenced with mirrors still ACTIVE (nothing promoted, not switched),
// then re-runs uninterrupted and shows the migration completes: mirrors STOPPED,
// route switched to destination.
//
// Reserved slice: batch-03 (tbm-topic-023..033), disjoint from other tests'.
func TestResume_InterruptAfterFence_CompletesOnRerun(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	topics := e.topicRange(23, 33)
	mani := e.manifestPath("batch-03.yaml")

	e.snapshot(t, ctx, "BEFORE — pristine (expect mirrors ACTIVE, route → source-domain, fencing empty)", topics)

	// --- Interrupt: cancel right after the fence checkpoint. ---
	out1, err1 := e.runKCP(t, cpFenced, "migration", "execute", "--migration-yaml", mani)
	require.Error(t, err1, "an interrupted run must exit non-zero (simulated abrupt exit)")
	require.Contains(t, out1, "kill-point", "the non-zero exit must be OUR seam firing, not a real failure")
	require.NotContains(t, strings.ToLower(out1), "migration complete", "the interrupted run must NOT have completed")

	e.snapshot(t, ctx, "AFTER interrupt (expect route FENCED, mirrors still ACTIVE, NOT switched)", topics)

	// The live world must be a real, resumable partial state: route fenced,
	// mirrors untouched (still ACTIVE — nothing promoted).
	rules := e.routeRulesYAML(t, e.readCR(t, ctx))
	require.Contains(t, rules, "fencing:", "the live route must carry a fencing block after the fence step")
	require.NotContains(t, rules, "fencing: []", "the fencing block must be populated, not empty")
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "ACTIVE", ms[tp], "%s must still be ACTIVE — the interrupt happened before promote", tp)
	}

	// --- Resume: re-run the SAME manifest, uninterrupted. ---
	out2, err2 := e.runKCP(t, "", "migration", "execute", "--migration-yaml", mani)
	require.NoError(t, err2, "re-running the same manifest must drive the migration to completion")
	require.Contains(t, strings.ToLower(out2), "migration complete", "the resume run must report completion")

	e.snapshot(t, ctx, "AFTER resume (expect mirrors STOPPED, route → destination-domain, fencing cleared)", topics)

	afterCR := e.readCR(t, ctx)
	ms2 := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "STOPPED", ms2[tp], "%s must be STOPPED after the resumed migration completes", tp)
	}
	require.Contains(t, e.routeRulesYAML(t, afterCR), "destination-domain", "the resumed migration must switch the route to the destination")

	// A resumed migration must reach the SAME clean end-state as an uninterrupted
	// one: NO fence left on the switched topics. This is the regression guard for
	// the stale-fence-on-resume bug — reconcile built the switchover from the
	// already-fenced live route and left kcp's fence behind. Before the fix this
	// assertion fails (topics switched AND fenced).
	fenced := e.fencedTopics(t, afterCR)
	for _, tp := range topics {
		require.Falsef(t, fenced[tp], "%s must NOT be fenced after the migration completes — a switched route must carry no kcp fence", tp)
	}

	t.Logf("\n✅ RESULT: run interrupted after fence (live route left fenced, mirrors ACTIVE); re-running the same manifest drove to completion (mirrors STOPPED, route switched, fence cleared).")
}
