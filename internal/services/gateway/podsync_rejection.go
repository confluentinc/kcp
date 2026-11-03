package gateway

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// currentFatalCondition returns the first status=False condition with a fatal
// reason that is tied to the CR's current generation by its own
// observedGeneration, or nil when there is none. It runs only once
// status.observedGeneration has caught up with metadata.generation.
//
// A fatal condition without its own observedGeneration is not counted. CFK
// sets none, leaves ApplyFailed behind after a later spec applies, and does
// not bump status.observedGeneration on a rejected spec (verified live on CFK
// 0.1838.0), so the caught-up observedGeneration is the evidence that the
// current spec was applied. Unlike findGatewayRejection there is no kcp apply
// to time against, and a lastTransitionTime rule would refuse a healthy
// gateway whose spec was fixed soon after a rejection, with nothing short of
// another spec write to clear it.
func currentFatalCondition(cr *unstructured.Unstructured, gatewayName string) (*GatewayRejectedError, error) {
	conditions, found, err := unstructured.NestedSlice(cr.Object, "status", "conditions")
	if err != nil {
		return nil, fmt.Errorf("failed to read gateway status.conditions: %w", err)
	}
	if !found {
		return nil, nil
	}

	generation := cr.GetGeneration()
	observed, _, _ := unstructured.NestedInt64(cr.Object, "status", "observedGeneration")

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
		condGen, ok, err := unstructured.NestedInt64(condition, "observedGeneration")
		if err != nil || !ok || condGen != generation {
			continue
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
		}, nil
	}
	return nil, nil
}
