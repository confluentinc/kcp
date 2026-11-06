package gateway

import (
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// transitionTiming records, on a gateway.wait_config_applied span, how a config
// change reached the gateway pods: an event each time the count of pods at the
// wanted configId changes, and on end the time to the first pod and to every
// pod, and the mechanism observed. It only observes; it never changes what the
// wait waits for or when it returns.
type transitionTiming struct {
	span trace.Span

	seen       bool
	lastAtWant int
	mechanism  RolloutMechanism
	firstPod   time.Duration
	hasFirst   bool
	allPods    time.Duration
	hasAll     bool
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
	t.span.SetAttributes(attrs...)
}
