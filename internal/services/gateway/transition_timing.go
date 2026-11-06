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
// Kubernetes aggregates repeats of an event into one object, so a repeat shows
// as a higher Count or a later LastSeen on the same object (Name). Several
// objects can share a reason: a failure's message varies, and an operator
// restart starts a fresh object.
type OperatorEvent struct {
	Name     string
	Reason   string
	Count    int32
	LastSeen time.Time
}

// HotReloadSignalReader is optional: the verifier reads the operator's signals
// only when its Service implements it, and only to time the change.
type HotReloadSignalReader interface {
	ReadHotReloadSignals(ctx context.Context, namespace, gatewayName string) (HotReloadSignals, error)
}

// transitionTiming records on a gateway wait's span how a config change reached
// the gateway pods: events as the pod counts change, and on end the time to the
// first pod (when the wait can tell) and to every pod, and the mechanism
// observed. When watching the operator it also records when the canary started
// and when the operator promoted the change. Every time counts from origin, the
// patch when it is known, on kcp's clock. It only observes; it never changes
// what the wait waits for or when it returns.
type transitionTiming struct {
	span     trace.Span
	origin   time.Time
	timedBy  string // "patch" or "wait_start"
	seen     bool
	lastPods int

	mechanism RolloutMechanism
	firstPod  time.Duration
	hasFirst  bool
	allPods   time.Duration
	hasAll    bool

	watching      bool
	baseline      map[string]OperatorEvent // by event name
	inProgress    bool                     // the condition was InProgress during this wait
	canaryStarted time.Duration
	hasCanary     bool
	promoted      time.Duration
	hasPromoted   bool
	outcome       string
}

// newTransitionTiming times from patchedAt, or from now when the patch time is
// unknown (zero).
func newTransitionTiming(span trace.Span, patchedAt time.Time) *transitionTiming {
	t := &transitionTiming{span: span, origin: patchedAt, timedBy: "patch", mechanism: MechanismNoRollObserved}
	if patchedAt.IsZero() {
		t.origin, t.timedBy = time.Now(), "wait_start"
	}
	return t
}

func (t *transitionTiming) since(now time.Time) time.Duration { return now.Sub(t.origin) }

// observePods takes one poll of the per-pod configId wait, seen at now.
func (t *transitionTiming) observePods(p ConfigWaitProgress, now time.Time) {
	if p.Mechanism != "" {
		t.mechanism = p.Mechanism
	}
	if p.PodsAtWant > 0 && !t.hasFirst {
		t.firstPod, t.hasFirst = t.since(now), true
	}
	if p.Converged && !t.hasAll {
		t.allPods, t.hasAll = t.since(now), true
	}
	if t.seen && p.PodsAtWant == t.lastPods {
		return
	}
	t.seen, t.lastPods = true, p.PodsAtWant
	t.span.AddEvent("gateway.config_progress", trace.WithAttributes(
		attribute.Int("kcp.gateway.pods_at_config", p.PodsAtWant),
		attribute.Int("kcp.gateway.pods_ready", p.PodsReady),
		attribute.Int("kcp.gateway.pods_total", p.PodsTotal),
		attribute.String("kcp.gateway.observed_mechanism", string(t.mechanism)),
		attribute.Int64("kcp.gateway.elapsed_ms", t.since(now).Milliseconds()),
	))
}

// observeRollout takes one poll of the rollout wait (a gateway without hot
// reload), seen at now. It counts ready pods, not pods at the new revision, so
// it gives the time to every pod but no first-pod time.
func (t *transitionTiming) observeRollout(p GatewayReadinessProgress, now time.Time) {
	if p.RolloutDetected {
		t.mechanism = MechanismPodRoll
	}
	if p.Ready && !t.hasAll {
		t.allPods, t.hasAll = t.since(now), true
	}
	if t.seen && p.PodsReady == t.lastPods {
		return
	}
	t.seen, t.lastPods = true, p.PodsReady
	t.span.AddEvent("gateway.rollout_progress", trace.WithAttributes(
		attribute.Int("kcp.gateway.pods_ready", p.PodsReady),
		attribute.Int("kcp.gateway.pods_total", p.InitialPodCount),
		attribute.String("kcp.gateway.observed_mechanism", string(t.mechanism)),
		attribute.Int64("kcp.gateway.elapsed_ms", t.since(now).Milliseconds()),
	))
}

// watchOperator arms the operator signals with what the operator reported just
// before the patch. Only signals that differ from it belong to this change.
func (t *transitionTiming) watchOperator(baseline HotReloadSignals) {
	t.watching = true
	t.baseline = make(map[string]OperatorEvent, len(baseline.Events))
	for _, e := range baseline.Events {
		t.baseline[e.Name] = e
	}
}

// outcomeKnown reports whether this change's outcome has been seen, after which
// the operator needn't be read again.
func (t *transitionTiming) outcomeKnown() bool { return t.outcome != "" }

// observeOperator takes one poll's operator signals, read at now.
//
// The condition's lastTransitionTime moves only when its status, reason or
// message change, so back-to-back changes with the canary off leave it at
// True/AppliedDirectly throughout. The outcome comes from the operator's
// events: an event object absent from the baseline, or present with a higher
// count or a later last-seen time, is this change's. The operator's event
// recorder drops events past a burst, though, so with the canary on the
// condition backs it up: InProgress (which only a canary sets) and then
// Succeeded, both in this wait, is this change's promotion.
func (t *transitionTiming) observeOperator(s HotReloadSignals, now time.Time) {
	if !t.watching {
		return
	}
	if c := s.Condition; c != nil && c.Reason == hotReloadInProgress && !t.hasCanary {
		t.inProgress = true
		t.canaryStarted, t.hasCanary = t.since(now), true
		t.statusEvent(c.Reason, "condition", c.LastTransitionTime, now)
	}
	if t.outcome != "" {
		return
	}
	for _, e := range s.Events {
		if !hotReloadEventReasons[e.Reason] {
			continue
		}
		if b, ok := t.baseline[e.Name]; ok && e.Count <= b.Count && !e.LastSeen.After(b.LastSeen) {
			continue
		}
		t.setOutcome(e.Reason, "event", e.LastSeen, now)
		return
	}
	if c := s.Condition; c != nil && t.inProgress && c.Status == "True" && c.Reason == "Succeeded" {
		t.setOutcome(EventHotReloadSucceeded, "condition", c.LastTransitionTime, now)
	}
}

func (t *transitionTiming) setOutcome(reason, source string, serverTime, now time.Time) {
	t.outcome = reason
	if reason == EventHotReloadSucceeded || reason == EventHotReloadAppliedDirectly {
		t.promoted, t.hasPromoted = t.since(now), true
	}
	t.statusEvent(reason, source, serverTime, now)
}

func (t *transitionTiming) statusEvent(reason, source string, serverTime, now time.Time) {
	t.span.AddEvent("gateway.hot_reload_status", trace.WithAttributes(
		attribute.String("kcp.gateway.hot_reload.reason", reason),
		attribute.String("kcp.gateway.hot_reload.source", source),
		attribute.String("kcp.gateway.hot_reload.server_time", serverTime.UTC().Format(time.RFC3339)),
		attribute.Int64("kcp.gateway.elapsed_ms", t.since(now).Milliseconds()),
	))
}

// end sets the summary attributes. A time that was never observed is left
// absent rather than recorded as 0.
func (t *transitionTiming) end() {
	attrs := []attribute.KeyValue{
		attribute.String("kcp.gateway.observed_mechanism", string(t.mechanism)),
		attribute.String("kcp.gateway.timed_from", t.timedBy),
	}
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
