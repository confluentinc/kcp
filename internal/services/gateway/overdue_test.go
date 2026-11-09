package gateway

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestOverduePod(t *testing.T) {
	now := time.Now()
	mk := func(name string, deleted *time.Time, phase corev1.PodPhase) corev1.Pod {
		p := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: corev1.PodStatus{Phase: phase}}
		if deleted != nil {
			ts := metav1.NewTime(*deleted)
			p.DeletionTimestamp = &ts
		}
		return p
	}
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	includeAll := func(*corev1.Pod) bool { return true }
	includeNone := func(*corev1.Pod) bool { return false }

	tests := []struct {
		name        string
		pods        []corev1.Pod
		include     func(*corev1.Pod) bool
		wantPod     string
		wantOverdue time.Duration
	}{
		{"past margin", []corev1.Pod{mk("gw-0", ago(replacedPodOverdueMargin+30*time.Second), corev1.PodRunning)}, includeAll, "gw-0", 30 * time.Second},
		{"within margin", []corev1.Pod{mk("gw-0", ago(replacedPodOverdueMargin-time.Second), corev1.PodRunning)}, includeAll, "", 0},
		{"deadline in future", []corev1.Pod{mk("gw-0", ago(-time.Hour), corev1.PodRunning)}, includeAll, "", 0},
		{"terminal phase", []corev1.Pod{mk("gw-0", ago(time.Hour), corev1.PodFailed)}, includeAll, "", 0},
		{"not included", []corev1.Pod{mk("gw-0", ago(time.Hour), corev1.PodRunning)}, includeNone, "", 0},
		{"no deletion timestamp", []corev1.Pod{mk("gw-0", nil, corev1.PodRunning)}, includeAll, "", 0},
		{"first by name", []corev1.Pod{
			mk("gw-b", ago(time.Hour), corev1.PodRunning),
			mk("gw-a", ago(time.Hour), corev1.PodRunning),
		}, includeAll, "gw-a", time.Hour - replacedPodOverdueMargin},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pod, by := overduePod(tc.pods, tc.include, now)
			if tc.wantPod == "" {
				assert.Nil(t, pod)
				assert.Zero(t, by)
				return
			}
			require.NotNil(t, pod)
			assert.Equal(t, tc.wantPod, pod.Name)
			assert.Equal(t, tc.wantOverdue, by.Round(time.Second))
		})
	}
}

func TestOverdueError(t *testing.T) {
	pod := &corev1.Pod{}
	pod.Name = "gw-0"
	pod.Spec.NodeName = "node-a"
	err := overdueError("ns", pod, 95*time.Second+400*time.Millisecond)
	require.True(t, errors.Is(err, ErrReplacedPodOverdue))
	for _, want := range []string{"gw-0", "node-a", "1m35s", "kubectl -n ns delete pod gw-0 --grace-period=0 --force"} {
		assert.Contains(t, err.Error(), want)
	}

	pod.Spec.NodeName = ""
	assert.Contains(t, overdueError("ns", pod, time.Minute).Error(), "unknown node")
}
