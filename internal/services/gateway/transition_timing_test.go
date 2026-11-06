package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/tracing"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func progress(atWant, ready int, mech RolloutMechanism, elapsed time.Duration, converged bool) ConfigWaitProgress {
	return ConfigWaitProgress{Want: "c1", PodsTotal: 3, PodsReady: ready, PodsAtWant: atWant, Mechanism: mech, Elapsed: elapsed, Converged: converged}
}

// runTiming feeds the progress sequence to a timing on a fresh span, ends both, and returns the span.
func runTiming(t *testing.T, seq ...ConfigWaitProgress) sdktrace.ReadOnlySpan {
	t.Helper()
	rec := tracingtest.Install(t)
	_, span := tracing.Start(context.Background(), tracerName, "test.span")
	timing := newTransitionTiming(span)
	for _, p := range seq {
		timing.observePods(p)
	}
	timing.end()
	span.End()
	return tracingtest.Find(t, tracingtest.Spans(rec), "test.span")
}

func eventsNamed(s sdktrace.ReadOnlySpan, name string) []sdktrace.Event {
	var out []sdktrace.Event
	for _, e := range s.Events() {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

func eventAttr(e sdktrace.Event, key string) (int64, string, bool) {
	for _, kv := range e.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsInt64(), kv.Value.String(), true
		}
	}
	return 0, "", false
}

var standardSequence = []ConfigWaitProgress{
	progress(0, 3, MechanismNoRollObserved, 2*time.Second, false),
	progress(0, 3, MechanismNoRollObserved, 4*time.Second, false),
	progress(1, 3, MechanismNoRollObserved, 6*time.Second, false),
	progress(1, 3, MechanismNoRollObserved, 8*time.Second, false),
	progress(3, 3, MechanismNoRollObserved, 10*time.Second, true),
}

func TestTiming_RecordsAnEventOnlyWhenThePodCountChanges(t *testing.T) {
	s := runTiming(t, standardSequence...)
	events := eventsNamed(s, "gateway.config_progress")
	require.Len(t, events, 3)
	for _, e := range events {
		for _, k := range []string{"kcp.gateway.pods_at_config", "kcp.gateway.pods_ready", "kcp.gateway.pods_total", "kcp.gateway.observed_mechanism", "kcp.gateway.elapsed_ms"} {
			_, _, ok := eventAttr(e, k)
			assert.True(t, ok, "event missing %s", k)
		}
	}
	at, _, _ := eventAttr(events[0], "kcp.gateway.pods_at_config")
	assert.Equal(t, int64(0), at)
	at, _, _ = eventAttr(events[1], "kcp.gateway.pods_at_config")
	assert.Equal(t, int64(1), at)
	ms, _, _ := eventAttr(events[2], "kcp.gateway.elapsed_ms")
	assert.Equal(t, int64(10000), ms)
}

func TestTiming_EndSetsFirstAndAllPodTimes(t *testing.T) {
	s := runTiming(t, standardSequence...)
	first, ok := tracingtest.Attr(s, "kcp.gateway.first_pod_ms")
	require.True(t, ok)
	assert.Equal(t, int64(6000), first.AsInt64())
	all, ok := tracingtest.Attr(s, "kcp.gateway.all_pods_ms")
	require.True(t, ok)
	assert.Equal(t, int64(10000), all.AsInt64())
	mech, ok := tracingtest.Attr(s, "kcp.gateway.observed_mechanism")
	require.True(t, ok)
	assert.Equal(t, "no-roll-observed", mech.AsString())
}

func TestTiming_PodRollIsRecorded(t *testing.T) {
	s := runTiming(t,
		progress(0, 3, MechanismNoRollObserved, 2*time.Second, false),
		progress(1, 2, MechanismPodRoll, 20*time.Second, false),
		progress(3, 3, MechanismPodRoll, 60*time.Second, true),
	)
	mech, _ := tracingtest.Attr(s, "kcp.gateway.observed_mechanism")
	assert.Equal(t, "pod-roll", mech.AsString())
}

func TestTiming_NoConvergenceLeavesAllPodsAbsent(t *testing.T) {
	s := runTiming(t,
		progress(0, 3, MechanismNoRollObserved, 2*time.Second, false),
		progress(2, 3, MechanismNoRollObserved, 4*time.Second, false),
	)
	_, ok := tracingtest.Attr(s, "kcp.gateway.all_pods_ms")
	assert.False(t, ok, "all_pods_ms must be absent, not 0, when the wait never converged")
	first, ok := tracingtest.Attr(s, "kcp.gateway.first_pod_ms")
	require.True(t, ok)
	assert.Equal(t, int64(4000), first.AsInt64())
}
