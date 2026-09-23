//go:build e2e

package idempotent_fsm_e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// runResumeScenario is the shared shape of every boundary kill-point test:
// interrupt execute right after `checkpoint` (a real context cancellation via
// the killpoint seam), let assertPartial verify the genuine partial world it
// left, then re-run the SAME manifest uninterrupted and assert the migration
// reaches the clean, complete end-state — mirrors STOPPED, route switched to
// destination, and NO stale fence. Every run's raw output and the before/after
// world state are logged by the harness for human review.
func runResumeScenario(t *testing.T, e *env, checkpoint, name string, topics []string, assertPartial func(t *testing.T, ctx context.Context)) {
	ctx := context.Background()
	mani := e.writeManifest(t, name, topics)

	e.snapshot(t, ctx, "BEFORE "+name+" (expect mirrors ACTIVE, route → source)", topics)

	out1, err1 := e.runKCP(t, checkpoint, "migration", "execute", "--migration-yaml", mani)
	require.Errorf(t, err1, "the run interrupted at %q must exit non-zero (simulated abrupt exit)", checkpoint)
	require.Containsf(t, out1, "kill-point", "the non-zero exit at %q must be OUR seam firing, not a real failure", checkpoint)
	require.NotContains(t, strings.ToLower(out1), "migration complete", "the interrupted run must NOT have completed")

	e.snapshot(t, ctx, "AFTER interrupt at "+checkpoint, topics)
	assertPartial(t, ctx)

	out2, err2 := e.runKCP(t, "", "migration", "execute", "--migration-yaml", mani)
	require.NoErrorf(t, err2, "re-running the same manifest after an interrupt at %q must drive to completion", checkpoint)
	require.Contains(t, strings.ToLower(out2), "migration complete", "the resume run must report completion")

	e.snapshot(t, ctx, "AFTER resume (expect mirrors STOPPED, route → destination, fence cleared)", topics)

	afterCR := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED after the resumed migration completes", tp)
		require.Truef(t, e.isSwitchedToTarget(t, afterCR, tp), "%s must be switched to the target domain after completion", tp)
		require.Falsef(t, e.isFenced(t, afterCR, tp), "%s must NOT be fenced after completion — a switched route carries no kcp fence", tp)
	}
	t.Logf("\n✅ RESULT: interrupted at %q, re-run drove to completion (mirrors STOPPED, route switched, fence cleared).", checkpoint)
}

// Kill after FENCE: the Migratable resume path. Partial world = route fenced,
// mirrors still ACTIVE (nothing promoted). Slice tbm-topic-056..060.
func TestResume_InterruptAfterFence(t *testing.T) {
	e := newEnv()
	topics := e.topicRange(56, 60)
	runResumeScenario(t, e, cpFenced, "resume-fence", topics, func(t *testing.T, ctx context.Context) {
		cr := e.readCR(t, ctx)
		ms := e.mirrorStatus(t, ctx)
		for _, tp := range topics {
			require.Truef(t, e.isFenced(t, cr, tp), "%s must be fenced after the fence step", tp)
			require.Equalf(t, "ACTIVE", ms[tp], "%s must still be ACTIVE — interrupt happened before promote", tp)
		}
	})
}

// Kill after PROMOTE: the SwitchOnly resume path. Partial world = mirrors
// STOPPED (promoted) but route NOT yet switched (fence still present). The
// resume must switch them without re-promoting. Slice tbm-topic-061..065.
func TestResume_InterruptAfterPromote(t *testing.T) {
	e := newEnv()
	e.skipMultiScenarioOnStatic(t)
	topics := e.topicRange(61, 65)
	runResumeScenario(t, e, cpPromoted, "resume-promote", topics, func(t *testing.T, ctx context.Context) {
		cr := e.readCR(t, ctx)
		ms := e.mirrorStatus(t, ctx)
		for _, tp := range topics {
			require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED — interrupt happened after promote", tp)
			require.Truef(t, e.isFenced(t, cr, tp), "%s must still be fenced — interrupt happened before switch", tp)
		}
	})
}

// Kill DURING promote (intra-step, killpoint.AfterPromoteAccepted): after a
// promote request is accepted but before STOPPED is confirmed, leaving mirrors
// PENDING_STOPPED. The resume must NOT re-promote the already-promoting mirror
// (CC rejects that → fatal) — it must wait for STOPPED then switch. This is the
// live proof of the PENDING_STOPPED resume fix. Slice tbm-topic-071..075.
func TestResume_InterruptDuringPromote(t *testing.T) {
	e := newEnv()
	e.skipMultiScenarioOnStatic(t)
	topics := e.topicRange(71, 75)
	runResumeScenario(t, e, cpPromoteAccepted, "resume-promote-accepted", topics, func(t *testing.T, ctx context.Context) {
		ms := e.mirrorStatus(t, ctx)
		for _, tp := range topics {
			require.Containsf(t, []string{"PENDING_STOPPED", "STOPPED"}, ms[tp],
				"%s must be mid/finished promotion (not ACTIVE) after the intra-promote interrupt, got %q", tp, ms[tp])
		}
	})
}

// Kill after SWITCH: the Unchanged resume path. The interrupt fires once the
// migration is effectively complete (route switched, fence cleared, mirrors
// STOPPED) but before the success banner; the re-run must be a clean no-op that
// still reports completion. Slice tbm-topic-066..070.
func TestResume_InterruptAfterSwitch(t *testing.T) {
	e := newEnv()
	e.skipMultiScenarioOnStatic(t)
	topics := e.topicRange(66, 70)
	runResumeScenario(t, e, cpSwitched, "resume-switch", topics, func(t *testing.T, ctx context.Context) {
		cr := e.readCR(t, ctx)
		ms := e.mirrorStatus(t, ctx)
		for _, tp := range topics {
			require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED — switch already ran", tp)
			require.Falsef(t, e.isFenced(t, cr, tp), "%s must already be unfenced — switch cleared the fence", tp)
		}
	})
}
