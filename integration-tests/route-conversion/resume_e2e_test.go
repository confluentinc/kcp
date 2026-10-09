//go:build e2e

package routeconversion

import (
	"context"
	"fmt"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migration/d2s"
	"github.com/stretchr/testify/require"
)

// resumeCase is one kill point: the d2s state run 1 stops after, what the world
// must look like then, and what run 2's output must show besides completing.
type resumeCase struct {
	state       string
	afterRun1   func(t *testing.T, ctx context.Context, e *env, seeds []seed)
	run2Shows   []string
	nothingToDo bool // run 1 already switched, so run 2 finds nothing to do
}

// runResumeCase: reset, seed, run 1 interrupted at c.state (non-zero exit),
// record and check the live world, run 2 with no seam, then the baseline's end
// state.
func runResumeCase(t *testing.T, name string, c resumeCase) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds(name)...)
	security := e.liveRoute(t, ctx)["security"]
	mani, id := e.writeManifest(t, "resume-"+name, minDetect)
	e.snapshot(t, ctx, "before", "BEFORE (expect the post-TBM route, no destination offsets)", seeds)

	out, err := e.execute(t, "kcp-run-1-interrupted.log", mani, cancelAfter(c.state), nil)
	requireInterrupted(t, out, err)
	e.snapshot(t, ctx, "after-interrupt", "AFTER the interrupt at "+c.state, seeds)
	c.afterRun1(t, ctx, e, seeds)

	out, err = e.execute(t, "kcp-run-2-resume.log", mani, nil, nil)
	if c.nothingToDo {
		requireNothingToDo(t, out, err, id)
	} else {
		requireCompleted(t, out, err, id)
	}
	for _, want := range c.run2Shows {
		require.Contains(t, out, want)
	}
	e.snapshot(t, ctx, "after-resume", "AFTER the resume (expect static on the destination, offsets synced)", seeds)
	e.requireConvertedRoute(t, ctx, security)
	e.requireSynced(t, seeds)
	t.Logf("\n✅ RESULT: interrupted after %s, the re-run finished the conversion with the baseline's end state.", c.state)
}

func TestResume_AfterInitialized(t *testing.T) {
	runResumeCase(t, "initialized", resumeCase{
		state: d2s.StateInitialized,
		afterRun1: func(t *testing.T, ctx context.Context, e *env, seeds []seed) {
			e.requirePostTBM(t, ctx, "interrupted before the fence step")
			e.requireNoDestOffsets(t, seeds, "interrupted before the sync")
		},
		run2Shows: []string{fenceAppliedLine},
	})
}

func TestResume_AfterFenced(t *testing.T) {
	runResumeCase(t, "fenced", resumeCase{
		state: d2s.StateFenced,
		afterRun1: func(t *testing.T, ctx context.Context, e *env, seeds []seed) {
			e.requireFenced(t, ctx, "interrupted after the fence step")
			e.requireNoDestOffsets(t, seeds, "interrupted before the sync")
		},
		// Reconcile sees kcp's fence up and the fence step sets the same rules again.
		run2Shows: []string{fenceAppliedLine},
	})
}

func TestResume_AfterFenceVerified(t *testing.T) {
	runResumeCase(t, "fence-verified", resumeCase{
		state: d2s.StateFenceVerified,
		afterRun1: func(t *testing.T, ctx context.Context, e *env, seeds []seed) {
			e.requireFenced(t, ctx, "interrupted after verify_fence")
			e.requireNoDestOffsets(t, seeds, "interrupted before the sync")
		},
		// Nothing carries over between runs: run 2 takes a fresh detection window.
		run2Shows: []string{fmt.Sprintf(watchingLine, minDetect)},
	})
}

func TestResume_AfterOffsetsSynced(t *testing.T) {
	runResumeCase(t, "offsets-synced", resumeCase{
		state: d2s.StateOffsetsSynced,
		afterRun1: func(t *testing.T, ctx context.Context, e *env, seeds []seed) {
			e.requireFenced(t, ctx, "interrupted after sync_offsets")
			e.requireSynced(t, seeds)
		},
		// The groups run 1 synced sit Empty on the destination: run 2 warns it
		// will overwrite them, then syncs the same values again.
		run2Shows: []string{idleGroupWarning, fmt.Sprintf(watchingLine, minDetect)},
	})
}

func TestResume_AfterSwitched(t *testing.T) {
	runResumeCase(t, "switched", resumeCase{
		state: d2s.StateSwitched,
		afterRun1: func(t *testing.T, ctx context.Context, e *env, seeds []seed) {
			require.True(t, StaticOn(e.liveRoute(t, ctx), e.domains.Dest, e.domains.DestID),
				"interrupted after the switch: the route must already be static")
			e.requireSynced(t, seeds)
		},
		nothingToDo: true,
	})
}
