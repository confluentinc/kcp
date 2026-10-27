package offset

import (
	"context"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedProvider map[string]map[int32]int64

func (p fixedProvider) GetMany(context.Context, []string) (map[string]map[int32]int64, error) {
	return p, nil
}

type discardReporter struct{}

func (discardReporter) Detail(string, ...any) {}

func TestTracing_FetchSourceAndDestinationOffsets_IsOneSweepSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	p := fixedProvider{"orders": {0: 1}}
	_, _, err := FetchSourceAndDestinationOffsets(context.Background(), p, p, []string{"orders"})
	require.NoError(t, err)

	spans := tracingtest.Spans(rec)
	sweep := tracingtest.Find(t, spans, "offset.sweep")
	n, _ := tracingtest.Attr(sweep, "kcp.topics")
	assert.Equal(t, int64(1), n.AsInt64())
	tracingtest.AssertNoValues(t, spans, "orders")
}

func TestTracing_DetectUnroutedProducers_SpansTheWindow(t *testing.T) {
	rec := tracingtest.Install(t)
	p := fixedProvider{"orders": {0: 1}}
	require.NoError(t, DetectUnroutedProducers(context.Background(), p, discardReporter{}, []string{"orders"}, time.Millisecond))

	spans := tracingtest.Spans(rec)
	detect := tracingtest.Find(t, spans, "offset.detect_unrouted_producers")
	assert.Equal(t, []string{"wait.unrouted_producers_window"}, tracingtest.Names(tracingtest.Children(spans, detect)))
	v, _ := tracingtest.Attr(detect, "kcp.violations")
	assert.Equal(t, int64(0), v.AsInt64())
}
