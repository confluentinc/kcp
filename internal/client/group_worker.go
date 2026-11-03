package client

import (
	"context"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/confluentinc/kcp/internal/tracing"
)

// tracerName is the instrumentation scope of this package's spans.
const tracerName = "kcp/client"

// GroupWorker is one worker of a fan-out over consumer groups (a conversion's snapshots and apply, and
// reconcile's committed-topics fetch), spanned as kafka.group_worker. Each group costs the worker two round
// trips: finding the group's coordinator, then the request itself (OffsetFetch or OffsetCommit). The client's
// group calls find the worker in their context and add both up on it, every attempt included, so the span
// shows where the worker's time went without a span per group, which at tens of thousands of groups would
// swamp the trace. A GroupWorker belongs to one goroutine, so it takes no lock.
type GroupWorker struct {
	span         trace.Span
	groups       int
	api          string
	lookups      tally
	requests     tally
	coordinators map[string]struct{}
}

// tally adds up calls: how many, their total time and the slowest.
type tally struct {
	count      int
	total, max time.Duration
}

func (t *tally) add(d time.Duration) {
	t.count++
	t.total += d
	if d > t.max {
		t.max = d
	}
}

func (t tally) attributes(prefix string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int(prefix+".count", t.count),
		attribute.Float64(prefix+".total_ms", milliseconds(t.total)),
		attribute.Float64(prefix+".max_ms", milliseconds(t.max)),
	}
}

func milliseconds(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

type groupWorkerKey struct{}

// StartGroupWorker opens worker i's kafka.group_worker span, a child of the span in ctx, and returns a context
// that carries the worker to the client's group calls.
func StartGroupWorker(ctx context.Context, i int) (context.Context, *GroupWorker) {
	ctx, span := tracing.Start(ctx, tracerName, "kafka.group_worker", attribute.Int("kcp.worker", i))
	w := &GroupWorker{span: span, coordinators: map[string]struct{}{}}
	return context.WithValue(ctx, groupWorkerKey{}, w), w
}

// GroupDone counts one group the worker finished.
func (w *GroupWorker) GroupDone() { w.groups++ }

// End records the worker's totals on its span and closes it, recording err when it is non-nil: kcp.groups (groups
// finished), kcp.coordinators (distinct coordinator brokers it asked), kcp.coordinator_lookup.* and
// kcp.request.* (count, total_ms, max_ms), and kcp.request.api once it has sent a request.
func (w *GroupWorker) End(err error) {
	attrs := []attribute.KeyValue{
		attribute.Int("kcp.groups", w.groups),
		attribute.Int("kcp.coordinators", len(w.coordinators)),
	}
	attrs = append(attrs, w.lookups.attributes("kcp.coordinator_lookup")...)
	attrs = append(attrs, w.requests.attributes("kcp.request")...)
	if w.api != "" {
		attrs = append(attrs, attribute.String("kcp.request.api", w.api))
	}
	w.span.SetAttributes(attrs...)
	tracing.End(w.span, err)
}

func groupWorkerFrom(ctx context.Context) *GroupWorker {
	w, _ := ctx.Value(groupWorkerKey{}).(*GroupWorker)
	return w
}

// findCoordinator finds group's coordinator with lookup, timing it on ctx's worker, if any.
func findCoordinator(ctx context.Context, lookup func(group string) (*sarama.Broker, error), group string) (*sarama.Broker, error) {
	start := time.Now()
	b, err := lookup(group)
	if w := groupWorkerFrom(ctx); w != nil {
		w.lookups.add(time.Since(start))
		if err == nil {
			w.coordinators[b.Addr()] = struct{}{}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("finding the coordinator of consumer group %s: %w", group, err)
	}
	return b, nil
}

// timeRequest runs one group request, api naming it (OffsetFetch, OffsetCommit), timing it on ctx's worker, if
// any.
func timeRequest(ctx context.Context, api string, req func() error) error {
	start := time.Now()
	err := req()
	if w := groupWorkerFrom(ctx); w != nil {
		w.api = api
		w.requests.add(time.Since(start))
	}
	return err
}
