package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/goccy/go-yaml"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Kubernetes resource constants
const (
	// ConfluentNamespace     = "kcp"
	GatewayGroup          = "platform.confluent.io"
	GatewayVersion        = "v1beta1"
	GatewayResourcePlural = "gateways"
)

// Service defines gateway operations
type Service interface {
	GetGatewayYAML(ctx context.Context, namespace, gatewayName string) ([]byte, error)
	ValidateGatewayCRs(initialYAML, fencedYAML, switchoverYAML []byte) error
	CheckPermissions(ctx context.Context, verb, resource, group, namespace string) (bool, error)
	ApplyGatewayYAML(ctx context.Context, namespace, gatewayName string, yamlData []byte) error
	GetGatewayPodUIDs(ctx context.Context, namespace, gatewayName string) (map[types.UID]struct{}, error)
	WaitForGatewayPods(ctx context.Context, namespace, gatewayName string, initialPodUIDs map[types.UID]struct{}, pollInterval, timeout time.Duration, onProgress func(PodRolloutProgress)) error
	WaitForGatewayReady(ctx context.Context, namespace, gatewayName string, pollInterval, timeout time.Duration, onProgress func(GatewayReadinessProgress)) error
}

// PodRolloutProgress reports the current state of a pod rollout
type PodRolloutProgress struct {
	InitialPodCount  int
	NewPodsReady     int
	OldPodsRemaining int
	RolloutDetected  bool
}

// GatewayReadinessProgress reports the current state of a gateway readiness wait.
// The gate (whether the wait returns) comes from the operator-reported Ready
// condition on the gateway CR; pod counts are listed from the cluster for
// display only.
type GatewayReadinessProgress struct {
	InitialPodCount int
	PodsReady       int
	Elapsed         time.Duration
	RolloutDetected bool
	Ready           bool
}

// gatewayReadinessConditionType is the CFK operator's terminal "fully
// reconciled" condition on the gateway CR's status.conditions[] slice.
const gatewayReadinessConditionType = "Ready"

// gatewayReadinessDetectionWindow is the time we wait at the start of a
// readiness wait to distinguish a no-op patch from a rollout that has not
// yet incremented observedGeneration. var so tests can shorten it.
var gatewayReadinessDetectionWindow = 10 * time.Second

// K8sService implements gateway operations using Kubernetes clients
type K8sService struct {
	kubeConfigPath string
}

// NewK8sService creates a new gateway service
func NewK8sService(kubeConfigPath string) *K8sService {
	return &K8sService{
		kubeConfigPath: kubeConfigPath,
	}
}

// GetGatewayYAML retrieves the gateway resource as YAML
func (s *K8sService) GetGatewayYAML(ctx context.Context, namespace, gatewayName string) ([]byte, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gatewayGVR := schema.GroupVersionResource{
		Group:    GatewayGroup,
		Version:  GatewayVersion,
		Resource: GatewayResourcePlural,
	}

	gateway, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).
		Get(ctx, gatewayName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Gateway: %w", err)
	}

	yamlBytes, err := yaml.Marshal(gateway.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal to YAML: %w", err)
	}

	return yamlBytes, nil
}

// ValidateGatewayCRs validates that the initial, fenced, and switchover gateway CRs are consistent
func (s *K8sService) ValidateGatewayCRs(initialYAML, fencedYAML, switchoverYAML []byte) error {
	// TODO: implement cross-CR validation
	slog.Warn("gateway CR validation not yet implemented, skipping")
	return nil
}

// CheckPermissions checks if the user has the required Kubernetes permissions
func (s *K8sService) CheckPermissions(ctx context.Context, verb, resource, group, namespace string) (bool, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return false, fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return false, fmt.Errorf("failed to create clientset: %w", err)
	}

	sar := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Group:     group,
				Resource:  resource,
			},
		},
	}

	response, err := clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(
		ctx,
		sar,
		metav1.CreateOptions{},
	)

	if err != nil {
		return false, fmt.Errorf("failed to check permissions: %w", err)
	}

	return response.Status.Allowed, nil
}

// ApplyGatewayYAML applies a complete gateway CR YAML to the cluster using server-side apply
func (s *K8sService) ApplyGatewayYAML(ctx context.Context, namespace, gatewayName string, yamlData []byte) error {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gatewayGVR := schema.GroupVersionResource{
		Group:    GatewayGroup,
		Version:  GatewayVersion,
		Resource: GatewayResourcePlural,
	}

	// Parse YAML into unstructured object
	var obj unstructured.Unstructured
	if err := yaml.Unmarshal(yamlData, &obj.Object); err != nil {
		return fmt.Errorf("failed to parse gateway YAML: %w", err)
	}

	// Ensure metadata matches the expected resource
	obj.SetName(gatewayName)
	obj.SetNamespace(namespace)

	_, err = dynamicClient.Resource(gatewayGVR).Namespace(namespace).
		Apply(ctx, gatewayName, &obj, metav1.ApplyOptions{
			FieldManager: "kcp-migration",
			Force:        true,
		})
	if err != nil {
		return fmt.Errorf("failed to apply gateway YAML: %w", err)
	}

	return nil
}

// GetGatewayPodUIDs returns a set of UIDs for the current gateway pods.
// This should be called BEFORE patching the gateway to capture the initial pod state.
func (s *K8sService) GetGatewayPodUIDs(ctx context.Context, namespace, gatewayName string) (map[types.UID]struct{}, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	labelSelector := fmt.Sprintf("app=%s", gatewayName)

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list gateway pods: %w", err)
	}

	uids := make(map[types.UID]struct{}, len(pods.Items))
	for _, pod := range pods.Items {
		uids[pod.UID] = struct{}{}
	}
	return uids, nil
}

// WaitForGatewayPods waits for all gateway pods to be completely replaced after a config change.
//
// After patching the Gateway CRD, the Confluent operator triggers a rolling restart of gateway pods.
// This method polls until all initial pods are replaced with new pods and all are ready, ensuring
// the rollout is truly complete even when maxSurge temporarily creates extra pods.
//
// The initialPodUIDs parameter must contain the pod UIDs captured BEFORE the gateway patch is applied.
// This is critical to avoid race conditions where the rollout completes before we capture the initial state.
//
// The method works in two phases:
//  1. Wait for change detection: Wait up to 10 seconds to detect if rollout starts
//  2. Wait for complete replacement: Ensure all initial pods are replaced and new pods are ready
//
// This prevents returning prematurely when maxSurge creates extra pods during the rollout.
func (s *K8sService) WaitForGatewayPods(ctx context.Context, namespace, gatewayName string, initialPodUIDs map[types.UID]struct{}, pollInterval, timeout time.Duration, onProgress func(PodRolloutProgress)) error {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	// Confluent CFK labels gateway pods with app=<gateway-crd-name>
	labelSelector := fmt.Sprintf("app=%s", gatewayName)

	// Calculate initial pod count from the passed UIDs
	initialPodCount := len(initialPodUIDs)
	slog.Debug("waiting for gateway pod rollout", "namespace", namespace, "gateway", gatewayName, "initialPodCount", initialPodCount)

	// Phase 1: Wait for rollout to start (10 second detection window)
	changeDetectionDeadline := time.Now().Add(10 * time.Second)
	rolloutDetected := false

	for time.Now().Before(changeDetectionDeadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return fmt.Errorf("failed to list gateway pods: %w", err)
		}

		// Check if any pod changed (new UID) or became not ready
		for _, pod := range pods.Items {
			if _, wasInitial := initialPodUIDs[pod.UID]; !wasInitial {
				slog.Debug("new pod detected, rollout started", "pod", pod.Name)
				rolloutDetected = true
				break
			}
			if !isPodReady(&pod) {
				slog.Debug("pod not ready, rollout started", "pod", pod.Name)
				rolloutDetected = true
				break
			}
		}

		if rolloutDetected {
			break
		}

		time.Sleep(pollInterval)
	}

	if !rolloutDetected {
		slog.Debug("no rollout detected within 10 seconds, assuming config change did not require pod restart")
		if onProgress != nil {
			onProgress(PodRolloutProgress{
				InitialPodCount:  initialPodCount,
				NewPodsReady:     initialPodCount,
				OldPodsRemaining: 0,
				RolloutDetected:  false,
			})
		}
		return nil
	}

	// Phase 2: Wait for all pods to be completely replaced
	slog.Debug("rollout detected, waiting for complete pod replacement")

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return fmt.Errorf("failed to list gateway pods: %w", err)
		}

		newPodsReady := countNewReadyPods(pods.Items, initialPodUIDs)
		oldPodsRemaining := countOldPods(pods.Items, initialPodUIDs)

		slog.Debug("pod rollout progress",
			"newPodsReady", fmt.Sprintf("%d/%d", newPodsReady, initialPodCount),
			"oldPodsRemaining", oldPodsRemaining)

		if onProgress != nil {
			onProgress(PodRolloutProgress{
				InitialPodCount:  initialPodCount,
				NewPodsReady:     newPodsReady,
				OldPodsRemaining: oldPodsRemaining,
				RolloutDetected:  true,
			})
		}

		// Check completion: all old pods gone, all new pods ready, correct count
		if oldPodsRemaining == 0 && newPodsReady == initialPodCount && len(pods.Items) == initialPodCount {
			slog.Debug("all gateway pods replaced and ready", "podCount", initialPodCount)
			return nil
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timed out waiting for gateway pods to be replaced (timeout: %s)", timeout)
}

// countNewReadyPods counts how many new pods (not in initial set) are ready
func countNewReadyPods(currentPods []corev1.Pod, initialUIDs map[types.UID]struct{}) int {
	count := 0
	for _, pod := range currentPods {
		if _, wasInitial := initialUIDs[pod.UID]; !wasInitial && isPodReady(&pod) {
			count++
		}
	}
	return count
}

// countOldPods counts how many initial pods are still present
func countOldPods(currentPods []corev1.Pod, initialUIDs map[types.UID]struct{}) int {
	count := 0
	for _, pod := range currentPods {
		if _, wasInitial := initialUIDs[pod.UID]; wasInitial {
			count++
		}
	}
	return count
}

// isPodReady checks if a pod has the Ready condition set to True
func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// WaitForGatewayReady polls the gateway CR's operator-reported status until
// the operator reports the gateway is reconciled and Ready, or until ctx is
// cancelled. When timeout > 0, a hard deadline is applied via context; a
// timeout of 0 means no deadline.
//
// The wait runs in two phases:
//  1. Detection (up to 10s): determines whether the patch the caller just
//     applied triggered a rollout. If the CR is already reconciled at the
//     captured generation with Ready=True for the whole window, no pod
//     restart was required — onProgress is invoked once with
//     RolloutDetected=false and the wait returns nil.
//  2. Convergence: polls until observedGeneration is at least the captured
//     generation AND the Ready condition is True, calling onProgress on
//     every poll tick with monotonically increasing Elapsed.
//
// Pod counts in progress callbacks come from listing pods by the
// `app=<gatewayName>` label and are display-only — only the operator-reported
// readiness gates the return.
func (s *K8sService) WaitForGatewayReady(ctx context.Context, namespace, gatewayName string, pollInterval, timeout time.Duration, onProgress func(GatewayReadinessProgress)) error {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}
	return waitForGatewayReady(ctx, dynamicClient, clientset, namespace, gatewayName, pollInterval, timeout, onProgress)
}

// waitForGatewayReady is the inner orchestration used by WaitForGatewayReady.
// It is split from the method so unit tests can inject fake clients.
func waitForGatewayReady(ctx context.Context, dynamicClient dynamic.Interface, clientset kubernetes.Interface, namespace, gatewayName string, pollInterval, timeout time.Duration, onProgress func(GatewayReadinessProgress)) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	gatewayGVR := schema.GroupVersionResource{
		Group:    GatewayGroup,
		Version:  GatewayVersion,
		Resource: GatewayResourcePlural,
	}

	capturedGen, err := readGatewayGeneration(ctx, dynamicClient, gatewayGVR, namespace, gatewayName)
	if err != nil {
		return err
	}
	slog.Debug("captured initial gateway generation", "namespace", namespace, "gateway", gatewayName, "generation", capturedGen)

	initialPodCount, _, podErr := countGatewayPods(ctx, clientset, namespace, gatewayName)
	if podErr != nil {
		slog.Warn("failed to count gateway pods at start of readiness wait", "error", podErr)
	}
	start := time.Now()

	// Phase 1: detection window.
	detectionDeadline := time.Now().Add(gatewayReadinessDetectionWindow)
	rolloutDetected := false
	for time.Now().Before(detectionDeadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		cr, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).Get(ctx, gatewayName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to read gateway CR: %w", err)
		}
		if !gatewayReadinessAtOrAfter(cr, capturedGen) {
			slog.Debug("rollout detected during detection window", "gateway", gatewayName, "generation", capturedGen)
			rolloutDetected = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	if !rolloutDetected {
		slog.Debug("no rollout detected within detection window; treating patch as no-op", "gateway", gatewayName)
		if onProgress != nil {
			onProgress(GatewayReadinessProgress{
				InitialPodCount: initialPodCount,
				PodsReady:       initialPodCount,
				Elapsed:         time.Since(start),
				RolloutDetected: false,
				Ready:           true,
			})
		}
		return nil
	}

	// Phase 2: convergence — poll until the operator reports reconciled.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		cr, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).Get(ctx, gatewayName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to read gateway CR: %w", err)
		}
		ready := gatewayReadinessAtOrAfter(cr, capturedGen)
		podsReady, _, listErr := countGatewayPods(ctx, clientset, namespace, gatewayName)
		if listErr != nil {
			slog.Debug("failed to count gateway pods during convergence", "error", listErr)
		}
		if onProgress != nil {
			onProgress(GatewayReadinessProgress{
				InitialPodCount: initialPodCount,
				PodsReady:       podsReady,
				Elapsed:         time.Since(start),
				RolloutDetected: true,
				Ready:           ready,
			})
		}
		if ready {
			slog.Debug("gateway reported ready", "gateway", gatewayName, "elapsed", time.Since(start))
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// gatewayReadinessAtOrAfter reports whether the CR's status indicates the
// operator has reconciled at observedGeneration >= capturedGen AND a Ready
// condition with status=True is present. When observedGeneration is absent
// (older operators), only the Ready=True condition is checked; this fallback
// cannot distinguish a stale Ready from a post-patch Ready.
func gatewayReadinessAtOrAfter(cr *unstructured.Unstructured, capturedGen int64) bool {
	if cr == nil {
		return false
	}
	if obsGen, found, _ := unstructured.NestedInt64(cr.Object, "status", "observedGeneration"); found {
		if obsGen < capturedGen {
			return false
		}
	}
	conditions, found, _ := unstructured.NestedSlice(cr.Object, "status", "conditions")
	if !found {
		return false
	}
	for _, c := range conditions {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cm["type"] == gatewayReadinessConditionType && cm["status"] == "True" {
			return true
		}
	}
	return false
}

func readGatewayGeneration(ctx context.Context, dynamicClient dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) (int64, error) {
	cr, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to read gateway CR generation: %w", err)
	}
	return cr.GetGeneration(), nil
}

func countGatewayPods(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string) (ready, total int, err error) {
	labelSelector := fmt.Sprintf("app=%s", gatewayName)
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to list gateway pods: %w", err)
	}
	for _, pod := range pods.Items {
		total++
		if isPodReady(&pod) {
			ready++
		}
	}
	return ready, total, nil
}
