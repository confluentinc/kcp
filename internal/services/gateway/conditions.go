package gateway

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Gateway status-condition handling for hot-reload verification.
//
// Waiting out a full timeout when CFK already announced a rejection is poor
// UX — on a crashed canary the rejection is published ~5.8s after the apply,
// against a 90s timeout. But Gateway conditions cannot simply be trusted:
// they carry no observedGeneration, so nothing in a condition ties it to the
// spec revision that produced it, and they go stale for days. A cluster-ready
// ApplyFailed condition was observed still present 6 days and 13 generations
// after the failure it described, on a healthy 3/3 gateway.
//
// So a condition is only actionable when it *differs from what was there before
// our own apply*. Comparing lastTransitionTime against a kcp-side timestamp
// would work but introduces laptop-vs-cluster clock skew; instead we snapshot
// the conditions immediately before applying and compare each condition against
// its own previous value. Both timestamps are written by the cluster, so skew is
// structurally impossible.
//
// The comparison is over the whole condition, not just the timestamp — see
// conditionFingerprint for why the timestamp alone goes blind on a repeat
// failure.

// Gateway condition types reported by CFK.
const (
	ConditionHotReloadStatus   = "platform.confluent.io/hot-reload-status"
	ConditionClusterReady      = "platform.confluent.io/cluster-ready"
	ConditionGarbageCollecting = "platform.confluent.io/garbage-collecting"
)

// conditionStatusFalse is the only condition status that can indicate failure.
// Unknown means in-flight (hot reload reports Unknown/InProgress while the
// canary runs) and True means success.
const conditionStatusFalse = "False"

// rejectionPrecedence lists the conditions that can signal a rejected apply,
// most direct first. Conditions absent from this list are never treated as
// failures — notably garbage-collecting, which sits at False with reason
// "Garbage Collection not triggered" on a perfectly healthy gateway, and would
// otherwise fail every apply.
var rejectionPrecedence = []string{ConditionHotReloadStatus, ConditionClusterReady}

// clusterReadyFailureReasons are the cluster-ready reasons worth aborting on.
// cluster-ready also goes False transiently while the operator reconciles, so
// this is deliberately a whitelist rather than "any False".
//
// The bias is intentional: a missed fast-fail only costs the wait its full
// timeout, whereas a false fast-fail aborts a migration that was fine. The
// per-pod /config poll remains the actual gate.
var clusterReadyFailureReasons = map[string]struct{}{
	"ApplyFailed": {},
}

// gatewayCondition is one entry of a Gateway CR's status.conditions.
// Deliberately not metav1.Condition: CFK's entries carry lastProbeTime and
// omit observedGeneration, so they do not satisfy that shape.
type gatewayCondition struct {
	Type               string
	Status             string
	Reason             string
	Message            string
	LastTransitionTime metav1.Time
}

// conditionFingerprint is everything about a condition that can distinguish a
// fresh report from the one that was already there.
//
// lastTransitionTime alone is not enough. Kubernetes convention is that the
// timestamp only moves when a condition's *status* flips, so a gateway already
// sitting at cluster-ready False/ApplyFailed that refuses another apply keeps
// both its status and its timestamp — and the E14 gateway (ApplyFailed unchanged
// for 6 days and 13 generations) is direct evidence the timestamp really is
// sticky. Comparing on time alone therefore goes blind on exactly the clusters
// most likely to reject something. Reason and message do move, because the
// operator rewrites them per failure, so they carry the signal the timestamp
// drops.
type conditionFingerprint struct {
	LastTransitionTime metav1.Time
	Status             string
	Reason             string
	Message            string
}

// ConditionSnapshot records each condition as it stood at a point in time —
// captured immediately before an apply.
type ConditionSnapshot map[string]conditionFingerprint

// changed reports whether a condition differs from the snapshot, and therefore
// whether it says anything about the apply made after it.
//
// A condition type absent from the snapshot counts as changed: the operator has
// started reporting something it was not reporting before, which is new
// information.
func (s ConditionSnapshot) changed(c gatewayCondition) bool {
	prev, seen := s[c.Type]
	if !seen {
		return true
	}
	return fingerprint(c) != prev
}

// fingerprint reduces a condition to its comparable identity.
func fingerprint(c gatewayCondition) conditionFingerprint {
	return conditionFingerprint{
		LastTransitionTime: c.LastTransitionTime,
		Status:             c.Status,
		Reason:             c.Reason,
		Message:            c.Message,
	}
}

// snapshotConditions captures the current conditions, to be passed to a later
// findNewRejection call.
func snapshotConditions(conds []gatewayCondition) ConditionSnapshot {
	snapshot := make(ConditionSnapshot, len(conds))
	for _, c := range conds {
		snapshot[c.Type] = fingerprint(c)
	}
	return snapshot
}

// GatewayRejection reports that the operator refused a config change.
type GatewayRejection struct {
	ConditionType string
	Reason        string
	Message       string
	At            metav1.Time
}

func (r *GatewayRejection) Error() string {
	msg := fmt.Sprintf("gateway rejected the config change: %s reported %s", r.ConditionType, r.Reason)
	if r.Message != "" {
		msg += ": " + r.Message
	}
	return msg
}

// gatewayConditions extracts status.conditions from an unstructured Gateway CR.
//
// A CR with no status, or a status with no conditions, yields no conditions and
// no error — that is the normal state of a freshly created resource. Individual
// malformed entries are skipped rather than failing the whole read, so one bad
// entry cannot blind us to a real failure alongside it.
func gatewayConditions(obj map[string]any) ([]gatewayCondition, error) {
	statusRaw, ok := obj["status"]
	if !ok || statusRaw == nil {
		return nil, nil
	}
	status, ok := statusRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("gateway status is not an object")
	}

	condsRaw, ok := status["conditions"]
	if !ok || condsRaw == nil {
		return nil, nil
	}
	list, ok := condsRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("gateway status.conditions is not a list")
	}

	conds := make([]gatewayCondition, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		condType := condString(entry, "type")
		if condType == "" {
			continue
		}
		conds = append(conds, gatewayCondition{
			Type:               condType,
			Status:             condString(entry, "status"),
			Reason:             condString(entry, "reason"),
			Message:            condString(entry, "message"),
			LastTransitionTime: condTime(entry, "lastTransitionTime"),
		})
	}
	return conds, nil
}

func condString(entry map[string]any, key string) string {
	if v, ok := entry[key].(string); ok {
		return v
	}
	return ""
}

// condTime parses a condition timestamp, yielding the zero time when absent or
// unparseable. Timestamps are only ever used for change detection, so a zero
// value degrades to "no evidence of a transition" rather than to a false one.
func condTime(entry map[string]any, key string) metav1.Time {
	raw := condString(entry, key)
	if raw == "" {
		return metav1.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return metav1.Time{}
	}
	return metav1.NewTime(parsed)
}

// findNewRejection returns a rejection only when a failure condition differs
// from what before recorded. A condition still carrying its pre-apply values
// says nothing about the apply, however alarming it looks.
//
// An empty baseline disables the check entirely rather than treating everything
// as new. This is not merely a shortcut: changed counts an unseen condition type
// as changed — correct for a real snapshot, where a type appearing for the first
// time is new information — so without the guard a missing baseline would turn
// every days-old ApplyFailed into an instant rejection, which is the precise
// failure this whole mechanism exists to prevent.
func findNewRejection(conds []gatewayCondition, before ConditionSnapshot) *GatewayRejection {
	if len(before) == 0 {
		return nil
	}

	byType := make(map[string]gatewayCondition, len(conds))
	for _, c := range conds {
		byType[c.Type] = c
	}

	for _, condType := range rejectionPrecedence {
		c, ok := byType[condType]
		if !ok || c.Status != conditionStatusFalse {
			continue
		}
		if !isRejectionReason(condType, c.Reason) {
			continue
		}
		if !before.changed(c) {
			continue
		}
		return &GatewayRejection{
			ConditionType: c.Type,
			Reason:        c.Reason,
			Message:       c.Message,
			At:            c.LastTransitionTime,
		}
	}
	return nil
}

// isRejectionReason decides whether a False condition's reason is fatal.
// A False hot-reload-status is always fatal — the condition exists only to
// report the outcome of a hot reload. cluster-ready is narrower, since it also
// dips False during ordinary reconciliation.
func isRejectionReason(condType, reason string) bool {
	switch condType {
	case ConditionHotReloadStatus:
		return true
	case ConditionClusterReady:
		_, fatal := clusterReadyFailureReasons[reason]
		return fatal
	default:
		return false
	}
}
