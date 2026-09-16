package reconcile

import "fmt"

// Classify applies the per-topic verdict truth table.
//
//	MIGRATABLE  <=> onSource && mirror==Active && !routesToTarget
//	UNCHANGED   <=> mirror==Stopped && routesToTarget && onTarget
//	else FAIL-FAST, with a reason explaining the specific inconsistency.
func Classify(topic string, onSource, onTarget bool, mirror MirrorState, routesToTarget bool) TopicVerdict {
	tv := TopicVerdict{
		Topic: topic,
		S:     boolStr(onSource, "present", "absent"),
		M:     mirror.String(),
		T:     boolStr(onTarget, "present", "absent"),
		R:     boolStr(routesToTarget, "->target", "->source"),
	}
	switch {
	case onSource && mirror == MirrorActive && !routesToTarget:
		tv.Verdict = Migratable
	case mirror == MirrorStopped && routesToTarget && onTarget:
		tv.Verdict = Unchanged
	case routesToTarget && mirror == MirrorActive:
		tv.Verdict, tv.Reason = FailFast, fmt.Sprintf("%s routes to target but its mirror is not yet promoted", topic)
	case mirror == MirrorStopped && !routesToTarget:
		tv.Verdict, tv.Reason = FailFast, fmt.Sprintf("%s is promoted but not switched over", topic)
	case mirror == MirrorBad:
		tv.Verdict, tv.Reason = FailFast, fmt.Sprintf("%s mirror is in a failed/transitional state", topic)
	case onSource && mirror == MirrorNone && onTarget:
		tv.Verdict, tv.Reason = FailFast, fmt.Sprintf("%s exists on target but is not a mirror of the source", topic)
	case onSource && mirror == MirrorNone:
		tv.Verdict, tv.Reason = FailFast, fmt.Sprintf("%s is not on the cluster link", topic)
	case !onSource:
		tv.Verdict, tv.Reason = FailFast, fmt.Sprintf("%s not found on the source", topic)
	default:
		tv.Verdict, tv.Reason = FailFast, fmt.Sprintf("unclassified inconsistent state for %s", topic)
	}
	return tv
}

func boolStr(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}
