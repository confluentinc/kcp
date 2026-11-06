package gateway

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// The operator's hot reload signals on a Gateway (Confluent for Kubernetes).
const (
	// HotReloadConditionType is the Gateway status condition the operator sets
	// for hot reload: Unknown/InProgress while a canary validates a change,
	// True/Succeeded once it is promoted, True/AppliedDirectly when the canary is
	// off, False with a failure reason otherwise.
	HotReloadConditionType = "platform.confluent.io/hot-reload-status"
	hotReloadInProgress    = "InProgress"

	// The operator's Kubernetes event reasons for a hot reload's outcome.
	EventHotReloadSucceeded       = "HotReloadSucceeded"
	EventHotReloadAppliedDirectly = "HotReloadAppliedDirectly"
	EventHotReloadFailed          = "HotReloadFailed"
	EventHotReloadCanceled        = "HotReloadCanceled"
)

// hotReloadEventReasons are the operator events that end a hot reload.
var hotReloadEventReasons = map[string]bool{
	EventHotReloadSucceeded: true, EventHotReloadAppliedDirectly: true,
	EventHotReloadFailed: true, EventHotReloadCanceled: true,
}

// HotReloadSignals is what the operator reports about the Gateway's latest hot reload.
type HotReloadSignals struct {
	Condition *HotReloadCondition // nil when the Gateway has no hot reload condition
	Events    []OperatorEvent     // the operator's hot reload events on this Gateway
}

// HotReloadCondition is the Gateway's hot reload status condition.
type HotReloadCondition struct {
	Status, Reason     string
	LastTransitionTime time.Time
}

// OperatorEvent is one of the operator's hot reload events on the Gateway.
// Kubernetes aggregates repeats of an event, so a repeat shows as a higher
// Count or a later LastSeen on the same event.
type OperatorEvent struct {
	Reason   string
	Count    int32
	LastSeen time.Time
}

// HotReloadSignalReader is optional: the verifier reads the operator's signals
// only when its Service implements it, and only to time the change.
type HotReloadSignalReader interface {
	ReadHotReloadSignals(ctx context.Context, namespace, gatewayName string) (HotReloadSignals, error)
}

// transitionTiming records, on a gateway.wait_config_applied span, how a config
// change reached the gateway pods: an event each time the count of pods at the
// wanted configId changes, and on end the time to the first pod and to every
// pod, and the mechanism observed. When watching the operator, it also records
// when the canary started and when the operator promoted the change. It only
// observes; it never changes what the wait waits for or when it returns.
type transitionTiming struct {
	span trace.Span

	seen       bool
	lastAtWant int
	mechanism  RolloutMechanism
	firstPod   time.Duration
	hasFirst   bool
	allPods    time.Duration
	hasAll     bool

	// Operator signals, set by watchOperator. Durations are measured on kcp's
	// clock from patchedAt to the poll that first saw each signal.
	watching      bool
	patchedAt     time.Time
	baseline      map[string]OperatorEvent
	canaryStarted time.Duration
	hasCanary     bool
	promoted      time.Duration
	hasPromoted   bool
	outcome       string
}

func newTransitionTiming(span trace.Span) *transitionTiming {
	return &transitionTiming{span: span, mechanism: MechanismNoRollObserved}
}

// observePods takes one poll's progress. Elapsed is measured from the start of
// the wait.
func (t *transitionTiming) observePods(p ConfigWaitProgress) {
	if p.Mechanism != "" {
		t.mechanism = p.Mechanism
	}
	if p.PodsAtWant > 0 && !t.hasFirst {
		t.firstPod, t.hasFirst = p.Elapsed, true
	}
	if p.Converged && !t.hasAll {
		t.allPods, t.hasAll = p.Elapsed, true
	}
	if t.seen && p.PodsAtWant == t.lastAtWant {
		return
	}
	t.seen, t.lastAtWant = true, p.PodsAtWant
	t.span.AddEvent("gateway.config_progress", trace.WithAttributes(
		attribute.Int("kcp.gateway.pods_at_config", p.PodsAtWant),
		attribute.Int("kcp.gateway.pods_ready", p.PodsReady),
		attribute.Int("kcp.gateway.pods_total", p.PodsTotal),
		attribute.String("kcp.gateway.observed_mechanism", string(t.mechanism)),
		attribute.Int64("kcp.gateway.elapsed_ms", p.Elapsed.Milliseconds()),
	))
}

// watchOperator arms the operator signals: baseline is what the operator
// reported just before the patch, patchedAt when kcp made it (kcp's clock).
// Only signals that differ from the baseline belong to this change.
func (t *transitionTiming) watchOperator(baseline HotReloadSignals, patchedAt time.Time) {
	t.watching, t.patchedAt = true, patchedAt
	t.baseline = make(map[string]OperatorEvent, len(baseline.Events))
	for _, e := range baseline.Events {
		t.baseline[e.Reason] = e
	}
}

// observeOperator takes one poll's operator signals, read at now (kcp's clock).
//
// The condition can't mark a promotion by itself: its lastTransitionTime moves
// only when its status, reason or message change, so back-to-back changes with
// the canary off leave it at True/AppliedDirectly throughout. It is used only
// for the canary's start (InProgress, which only a canary sets). The outcome
// comes from the operator's events: one absent from the baseline, or present
// with a higher count or a later last-seen time, is this change's.
func (t *transitionTiming) observeOperator(s HotReloadSignals, now time.Time) {
	if !t.watching {
		return
	}
	if c := s.Condition; c != nil && c.Reason == hotReloadInProgress && !t.hasCanary {
		t.canaryStarted, t.hasCanary = now.Sub(t.patchedAt), true
		t.statusEvent(c.Reason, "condition", c.LastTransitionTime, now)
	}
	if t.outcome != "" {
		return
	}
	for _, e := range s.Events {
		if !hotReloadEventReasons[e.Reason] {
			continue
		}
		if b, ok := t.baseline[e.Reason]; ok && e.Count <= b.Count && !e.LastSeen.After(b.LastSeen) {
			continue
		}
		t.outcome = e.Reason
		if e.Reason == EventHotReloadSucceeded || e.Reason == EventHotReloadAppliedDirectly {
			t.promoted, t.hasPromoted = now.Sub(t.patchedAt), true
		}
		t.statusEvent(e.Reason, "event", e.LastSeen, now)
		return
	}
}

func (t *transitionTiming) statusEvent(reason, source string, serverTime, now time.Time) {
	t.span.AddEvent("gateway.hot_reload_status", trace.WithAttributes(
		attribute.String("kcp.gateway.hot_reload.reason", reason),
		attribute.String("kcp.gateway.hot_reload.source", source),
		attribute.String("kcp.gateway.hot_reload.server_time", serverTime.UTC().Format(time.RFC3339)),
		attribute.Int64("kcp.gateway.elapsed_since_patch_ms", now.Sub(t.patchedAt).Milliseconds()),
	))
}

// end sets the summary attributes. A time that was never observed is left
// absent rather than recorded as 0.
func (t *transitionTiming) end() {
	attrs := []attribute.KeyValue{attribute.String("kcp.gateway.observed_mechanism", string(t.mechanism))}
	if t.hasFirst {
		attrs = append(attrs, attribute.Int64("kcp.gateway.first_pod_ms", t.firstPod.Milliseconds()))
	}
	if t.hasAll {
		attrs = append(attrs, attribute.Int64("kcp.gateway.all_pods_ms", t.allPods.Milliseconds()))
	}
	if t.hasCanary {
		attrs = append(attrs, attribute.Int64("kcp.gateway.hot_reload.canary_started_ms", t.canaryStarted.Milliseconds()))
	}
	if t.hasPromoted {
		attrs = append(attrs, attribute.Int64("kcp.gateway.hot_reload.promoted_ms", t.promoted.Milliseconds()))
	}
	if t.outcome != "" {
		attrs = append(attrs, attribute.String("kcp.gateway.hot_reload.outcome", t.outcome))
	}
	t.span.SetAttributes(attrs...)
}
