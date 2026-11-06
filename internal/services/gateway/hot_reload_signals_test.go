package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func operatorEvent(name, reason, kind, object string, count int32, last time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: "confluent"},
		InvolvedObject: corev1.ObjectReference{Kind: kind, Name: object, Namespace: "confluent"},
		Reason:         reason,
		Count:          count,
		LastTimestamp:  metav1.NewTime(last),
	}
}

func TestReadHotReloadSignals(t *testing.T) {
	ltt := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	last := ltt.Add(5 * time.Second)
	dyn := newFakeDynamicClient(newGatewayCR("gw", "confluent", 3, 3, true,
		withGatewayConditionAtTime(HotReloadConditionType, "True", "Succeeded", "promoted", ltt)))
	cs := newFakeClientset(
		operatorEvent("e1", EventHotReloadSucceeded, "Gateway", "gw", 4, last),
		operatorEvent("e2", "Reconciled", "Gateway", "gw", 1, last),
		operatorEvent("e3", EventHotReloadSucceeded, "Gateway", "other-gw", 9, last),
	)

	s, err := readHotReloadSignals(context.Background(), dyn, cs, "confluent", "gw")
	require.NoError(t, err)
	require.NotNil(t, s.Condition)
	assert.Equal(t, "True", s.Condition.Status)
	assert.Equal(t, "Succeeded", s.Condition.Reason)
	assert.True(t, s.Condition.LastTransitionTime.Equal(ltt))
	require.Len(t, s.Events, 1)
	assert.Equal(t, OperatorEvent{Name: "e1", Reason: EventHotReloadSucceeded, Count: 4, LastSeen: last}, s.Events[0])
}

func TestReadHotReloadSignals_SeriesCountsWin(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	e := operatorEvent("e1", EventHotReloadAppliedDirectly, "Gateway", "gw", 1, base)
	e.Series = &corev1.EventSeries{Count: 7, LastObservedTime: metav1.NewMicroTime(base.Add(time.Minute))}
	dyn := newFakeDynamicClient(newGatewayCR("gw", "confluent", 3, 3, true))
	s, err := readHotReloadSignals(context.Background(), dyn, newFakeClientset(e), "confluent", "gw")
	require.NoError(t, err)
	assert.Nil(t, s.Condition)
	require.Len(t, s.Events, 1)
	assert.Equal(t, int32(7), s.Events[0].Count)
	assert.True(t, s.Events[0].LastSeen.Equal(base.Add(time.Minute)))
}
