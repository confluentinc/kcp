package d2s

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verify runs VerifyFence on w with the test policy, rendering refusals into w.rendered as plain text.
func verify(t *testing.T, w *world) (*D2SActions, error) {
	t.Helper()
	color.NoColor = true
	a := NewD2SActions(&mockGatewayService{}, w.deps())
	a.reporter = quietReporter()
	return a, a.VerifyFence(context.Background(), testConfig(), testPolicy())
}

func TestVerifyFence_ACleanPassHandsTheSecondSnapshotOn(t *testing.T) {
	w := cleanWorld()

	a, err := verify(t, w)

	require.NoError(t, err)
	assert.Equal(t, w.src.rounds[0], a.handoff, "snapshot 2, restricted to the in-scope groups, is what sync copies")
	assert.Equal(t, 1, w.gathers, "the facts are gathered afresh after the fence")
	assert.Equal(t, 2, w.src.lists, "a fresh source listing for each snapshot")
	assert.Equal(t, []time.Duration{10 * time.Second}, w.waits, "the policy's window, waited once")
	assert.Equal(t, 1, w.dst.groupLists, "the destination is listed again after the wait")
	assert.Empty(t, w.rendered.String(), "nothing is rendered when nothing refuses")
}

// Review Focus 1: nothing committed anywhere is a convertible route, not a missing hand-off.
func TestVerifyFence_NoCommittingGroupsHandsOverAnEmptySnapshot(t *testing.T) {
	w := cleanWorld()
	w.src.listings = [][]string{{"idle-app"}}
	w.src.rounds = []groupoffsets.Snapshot{{}}

	a, err := verify(t, w)

	require.NoError(t, err)
	require.NotNil(t, a.handoff, "an empty hand-off is still a hand-off: sync_offsets must run and write nothing")
	assert.Empty(t, a.handoff)
}

// Review Focus 4 (the err assertions): the reason reaches kcp.log through the error.
func TestVerifyFence_RefusesOnTheGatheredFactsBeforeTheFirstSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check string
		edit  func(*migplan.ConvertFacts)
	}{
		{"the destination cannot write offsets", reconcile.TargetOffsetCommitCheckName, func(f *migplan.ConvertFacts) { f.TargetCanCommitOffsets = false }},
		{"the source cannot list every group", reconcile.SourceGroupVisibilityCheckName, func(f *migplan.ConvertFacts) { f.SourceCanDescribeGroups = false }},
		{"the destination cannot describe every topic", reconcile.TargetTopicVisibilityCheckName, func(f *migplan.ConvertFacts) { f.TargetCanDescribeTopics = false }},
		{"offset sync was turned on since reconcile", "consumer offset sync disabled on link", func(f *migplan.ConvertFacts) { f.Link.OffsetSyncEnabled = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := cleanWorld()
			tc.edit(w.facts)

			a, err := verify(t, w)

			require.ErrorIs(t, err, ErrVerifyRefused)
			assert.Contains(t, err.Error(), tc.check+": ", "the error carries the failed check and its detail")
			assert.Contains(t, w.rendered.String(), "✗ "+tc.check, "rendered as --dry-run renders it")
			assert.Zero(t, w.src.lists, "no snapshot is taken on listings a refused check says may be partial")
			assert.Nil(t, a.handoff)
		})
	}
}

func TestVerifyFence_RefusesOnTheLinkAndGroupChecks(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
		edit func(*world)
	}{
		{"a link topic is not promoted", "its mirror is ACTIVE, not promoted", func(w *world) {
			w.facts.Link.LinkMirrors[0].State = reconcile.MirrorActive
			w.facts.Link.LinkMirrors[0].Status = "ACTIVE"
		}},
		{"the partition counts differ", "6 partitions on the destination but 3", func(w *world) {
			w.facts.Partitions.Target["orders"] = 6
		}},
		{"a group commits outside the link", "audit-app (audit)", func(w *world) {
			w.src.listings = [][]string{{"orders-app", "audit-app"}}
			w.src.rounds[0]["audit-app"] = groupoffsets.Offsets{"audit": {0: {Offset: 1}}}
		}},
		{"an in-scope group is active on the destination", "orders-app (Stable)", func(w *world) {
			w.facts.TargetGroupListing = []types.ConsumerGroupListing{{GroupID: "orders-app", State: "Stable"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := cleanWorld()
			tc.edit(w)

			a, err := verify(t, w)

			require.ErrorIs(t, err, ErrVerifyRefused)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, w.rendered.String(), tc.want)
			assert.Empty(t, w.waits, "a refusal on snapshot 1 comes before the wait")
			assert.Equal(t, 1, w.src.lists)
			assert.Nil(t, a.handoff)
		})
	}
}

func TestVerifyFence_ADirectCommitDuringTheWindowAborts(t *testing.T) {
	w := cleanWorld()
	w.src.rounds = append(w.src.rounds, groupoffsets.Snapshot{
		"orders-app": {"orders": {0: {Offset: 6}, 1: {Offset: 7, Metadata: "stream-time=1"}}},
	})

	a, err := verify(t, w)

	require.ErrorIs(t, err, groupoffsets.ErrRogueCommits)
	assert.NotErrorIs(t, err, ErrVerifyRefused, "a rogue commit is its own cause, not a check refusal")
	assert.Contains(t, err.Error(), "group orders-app topic orders partition 0: 5 -> 6")
	assert.Zero(t, w.dst.groupLists, "nothing after the diff runs")
	assert.Nil(t, a.handoff)
}

func TestVerifyFence_AGroupThatStartsCommittingDuringTheWindowAborts(t *testing.T) {
	w := cleanWorld()
	w.src.listings = [][]string{{"orders-app"}, {"orders-app", "late-app"}}
	w.src.rounds = append(w.src.rounds, groupoffsets.Snapshot{
		"orders-app": w.src.rounds[0]["orders-app"],
		"late-app":   {"orders": {0: {Offset: 1}}},
	})

	_, err := verify(t, w)

	require.ErrorIs(t, err, groupoffsets.ErrRogueCommits)
	assert.Contains(t, err.Error(), "group late-app topic orders partition 0: new commit at 1")
}

// Review Focus 3, decision 23: a destination group that becomes active during the wait is caught, and the
// report shows that check once, as the failure.
func TestVerifyFence_SplitBrainSeenOnlyAfterTheWaitRefuses(t *testing.T) {
	w := cleanWorld()
	w.dst.groups = []types.ConsumerGroupListing{{GroupID: "orders-app", State: "Stable"}}

	a, err := verify(t, w)

	require.ErrorIs(t, err, ErrVerifyRefused)
	assert.Contains(t, err.Error(), reconcile.GroupSplitBrainCheckName+": ")
	assert.Equal(t, []time.Duration{10 * time.Second}, w.waits, "seen only after the wait")
	out := w.rendered.String()
	assert.Equal(t, 1, strings.Count(out, reconcile.GroupSplitBrainCheckName), "the fresh result replaces the first check's line:\n%s", out)
	assert.Contains(t, out, "✗ "+reconcile.GroupSplitBrainCheckName)
	assert.Contains(t, out, "orders-app (Stable)")
	assert.Nil(t, a.handoff)
}

func TestVerifyFence_ReadFailuresAreErrorsNotRefusals(t *testing.T) {
	boom := errors.New("boom")
	for name, edit := range map[string]func(*world){
		"gathering the facts":        func(w *world) { w.gatherErr = boom },
		"listing the source groups":  func(w *world) { w.src.listErr = boom },
		"fetching committed offsets": func(w *world) { w.src.fetchErr = boom },
		"listing destination groups": func(w *world) { w.dst.groupsErr = boom },
	} {
		t.Run(name, func(t *testing.T) {
			w := cleanWorld()
			edit(w)

			a, err := verify(t, w)

			require.ErrorIs(t, err, boom)
			assert.NotErrorIs(t, err, ErrVerifyRefused)
			assert.NotErrorIs(t, err, groupoffsets.ErrRogueCommits)
			assert.Empty(t, w.rendered.String(), "a read failure is not a verdict, so there is no report")
			assert.Nil(t, a.handoff)
		})
	}
	t.Run("the facts carry no link status", func(t *testing.T) {
		w := cleanWorld()
		w.facts.Link = nil

		_, err := verify(t, w)

		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrVerifyRefused)
	})
	t.Run("the wait is cancelled", func(t *testing.T) {
		w := cleanWorld()
		deps := w.deps()
		deps.Wait = func(context.Context, time.Duration) error { return context.Canceled }
		a := NewD2SActions(&mockGatewayService{}, deps)
		a.reporter = quietReporter()

		err := a.VerifyFence(context.Background(), testConfig(), testPolicy())

		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, a.handoff)
	})
}

func TestVerifyFence_AZeroWindowIsAnError(t *testing.T) {
	w := cleanWorld()
	a := NewD2SActions(&mockGatewayService{}, w.deps())
	a.reporter = quietReporter()

	err := a.VerifyFence(context.Background(), testConfig(), Policy{Workers: 2})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "detection window")
	assert.Zero(t, w.gathers, "nothing is read when the check could not run")
}
