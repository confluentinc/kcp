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

// runTiming feeds the progress sequence to a timing on a fresh span, ends both, and returns the
// span. Each progress is seen at origin plus its Elapsed.
func runTiming(t *testing.T, seq ...ConfigWaitProgress) sdktrace.ReadOnlySpan {
	t.Helper()
	rec := tracingtest.Install(t)
	_, span := tracing.Start(context.Background(), tracerName, "test.span")
	timing := newTransitionTiming(span, patchedAt)
	for _, p := range seq {
		timing.observePods(p, patchedAt.Add(p.Elapsed))
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

var patchedAt = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return patchedAt.Add(d) }

// runOperatorTiming arms a timing with the baseline, feeds it the polls, ends it, and returns the span.
func runOperatorTiming(t *testing.T, baseline HotReloadSignals, polls ...struct {
	s   HotReloadSignals
	now time.Time
}) sdktrace.ReadOnlySpan {
	t.Helper()
	rec := tracingtest.Install(t)
	_, span := tracing.Start(context.Background(), tracerName, "test.span")
	timing := newTransitionTiming(span, patchedAt)
	timing.watchOperator(baseline)
	for _, p := range polls {
		timing.observeOperator(p.s, p.now)
	}
	timing.end()
	span.End()
	return tracingtest.Find(t, tracingtest.Spans(rec), "test.span")
}

type poll = struct {
	s   HotReloadSignals
	now time.Time
}

func cond(status, reason string, ltt time.Time) *HotReloadCondition {
	return &HotReloadCondition{Status: status, Reason: reason, LastTransitionTime: ltt}
}

func ev(reason string, count int32, last time.Time) OperatorEvent {
	return OperatorEvent{Name: "gw." + reason, Reason: reason, Count: count, LastSeen: last}
}

func intAttr(t *testing.T, s sdktrace.ReadOnlySpan, key string) (int64, bool) {
	t.Helper()
	v, ok := tracingtest.Attr(s, key)
	return v.AsInt64(), ok
}

func TestTiming_CanaryStartThenSucceeded(t *testing.T) {
	earlier := patchedAt.Add(-10 * time.Minute)
	baseline := HotReloadSignals{
		Condition: cond("True", "Succeeded", earlier),
		Events:    []OperatorEvent{ev("HotReloadSucceeded", 4, earlier)},
	}
	s := runOperatorTiming(t, baseline,
		poll{HotReloadSignals{Condition: cond("Unknown", "InProgress", at(2*time.Second)), Events: baseline.Events}, at(3 * time.Second)},
		poll{HotReloadSignals{Condition: cond("True", "Succeeded", at(8*time.Second)), Events: []OperatorEvent{ev("HotReloadSucceeded", 5, at(8*time.Second))}}, at(9 * time.Second)},
	)
	events := eventsNamed(s, "gateway.hot_reload_status")
	require.Len(t, events, 2)
	_, reason, _ := eventAttr(events[0], "kcp.gateway.hot_reload.reason")
	assert.Equal(t, "InProgress", reason)
	_, reason, _ = eventAttr(events[1], "kcp.gateway.hot_reload.reason")
	assert.Equal(t, "HotReloadSucceeded", reason)
	for _, e := range events {
		_, _, ok := eventAttr(e, "kcp.gateway.hot_reload.server_time")
		assert.True(t, ok)
	}
	started, ok := intAttr(t, s, "kcp.gateway.hot_reload.canary_started_ms")
	require.True(t, ok)
	assert.Equal(t, int64(3000), started)
	promoted, ok := intAttr(t, s, "kcp.gateway.hot_reload.promoted_ms")
	require.True(t, ok)
	assert.Equal(t, int64(9000), promoted)
	outcome, _ := tracingtest.Attr(s, "kcp.gateway.hot_reload.outcome")
	assert.Equal(t, "HotReloadSucceeded", outcome.AsString())
}

func TestTiming_BackToBackAppliedDirectly(t *testing.T) {
	earlier := patchedAt.Add(-time.Minute)
	direct := cond("True", "AppliedDirectly", patchedAt.Add(-time.Hour))
	baseline := HotReloadSignals{Condition: direct, Events: []OperatorEvent{ev("HotReloadAppliedDirectly", 2, earlier)}}
	s := runOperatorTiming(t, baseline,
		poll{baseline, at(2 * time.Second)},
		poll{HotReloadSignals{Condition: direct, Events: []OperatorEvent{ev("HotReloadAppliedDirectly", 3, at(3*time.Second))}}, at(4 * time.Second)},
	)
	promoted, ok := intAttr(t, s, "kcp.gateway.hot_reload.promoted_ms")
	require.True(t, ok)
	assert.Equal(t, int64(4000), promoted)
	outcome, _ := tracingtest.Attr(s, "kcp.gateway.hot_reload.outcome")
	assert.Equal(t, "HotReloadAppliedDirectly", outcome.AsString())
	_, ok = tracingtest.Attr(s, "kcp.gateway.hot_reload.canary_started_ms")
	assert.False(t, ok)
}

func TestTiming_LeftoverSucceededIsNotThisChange(t *testing.T) {
	earlier := patchedAt.Add(-time.Minute)
	baseline := HotReloadSignals{
		Condition: cond("True", "Succeeded", earlier),
		Events:    []OperatorEvent{ev("HotReloadSucceeded", 4, earlier)},
	}
	s := runOperatorTiming(t, baseline, poll{baseline, at(2 * time.Second)}, poll{baseline, at(4 * time.Second)})
	assert.Empty(t, eventsNamed(s, "gateway.hot_reload_status"))
	for _, k := range []string{"kcp.gateway.hot_reload.canary_started_ms", "kcp.gateway.hot_reload.promoted_ms", "kcp.gateway.hot_reload.outcome"} {
		_, ok := tracingtest.Attr(s, k)
		assert.False(t, ok, k)
	}
}

func TestTiming_LastSeenMovedCountsAsNew(t *testing.T) {
	earlier := patchedAt.Add(-time.Minute)
	baseline := HotReloadSignals{Events: []OperatorEvent{ev("HotReloadAppliedDirectly", 2, earlier)}}
	s := runOperatorTiming(t, baseline,
		poll{HotReloadSignals{Events: []OperatorEvent{ev("HotReloadAppliedDirectly", 2, at(time.Second))}}, at(2 * time.Second)},
	)
	promoted, ok := intAttr(t, s, "kcp.gateway.hot_reload.promoted_ms")
	require.True(t, ok)
	assert.Equal(t, int64(2000), promoted)
}

func TestTiming_FailedIsRecorded(t *testing.T) {
	s := runOperatorTiming(t, HotReloadSignals{},
		poll{HotReloadSignals{Events: []OperatorEvent{ev("HotReloadFailed", 1, at(5*time.Second))}}, at(6 * time.Second)},
	)
	outcome, ok := tracingtest.Attr(s, "kcp.gateway.hot_reload.outcome")
	require.True(t, ok)
	assert.Equal(t, "HotReloadFailed", outcome.AsString())
	_, ok = tracingtest.Attr(s, "kcp.gateway.hot_reload.promoted_ms")
	assert.False(t, ok)
}

func TestTiming_NotWatchingLeavesHotReloadAbsent(t *testing.T) {
	s := runTiming(t, standardSequence...)
	_, ok := tracingtest.Attr(s, "kcp.gateway.hot_reload.outcome")
	assert.False(t, ok)
}

// Two event objects can share a reason (a failure's message varies; the operator's restart starts
// a fresh event). Each is compared with itself in the baseline, never with another object.
func TestTiming_TwoEventsOfOneReasonAreToldApart(t *testing.T) {
	earlier := patchedAt.Add(-time.Hour)
	older := OperatorEvent{Name: "gw.a", Reason: EventHotReloadSucceeded, Count: 9, LastSeen: earlier}
	newer := OperatorEvent{Name: "gw.b", Reason: EventHotReloadSucceeded, Count: 1, LastSeen: earlier.Add(time.Minute)}
	baseline := HotReloadSignals{Events: []OperatorEvent{older, newer}}
	s := runOperatorTiming(t, baseline, poll{baseline, at(2 * time.Second)})
	_, ok := tracingtest.Attr(s, "kcp.gateway.hot_reload.outcome")
	assert.False(t, ok, "neither object changed, so nothing is this change's")

	moved := newer
	moved.Count, moved.LastSeen = 2, at(3*time.Second)
	s = runOperatorTiming(t, baseline, poll{baseline, at(2 * time.Second)}, poll{HotReloadSignals{Events: []OperatorEvent{older, moved}}, at(4 * time.Second)})
	promoted, ok := intAttr(t, s, "kcp.gateway.hot_reload.promoted_ms")
	require.True(t, ok)
	assert.Equal(t, int64(4000), promoted)
}

// The operator's event recorder drops events past a burst; the condition isn't rate limited. With
// the canary on, InProgress then Succeeded in this wait is the promotion when no event shows it.
func TestTiming_ConditionMarksPromotionWhenTheEventIsDropped(t *testing.T) {
	earlier := patchedAt.Add(-time.Hour)
	baseline := HotReloadSignals{Condition: cond("True", "Succeeded", earlier)}
	s := runOperatorTiming(t, baseline,
		poll{HotReloadSignals{Condition: cond("Unknown", "InProgress", at(time.Second))}, at(2 * time.Second)},
		poll{HotReloadSignals{Condition: cond("True", "Succeeded", at(7*time.Second))}, at(8 * time.Second)},
	)
	promoted, ok := intAttr(t, s, "kcp.gateway.hot_reload.promoted_ms")
	require.True(t, ok)
	assert.Equal(t, int64(8000), promoted)
	outcome, _ := tracingtest.Attr(s, "kcp.gateway.hot_reload.outcome")
	assert.Equal(t, EventHotReloadSucceeded, outcome.AsString())
}

// A Succeeded condition without an InProgress in this wait is the last change's, not this one's.
func TestTiming_SucceededConditionAloneIsNotThisChange(t *testing.T) {
	baseline := HotReloadSignals{Condition: cond("True", "Succeeded", patchedAt.Add(-time.Hour))}
	s := runOperatorTiming(t, baseline, poll{HotReloadSignals{Condition: cond("True", "Succeeded", patchedAt.Add(-time.Hour))}, at(2 * time.Second)})
	_, ok := tracingtest.Attr(s, "kcp.gateway.hot_reload.promoted_ms")
	assert.False(t, ok)
}

func TestTiming_OutcomeKnownStopsTheReads(t *testing.T) {
	rec := tracingtest.Install(t)
	_, span := tracing.Start(context.Background(), tracerName, "test.span")
	timing := newTransitionTiming(span, patchedAt)
	timing.watchOperator(HotReloadSignals{})
	assert.False(t, timing.outcomeKnown())
	timing.observeOperator(HotReloadSignals{Events: []OperatorEvent{ev(EventHotReloadAppliedDirectly, 1, at(time.Second))}}, at(2*time.Second))
	assert.True(t, timing.outcomeKnown())
	span.End()
	_ = rec
}

// Every time on the span counts from the patch, so pod times and the promotion line up.
func TestTiming_EverythingCountsFromThePatch(t *testing.T) {
	rec := tracingtest.Install(t)
	_, span := tracing.Start(context.Background(), tracerName, "test.span")
	timing := newTransitionTiming(span, patchedAt)
	timing.watchOperator(HotReloadSignals{})
	// The wait started 3s after the patch (wait_accepted), so its own Elapsed is 3s behind.
	timing.observeOperator(HotReloadSignals{Events: []OperatorEvent{ev(EventHotReloadAppliedDirectly, 1, at(4*time.Second))}}, at(4*time.Second))
	timing.observePods(progress(3, 3, MechanismNoRollObserved, 2*time.Second, true), at(5*time.Second))
	timing.end()
	span.End()
	s := tracingtest.Find(t, tracingtest.Spans(rec), "test.span")
	promoted, _ := intAttr(t, s, "kcp.gateway.hot_reload.promoted_ms")
	all, _ := intAttr(t, s, "kcp.gateway.all_pods_ms")
	assert.Equal(t, int64(4000), promoted)
	assert.Equal(t, int64(5000), all, "pod times count from the patch, not the wait's start")
	from, _ := tracingtest.Attr(s, "kcp.gateway.timed_from")
	assert.Equal(t, "patch", from.AsString())
}

// A rolling gateway's transition is verified by the rollout wait; its timing records the rollout.
func TestTiming_RolloutRecordsReadyPodsAndTheRoll(t *testing.T) {
	rec := tracingtest.Install(t)
	_, span := tracing.Start(context.Background(), tracerName, "test.span")
	timing := newTransitionTiming(span, patchedAt)
	for _, p := range []struct {
		p   GatewayReadinessProgress
		now time.Duration
	}{
		{GatewayReadinessProgress{InitialPodCount: 3, PodsReady: 3}, 5 * time.Second},
		{GatewayReadinessProgress{InitialPodCount: 3, PodsReady: 2, RolloutDetected: true}, 10 * time.Second},
		{GatewayReadinessProgress{InitialPodCount: 3, PodsReady: 2, RolloutDetected: true}, 15 * time.Second},
		{GatewayReadinessProgress{InitialPodCount: 3, PodsReady: 3, RolloutDetected: true, Ready: true}, 60 * time.Second},
	} {
		timing.observeRollout(p.p, at(p.now))
	}
	timing.end()
	span.End()
	s := tracingtest.Find(t, tracingtest.Spans(rec), "test.span")
	assert.Len(t, eventsNamed(s, "gateway.rollout_progress"), 3)
	all, ok := intAttr(t, s, "kcp.gateway.all_pods_ms")
	require.True(t, ok)
	assert.Equal(t, int64(60000), all)
	mech, _ := tracingtest.Attr(s, "kcp.gateway.observed_mechanism")
	assert.Equal(t, "pod-roll", mech.AsString())
	_, ok = tracingtest.Attr(s, "kcp.gateway.first_pod_ms")
	assert.False(t, ok, "the rollout wait counts ready pods, not pods at the new revision")
}
