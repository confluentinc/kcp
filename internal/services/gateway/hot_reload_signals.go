package gateway

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var _ HotReloadSignalReader = (*K8sService)(nil)

// ReadHotReloadSignals reads the operator's hot reload condition on the
// Gateway and its hot reload events. Reading events needs get and list on
// events in the Gateway's namespace; callers treat any error as "no timings".
func (s *K8sService) ReadHotReloadSignals(ctx context.Context, namespace, gatewayName string) (HotReloadSignals, error) {
	config, err := RESTConfig(s.kubeConfigPath)
	if err != nil {
		return HotReloadSignals{}, fmt.Errorf("failed to build config: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return HotReloadSignals{}, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return HotReloadSignals{}, fmt.Errorf("failed to create clientset: %w", err)
	}
	return readHotReloadSignals(ctx, dynamicClient, clientset, namespace, gatewayName)
}

// readHotReloadSignals is ReadHotReloadSignals with injected clients, for tests.
func readHotReloadSignals(ctx context.Context, dynamicClient dynamic.Interface, clientset kubernetes.Interface, namespace, gatewayName string) (HotReloadSignals, error) {
	gvr := schema.GroupVersionResource{Group: GatewayGroup, Version: GatewayVersion, Resource: GatewayResourcePlural}
	gw, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, gatewayName, metav1.GetOptions{})
	if err != nil {
		return HotReloadSignals{}, fmt.Errorf("failed to get gateway: %w", err)
	}
	var out HotReloadSignals
	conditions, _, _ := unstructured.NestedSlice(gw.Object, "status", "conditions")
	for _, c := range conditions {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != HotReloadConditionType {
			continue
		}
		cond := &HotReloadCondition{}
		cond.Status, _ = m["status"].(string)
		cond.Reason, _ = m["reason"].(string)
		if ts, ok := m["lastTransitionTime"].(string); ok {
			cond.LastTransitionTime, _ = time.Parse(time.RFC3339, ts)
		}
		out.Condition = cond
		break
	}

	selector := fields.Set{"involvedObject.name": gatewayName, "involvedObject.kind": "Gateway"}.AsSelector().String()
	events, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		return HotReloadSignals{}, fmt.Errorf("failed to list the gateway's events: %w", err)
	}
	for _, e := range events.Items {
		// The field selector is applied by the API server; check again, as fakes ignore it.
		if e.InvolvedObject.Name != gatewayName || e.InvolvedObject.Kind != "Gateway" || !hotReloadEventReasons[e.Reason] {
			continue
		}
		out.Events = append(out.Events, operatorEventOf(e))
	}
	return out, nil
}

// operatorEventOf reads an event's repeat count and last sighting, from its
// series when it has one, else from the legacy count and timestamps.
func operatorEventOf(e corev1.Event) OperatorEvent {
	if e.Series != nil {
		return OperatorEvent{Reason: e.Reason, Count: e.Series.Count, LastSeen: e.Series.LastObservedTime.Time}
	}
	last := e.LastTimestamp.Time
	if last.IsZero() {
		last = e.EventTime.Time
	}
	return OperatorEvent{Reason: e.Reason, Count: e.Count, LastSeen: last}
}
