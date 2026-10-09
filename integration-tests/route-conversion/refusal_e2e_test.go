//go:build e2e

package routeconversion

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/stretchr/testify/require"
)

// requireRefusedUnchanged asserts a run was refused and changed nothing: a
// non-zero exit naming every want, the route exactly as before, and no seed
// offsets on the destination.
func (e *env) requireRefusedUnchanged(t *testing.T, ctx context.Context, out string, err error, routeBefore map[string]any, seeds []seed, want ...string) {
	t.Helper()
	require.Error(t, err, "a refused conversion must exit non-zero")
	require.Contains(t, out, "reconcile plan refused")
	for _, w := range want {
		require.Contains(t, out, w)
	}
	require.True(t, jsonEqual(routeBefore, e.liveRoute(t, ctx)), "a refusal must leave the route exactly as it was")
	e.requireNoDestOffsets(t, seeds, "a refused run")
}

// A link mirror that is still ACTIVE refuses the conversion. The mirror is
// promoted at the end so the next test's reset finds the link fully promoted
// and routes the new topic to the destination.
func TestRefusal_UnpromotedLinkTopic(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds("unpromoted")...)
	// Unique per run: promotion is irreversible, so a re-run on the same
	// environment needs a topic that is not on the link yet.
	topic := fmt.Sprintf("%snew-%d", e.topicPrefix, time.Now().Unix())
	e.addMirror(t, ctx, topic)
	t.Cleanup(func() { e.promoteAll(t, context.Background()) })
	routeBefore := e.liveRoute(t, ctx)
	mani, _ := e.writeManifest(t, "refuse-unpromoted", minDetect)

	out, err := e.execute(t, "kcp-run-1-refused.log", mani, nil, nil)
	e.requireRefusedUnchanged(t, ctx, out, err, routeBefore, seeds, topic+" cannot be converted", "ACTIVE, not promoted")
	t.Logf("\n✅ RESULT: an ACTIVE link mirror refused the conversion and nothing changed.")
}

// A group with a commit on a topic that is not on the link refuses the
// conversion, naming the group and the topic.
func TestRefusal_GroupCommitsOutsideTheLink(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds("outside")...)
	e.seedGroups(t, seed{group: "outside-orphan", offsets: offsets{e.orphanTopic: {0: {Offset: 0}}}})
	routeBefore := e.liveRoute(t, ctx)
	mani, _ := e.writeManifest(t, "refuse-outside", minDetect)

	out, err := e.execute(t, "kcp-run-1-refused.log", mani, nil, nil)
	e.requireRefusedUnchanged(t, ctx, out, err, routeBefore, seeds,
		"outside-orphan ("+e.orphanTopic+")", "have committed offsets on topics that are not on the cluster link")
	t.Logf("\n✅ RESULT: a group committing outside the link refused the conversion and nothing changed.")
}

// An in-scope source group with live members on the destination refuses the
// conversion (split brain).
func TestRefusal_GroupActiveOnTheDestination(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds("active")...)
	group := seeds[1].group
	dir := e.workDir(t)
	c := e.startConsumer(t, dir, e.destBootstrap, e.destClientProps(t, dir), group, e.topic(3), "dest", false)
	defer c.stop(t)
	e.waitGroupState(t, destCluster, group, "Stable", 1, 2*time.Minute)
	routeBefore := e.liveRoute(t, ctx)
	mani, _ := e.writeManifest(t, "refuse-active", minDetect)

	out, err := e.execute(t, "kcp-run-1-refused.log", mani, nil, nil)
	require.Error(t, err)
	require.Contains(t, out, group+" (Stable) exist on the source and already have members on the destination")
	require.True(t, jsonEqual(routeBefore, e.liveRoute(t, ctx)), "a refusal must leave the route exactly as it was")
	// The live destination member commits its own offsets for seeds[1], so only
	// seeds[0] is checked for the absence of kcp's sync.
	e.requireNoDestOffsets(t, seeds[:1], "a refused run")
	t.Logf("\n✅ RESULT: an in-scope group live on the destination refused the conversion and nothing changed.")
}

// A client committing straight to the source while the fence is up is caught
// by verify_fence's detection window: the run fails, the fence comes off, and
// nothing reaches the destination.
func TestRefusal_DirectCommitDuringTheWindow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds("direct")...)
	detect := 30 * time.Second
	mani, _ := e.writeManifest(t, "refuse-direct", detect)

	// The commit is made when kcp announces its window: after the first
	// snapshot, so the second one sees the change. onLine runs on the output
	// reader's goroutine, so it reports through a channel instead of failing
	// the test itself.
	var once sync.Once
	committed := make(chan error, 1)
	watching := fmt.Sprintf(watchingLine, detect)
	gc := e.groupClient(t, sourceCluster)
	onLine := func(line string) {
		if !strings.Contains(line, watching) {
			return
		}
		once.Do(func() {
			raw, err := e.svc.GetGatewayYAML(ctx, e.namespace, e.gateway)
			if err != nil {
				committed <- fmt.Errorf("read the gateway CR when the window opened: %w", err)
				return
			}
			if !HasConvertFence(FindRoute(raw, e.route)) {
				committed <- fmt.Errorf("kcp's fence was not on the route when the window opened")
				return
			}
			committed <- gc.CommitGroupOffsets(seeds[1].group, offsets{e.topic(3): {0: client.CommittedOffset{Offset: 9}}})
		})
	}

	out, err := e.execute(t, "kcp-run-1-fails.log", mani, nil, onLine)
	select {
	case cerr := <-committed:
		require.NoError(t, cerr, "the direct commit to the source")
	default:
		t.Fatalf("kcp never printed %q, so no direct commit was made", watching)
	}
	require.Error(t, err, "a direct commit during the window must fail the run")
	require.Contains(t, out, rogueCommitsError)
	require.Contains(t, out, rogueCommitsReason)
	require.Contains(t, out, gatewayUnfenced)
	e.requirePostTBM(t, ctx, "the fence must come off after the direct-commit failure")
	e.requireNoDestOffsets(t, seeds, "a failed run")
	t.Logf("\n✅ RESULT: a direct commit during the window failed the run, the fence came off, and nothing was synced.")
}

// A refusal while kcp's fence is already up (left by an interrupted run) warns
// that the route still blocks every topic and leaves the fence; once the cause
// is fixed a re-run converts the route.
func TestRefusal_WithKcpFenceAlreadyUp(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.reset(t, ctx)
	seeds := e.seedGroups(t, e.defaultSeeds("fenceup")...)
	orphan := e.seedGroups(t, seed{group: "fenceup-orphan", offsets: offsets{e.orphanTopic: {0: {Offset: 0}}}})
	security := e.liveRoute(t, ctx)["security"]
	e.fenceByHand(t, ctx)
	routeBefore := e.liveRoute(t, ctx)
	mani, id := e.writeManifest(t, "refuse-fenceup", minDetect)

	out, err := e.execute(t, "kcp-run-1-refused.log", mani, nil, nil)
	e.requireRefusedUnchanged(t, ctx, out, err, routeBefore, seeds,
		fmt.Sprintf("route %q still carries kcp's conversion fence", e.route), "blocking every topic")
	e.requireFenced(t, ctx, "a refusal must keep kcp's fence")

	admin := e.admin(t, sourceCluster)
	require.NoError(t, admin.DeleteConsumerGroup(orphan[0].group), "delete the group committing outside the link")
	out, err = e.execute(t, "kcp-run-2-rerun.log", mani, nil, nil)
	requireCompleted(t, out, err, id)
	e.requireConvertedRoute(t, ctx, security)
	e.requireSynced(t, seeds)
	t.Logf("\n✅ RESULT: the refusal kept kcp's fence and warned; with the cause removed, a re-run converted the route.")
}
