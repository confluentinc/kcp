package gateway

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// lastSpecWriteTime returns the newest managedFields time of any write to the
// CR other than a status-subresource write: the closest evidence, without a
// kcp apply to time against, of when the spec last changed. It reports false
// when no such entry carries a time.
func lastSpecWriteTime(cr *unstructured.Unstructured) (time.Time, bool) {
	var newest time.Time
	found := false
	for _, entry := range cr.GetManagedFields() {
		if entry.Subresource == "status" || entry.Time == nil {
			continue
		}
		if !found || entry.Time.Time.After(newest) {
			newest = entry.Time.Time
			found = true
		}
	}
	return newest, found
}

// currentFatalCondition returns the first status=False condition with a fatal
// reason that is still current, or nil when there is none. Unlike
// findGatewayRejection there is no kcp apply to time against, so each
// condition is judged by, in order:
//
//  1. its own observedGeneration, when present: current iff it equals the CR's
//     generation.
//  2. otherwise, its lastTransitionTime against lastSpecWriteTime: stale iff
//     it is more than gatewayClockSkewTolerance before the last spec write.
//  3. otherwise it is current, and undecided is true: it was judged current
//     only for lack of evidence.
func currentFatalCondition(cr *unstructured.Unstructured, gatewayName string) (rej *GatewayRejectedError, undecided bool, err error) {
	conditions, found, err := unstructured.NestedSlice(cr.Object, "status", "conditions")
	if err != nil {
		return nil, false, fmt.Errorf("failed to read gateway status.conditions: %w", err)
	}
	if !found {
		return nil, false, nil
	}

	generation := cr.GetGeneration()
	observed, _, _ := unstructured.NestedInt64(cr.Object, "status", "observedGeneration")
	lastWrite, haveWrite := lastSpecWriteTime(cr)

	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if status, _ := condition["status"].(string); status != string(metav1.ConditionFalse) {
			continue
		}
		reason, _ := condition["reason"].(string)
		if !isFatalGatewayConditionReason(reason) {
			continue
		}

		condUndecided := false
		if condGen, ok, err := unstructured.NestedInt64(condition, "observedGeneration"); err == nil && ok {
			if condGen != generation {
				continue
			}
		} else if ltt, ok := parseConditionTransitionTime(condition); ok && haveWrite {
			if ltt.Before(lastWrite.Add(-gatewayClockSkewTolerance)) {
				continue
			}
		} else {
			condUndecided = true
		}

		message, _ := condition["message"].(string)
		conditionType, _ := condition["type"].(string)
		return &GatewayRejectedError{
			Gateway:            gatewayName,
			ConditionType:      conditionType,
			Reason:             reason,
			Message:            message,
			Generation:         generation,
			ObservedGeneration: observed,
		}, condUndecided, nil
	}
	return nil, false, nil
}

// parseConditionTransitionTime reads a condition's RFC3339 lastTransitionTime.
func parseConditionTransitionTime(condition map[string]any) (time.Time, bool) {
	raw, ok := condition["lastTransitionTime"].(string)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	return t, err == nil
}
