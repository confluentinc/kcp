package groupoffsets

import (
	"context"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracing_DetectRogueCommits_SpansWaitAndSnapshot(t *testing.T) {
	rec := tracingtest.Install(t)
	offs := Offsets{"orders": {0: at(5)}}
	factory, _, _ := factoryFor(&fakeFetcher{offsets: map[string]Offsets{"orders-app": offs}})
	first := Snapshot{"orders-app": offs}
	lister := func(context.Context) ([]string, error) { return []string{"orders-app"}, nil }

	_, err := DetectRogueCommits(context.Background(), first, lister, factory, 2, time.Millisecond, Sleep)
	require.NoError(t, err)

	spans := tracingtest.Spans(rec)
	detect := tracingtest.Find(t, spans, "groupoffsets.detect_rogue_commits")
	assert.Equal(t, []string{"wait.rogue_commit_window", "groupoffsets.snapshot"},
		tracingtest.Names(tracingtest.Children(spans, detect)))
	snap := tracingtest.Find(t, spans, "groupoffsets.snapshot")
	n, _ := tracingtest.Attr(snap, "kcp.groups.with_commits")
	assert.Equal(t, int64(1), n.AsInt64())
	tracingtest.AssertNoValues(t, spans, "orders-app", "orders")
}
