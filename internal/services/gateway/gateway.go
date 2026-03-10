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
}

// PodRolloutProgress reports the current state of a pod rollout
type PodRolloutProgress struct {
	InitialPodCount int
	ReplacedCount   int
	ReadyCount      int
	RolloutDetected bool
}

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
				InitialPodCount: initialPodCount,
				ReplacedCount:   initialPodCount,
				ReadyCount:      initialPodCount,
				RolloutDetected: false,
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

		replacedCount := countReplacedPods(pods.Items, initialPodUIDs)
		readyCount := countReadyPods(pods.Items)

		slog.Debug("pod rollout progress",
			"replaced", fmt.Sprintf("%d/%d", replacedCount, initialPodCount),
			"ready", fmt.Sprintf("%d/%d", readyCount, initialPodCount))

		if onProgress != nil {
			onProgress(PodRolloutProgress{
				InitialPodCount: initialPodCount,
				ReplacedCount:   replacedCount,
				ReadyCount:      readyCount,
				RolloutDetected: true,
			})
		}

		// Check completion: all replaced and all ready
		if allPodsReplaced(pods.Items, initialPodUIDs) && len(pods.Items) == initialPodCount && readyCount == initialPodCount {
			slog.Debug("all gateway pods replaced and ready", "podCount", initialPodCount)
			return nil
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timed out waiting for gateway pods to be replaced (timeout: %s)", timeout)
}

// allPodsReplaced checks if all current pods have different UIDs from initial set
func allPodsReplaced(currentPods []corev1.Pod, initialUIDs map[types.UID]struct{}) bool {
	for _, pod := range currentPods {
		if _, wasInitial := initialUIDs[pod.UID]; wasInitial {
			return false // Found an old pod still running
		}
	}
	return true
}

// countReplacedPods counts how many current pods have new UIDs (not in initial set)
func countReplacedPods(currentPods []corev1.Pod, initialUIDs map[types.UID]struct{}) int {
	count := 0
	for _, pod := range currentPods {
		if _, wasInitial := initialUIDs[pod.UID]; !wasInitial {
			count++
		}
	}
	return count
}

// countReadyPods returns count of ready pods
func countReadyPods(pods []corev1.Pod) int {
	count := 0
	for _, pod := range pods {
		if isPodReady(&pod) {
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

// GatewayResource represents the Kubernetes Gateway CRD structure
type GatewayResource struct {
	Spec GatewaySpec `yaml:"spec"`
}

type GatewaySpec struct {
	StreamingDomains []StreamingDomain `yaml:"streamingDomains"`
	Routes           []Route           `yaml:"routes"`
}

type StreamingDomain struct {
	Name string `yaml:"name"`
}

type Route struct {
	Name            string          `yaml:"name"`
	StreamingDomain StreamingDomain `yaml:"streamingDomain"`
	Security        Security        `yaml:"security"`
}

type Security struct {
	Auth    string  `yaml:"auth"`
	Client  Client  `yaml:"client"`
	Cluster Cluster `yaml:"cluster"`
}

type Client struct {
	Authentication Authentication `yaml:"authentication"`
}

type Cluster struct {
	Authentication Authentication `yaml:"authentication"`
}

type Authentication struct {
	Type string `yaml:"type"`
}
