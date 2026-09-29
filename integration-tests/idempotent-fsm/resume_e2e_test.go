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
func runResumeScenario(t *testing.T, e *env, checkpoint, name string, topics []string, assertPartial func(t *testing.T, ctx context.Context)) (resumeOut string) {
	return runResumeScenarioWith(t, e, checkpoint, name, topics, e.writeManifest, assertPartial, nil)
}

// runResumeScenarioWith is runResumeScenario with the manifest writer supplied
// by the caller, plus an optional assertFinal run after the standard end-state
// checks. Both return the resume run's output.
func runResumeScenarioWith(t *testing.T, e *env, checkpoint, name string, topics []string,
	writeManifest func(t *testing.T, name string, topics []string) string,
	assertPartial, assertFinal func(t *testing.T, ctx context.Context)) (resumeOut string) {
	ctx := context.Background()
	e.resetStaticRoute(t, ctx) // static: reclaim the whole route from any prior migration (no-op on dynamic)
	mani := writeManifest(t, name, topics)
	e.saveManifest(t, mani)

	e.snapshot(t, ctx, "before", "BEFORE "+name+" (expect mirrors ACTIVE, route → source)", topics)

	out1, err1 := e.runKCP(t, "kcp-run-1-interrupted.log", checkpoint, "migration", "execute", "--migration-yaml", mani)
	require.Errorf(t, err1, "the run interrupted at %q must exit non-zero (simulated abrupt exit)", checkpoint)
	require.Containsf(t, out1, "kill-point", "the non-zero exit at %q must be OUR seam firing, not a real failure", checkpoint)
	require.NotContains(t, strings.ToLower(out1), "migration complete", "the interrupted run must NOT have completed")

	e.snapshot(t, ctx, "after-interrupt", "AFTER interrupt at "+checkpoint, topics)
	assertPartial(t, ctx)

	out2, err2 := e.runKCP(t, "kcp-run-2-resume.log", "", "migration", "execute", "--migration-yaml", mani)
	require.NoErrorf(t, err2, "re-running the same manifest after an interrupt at %q must drive to completion", checkpoint)
	require.Contains(t, strings.ToLower(out2), "migration complete", "the resume run must report completion")

	e.snapshot(t, ctx, "after-resume", "AFTER resume (expect mirrors STOPPED, route → destination, fence cleared)", topics)

	afterCR := e.readCR(t, ctx)
	ms := e.mirrorStatus(t, ctx)
	for _, tp := range topics {
		require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED after the resumed migration completes", tp)
		require.Truef(t, e.isSwitchedToTarget(t, afterCR, tp), "%s must be switched to the target domain after completion", tp)
		require.Falsef(t, e.isFenced(t, afterCR, tp), "%s must NOT be fenced after completion — a switched route carries no kcp fence", tp)
	}
	if assertFinal != nil {
		assertFinal(t, ctx)
	}
	t.Logf("\n✅ RESULT: interrupted at %q, re-run drove to completion (mirrors STOPPED, route switched, fence cleared).", checkpoint)
	return out2
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
// (CC rejects that → fatal) — it must wait for STOPPED then switch. Slice
// tbm-topic-071..075.
func TestResume_InterruptDuringPromote(t *testing.T) {
	e := newEnv()
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
// STOPPED) but before the success banner (and, on static, before the
// offset-sync restore stage, a pass-through here with the pause off). The
// re-run's reconcile finds nothing to do, so it runs no state machine and
// still reports completion. Slice tbm-topic-066..070.
func TestResume_InterruptAfterSwitch(t *testing.T) {
	e := newEnv()
	topics := e.topicRange(66, 70)
	resumeOut := runResumeScenario(t, e, cpSwitched, "resume-switch", topics, func(t *testing.T, ctx context.Context) {
		cr := e.readCR(t, ctx)
		ms := e.mirrorStatus(t, ctx)
		for _, tp := range topics {
			require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED — switch already ran", tp)
			require.Falsef(t, e.isFenced(t, cr, tp), "%s must already be unfenced — switch cleared the fence", tp)
		}
	})
	requireNothingToDo(t, resumeOut)
}

// Kill after the OFFSET-SYNC PAUSE (static only; the dynamic FSM has no pause
// stage). The manifest sets spec.clusterLink.pauseConsumerOffsetSync with an
// "enabled" baseline, so right after fencing the static FSM disables the link's
// consumer.offset.sync.enable. Partial world = route fenced, mirrors still
// ACTIVE, offset sync off. The resume must re-apply the pause, complete, and
// restore offset sync to the baseline. setup.sh starts the static suite's link
// with offset sync on, so both the pause and the restore are observable. Slice
// tbm-topic-076..080.
func TestResume_InterruptAfterOffsetSyncPause(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: the dynamic FSM has no offset-sync pause stage")
	}
	topics := e.topicRange(76, 80)
	require.Equal(t, "true", e.linkOffsetSync(t, context.Background()),
		"setup.sh must start the static suite's cluster link with consumer offset sync on")

	runResumeScenarioWith(t, e, cpOffsetSyncPaused, "resume-offset-sync-pause", topics, e.writeManifestPausingOffsetSync,
		func(t *testing.T, ctx context.Context) {
			cr := e.readCR(t, ctx)
			ms := e.mirrorStatus(t, ctx)
			for _, tp := range topics {
				require.Truef(t, e.isFenced(t, cr, tp), "%s must be fenced — the pause runs after the fence step", tp)
				require.Equalf(t, "ACTIVE", ms[tp], "%s must still be ACTIVE — interrupt happened before promote", tp)
			}
			require.Equal(t, "false", e.linkOffsetSync(t, ctx),
				"the pause stage must have disabled consumer offset sync on the cluster link")
		},
		func(t *testing.T, ctx context.Context) {
			require.Equal(t, "true", e.linkOffsetSync(t, ctx),
				"the resumed run must restore consumer offset sync to the declared baseline (enabled)")
		})
}

// Kill after the SWITCH with the OFFSET-SYNC PAUSE on (static only). The switch
// has landed but the restore stage has not run, so the partial world is route
// switched, mirrors STOPPED, fence cleared, and consumer offset sync still off.
// The re-run's reconcile finds every topic migrated but the link still paused,
// so it owes the restore alone: the resume is not nothing-to-do, runs the state
// machine and sets offset sync back to the declared baseline (enabled). Slice
// tbm-topic-046..050.
func TestResume_InterruptAfterSwitch_OffsetSyncPaused(t *testing.T) {
	e := newEnv()
	if e.mode != "static" {
		t.Skip("static routes only: the dynamic FSM has no offset-sync pause stage")
	}
	topics := e.topicRange(46, 50)
	require.Equal(t, "true", e.linkOffsetSync(t, context.Background()),
		"setup.sh must start the static suite's cluster link with consumer offset sync on")

	resumeOut := runResumeScenarioWith(t, e, cpSwitched, "resume-switch-offset-sync-paused", topics, e.writeManifestPausingOffsetSync,
		func(t *testing.T, ctx context.Context) {
			cr := e.readCR(t, ctx)
			ms := e.mirrorStatus(t, ctx)
			for _, tp := range topics {
				require.Equalf(t, "STOPPED", ms[tp], "%s must be STOPPED — switch already ran", tp)
				require.Truef(t, e.isSwitchedToTarget(t, cr, tp), "%s must be switched — the interrupt came after the switch", tp)
				require.Falsef(t, e.isFenced(t, cr, tp), "%s must already be unfenced — switch cleared the fence", tp)
			}
			require.Equal(t, "false", e.linkOffsetSync(t, ctx),
				"offset sync must still be paused — the interrupt came before the restore stage")
		},
		func(t *testing.T, ctx context.Context) {
			require.Equal(t, "true", e.linkOffsetSync(t, ctx),
				"the resumed run must restore consumer offset sync to the declared baseline (enabled)")
		})
	requireStateMachineRan(t, resumeOut)
}

// midBatchPromoteSize is TestResume_InterruptMidBatch's promote batch size: the
// first promote request takes this many of the slice's topics, the rest stay
// ACTIVE until a later batch.
const midBatchPromoteSize = 2

// Kill MID-BATCH: the manifest sets spec.defaultPolicies.promoteBatchSize, so
// the first promote request takes midBatchPromoteSize of the 5 topics, and the
// kill fires right after it is accepted (killpoint.AfterPromoteAccepted).
// Partial world = route fenced, exactly midBatchPromoteSize mirrors promoting or
// promoted (PENDING_STOPPED/STOPPED), the rest still ACTIVE. Reconcile gives
// each topic its own verdict (AwaitStopped/SwitchOnly vs Migratable), so the
// resume must finish the already-promoting mirrors without re-promoting them,
// promote the ACTIVE ones, and switch all 5. Slice tbm-topic-081..085.
func TestResume_InterruptMidBatch(t *testing.T) {
	e := newEnv()
	topics := e.topicRange(81, 85)
	writeManifest := func(t *testing.T, name string, topics []string) string {
		return e.writeManifestWithPromoteBatchSize(t, name, topics, midBatchPromoteSize)
	}
	runResumeScenarioWith(t, e, cpPromoteAccepted, "resume-mid-batch", topics, writeManifest,
		func(t *testing.T, ctx context.Context) {
			cr := e.readCR(t, ctx)
			ms := e.mirrorStatus(t, ctx)
			var promoting, active []string
			for _, tp := range topics {
				require.Truef(t, e.isFenced(t, cr, tp), "%s must be fenced — promote runs after the fence step", tp)
				switch ms[tp] {
				case "PENDING_STOPPED", "STOPPED":
					promoting = append(promoting, tp)
				case "ACTIVE":
					active = append(active, tp)
				default:
					t.Fatalf("%s: unexpected mirror status %q after the mid-batch interrupt", tp, ms[tp])
				}
			}
			require.Lenf(t, promoting, midBatchPromoteSize,
				"exactly one batch of %d must have been promoted before the kill, got %v", midBatchPromoteSize, promoting)
			require.Lenf(t, active, len(topics)-midBatchPromoteSize,
				"the rest of the slice must still be ACTIVE, got %v", active)
		}, nil)
}
