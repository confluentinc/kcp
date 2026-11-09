package gateway

import (
	"errors"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// ErrReplacedPodOverdue is returned when a gateway pod captured before the
// patch is still listed long after its deletion deadline. A pod on a lost node
// can stay Terminating indefinitely, so waiting longer cannot help.
var ErrReplacedPodOverdue = errors.New("replaced gateway pod is overdue")

// replacedPodOverdueMargin is how long past its metadata.deletionTimestamp a
// pod may stay listed before it counts as overdue. For a graceful delete the
// deletionTimestamp is the request time plus the grace period, so past it the
// kubelet should already have removed the pod. A var so tests can shorten it.
var replacedPodOverdueMargin = 60 * time.Second

// overduePod returns the first pod (by name) that include accepts, that is not
// terminal, and whose deletionTimestamp is more than replacedPodOverdueMargin
// before now, along with how far past that margin it is. It returns nil, 0
// when there is none.
func overduePod(pods []corev1.Pod, include func(*corev1.Pod) bool, now time.Time) (pod *corev1.Pod, overdueBy time.Duration) {
	var candidates []*corev1.Pod
	for i := range pods {
		p := &pods[i]
		if !include(p) || isTerminalPod(p) || p.DeletionTimestamp == nil {
			continue
		}
		if now.Sub(p.DeletionTimestamp.Time) > replacedPodOverdueMargin {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return nil, 0
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	first := candidates[0]
	return first, now.Sub(first.DeletionTimestamp.Time) - replacedPodOverdueMargin
}

// overdueError builds the error for an overdue pod, wrapping
// ErrReplacedPodOverdue and naming the recovery command.
func overdueError(namespace string, pod *corev1.Pod, overdueBy time.Duration) error {
	node := pod.Spec.NodeName
	if node == "" {
		node = "unknown node"
	}
	return fmt.Errorf("%w: pod %s on %s is still listed %s past its deletion deadline; if node %s is really gone, run kubectl -n %s delete pod %s --grace-period=0 --force, then re-run kcp",
		ErrReplacedPodOverdue, pod.Name, node, overdueBy.Round(time.Second), node, namespace, pod.Name)
}
