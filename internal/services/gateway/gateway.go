package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/goccy/go-yaml"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// GatewayConfig holds gateway configuration
type GatewayConfig struct {
	Namespace            string
	CRDName              string
	SourceName           string
	DestinationName      string
	SourceRouteName      string
	DestinationRouteName string
	AuthMode             string
	KubeConfigPath       string
}

// Service defines gateway operations
type Service interface {
	GetGatewayYAML(ctx context.Context, namespace, gatewayName string) ([]byte, error)
	ValidateGateway(ctx context.Context, yaml []byte, config GatewayConfig) error
	CheckPermissions(ctx context.Context, verb, resource, group, namespace string) (bool, error)
	PatchGateway(ctx context.Context, namespace, gatewayName string, patchOps []map[string]interface{}) error
	WaitForGatewayPods(ctx context.Context, namespace, gatewayName string, pollInterval, timeout time.Duration) error
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

// ValidateGateway validates gateway YAML contains expected configuration
func (s *K8sService) ValidateGateway(ctx context.Context, gatewayYAML []byte, config GatewayConfig) error {
	var gateway GatewayResource
	if err := yaml.Unmarshal(gatewayYAML, &gateway); err != nil {
		return fmt.Errorf("failed to parse gateway YAML: %w", err)
	}

	if err := validateStreamingDomains(gateway, config); err != nil {
		return err
	}

	if err := validateRoutes(gateway, config); err != nil {
		return err
	}

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

// PatchGateway patches the gateway resource using JSON patch format
func (s *K8sService) PatchGateway(ctx context.Context, namespace, gatewayName string, patchOps []map[string]any) error {
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

	// Marshal patch operations to JSON
	patchBytes, err := json.Marshal(patchOps)
	if err != nil {
		return fmt.Errorf("failed to marshal patch operations: %w", err)
	}

	// Apply JSON patch
	_, err = dynamicClient.Resource(gatewayGVR).Namespace(namespace).
		Patch(ctx, gatewayName, types.JSONPatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to patch Gateway: %w", err)
	}

	return nil
}

// WaitForGatewayPods waits for all gateway pods to be running and ready after a config change.
//
// After patching the Gateway CRD, the Confluent operator triggers a rolling restart of gateway pods.
// This method polls until all pods matching the gateway are in a Ready state, indicating the
// rollout is complete and the gateway is healthy with the new configuration.
//
// The method works in two phases:
//  1. Detect rollout started: waits for at least one pod to be in a non-Ready state (being recycled)
//  2. Detect rollout complete: waits for ALL pods to be Ready
//
// This ensures we don't return prematurely if the operator hasn't started the rollout yet.
func (s *K8sService) WaitForGatewayPods(ctx context.Context, namespace, gatewayName string, pollInterval, timeout time.Duration) error {
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

	deadline := time.Now().Add(timeout)

	// Phase 1: Wait for rollout to start (at least one pod not Ready, or pod restart detected)
	slog.Info("waiting for gateway rollout to start", "namespace", namespace, "gateway", gatewayName, "labelSelector", labelSelector)

	initialPodUIDs, err := s.getGatewayPodUIDs(ctx, clientset, namespace, labelSelector)
	if err != nil {
		return fmt.Errorf("failed to get initial pod UIDs: %w", err)
	}
	slog.Info("recorded initial pod state", "podCount", len(initialPodUIDs))

	rolloutStarted := false
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

		// Check if any new pods appeared (new UIDs) or any pod is not Ready
		for _, pod := range pods.Items {
			if _, existed := initialPodUIDs[pod.UID]; !existed {
				slog.Info("new pod detected, rollout has started", "pod", pod.Name)
				rolloutStarted = true
				break
			}
			if !isPodReady(&pod) {
				slog.Info("pod not ready, rollout has started", "pod", pod.Name, "phase", pod.Status.Phase)
				rolloutStarted = true
				break
			}
		}

		if rolloutStarted {
			break
		}

		slog.Debug("rollout not yet started, waiting...", "pollInterval", pollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	if !rolloutStarted {
		slog.Warn("rollout did not start within timeout, proceeding anyway (operator may have applied config without restart)")
		return nil
	}

	// Phase 2: Wait for all pods to be Ready
	slog.Info("rollout started, waiting for all gateway pods to become ready")

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

		if len(pods.Items) == 0 {
			slog.Info("no gateway pods found yet, waiting...")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pollInterval):
			}
			continue
		}

		allReady := true
		readyCount := 0
		for _, pod := range pods.Items {
			if isPodReady(&pod) {
				readyCount++
			} else {
				allReady = false
			}
		}

		if allReady {
			slog.Info("all gateway pods are ready", "podCount", len(pods.Items))
			return nil
		}

		slog.Info("rollout in progress...", "ready", fmt.Sprintf("%d/%d", readyCount, len(pods.Items)))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return fmt.Errorf("timed out waiting for gateway pods to become ready (timeout: %s)", timeout)
}

// getGatewayPodUIDs returns a set of UIDs for the current gateway pods
func (s *K8sService) getGatewayPodUIDs(ctx context.Context, clientset kubernetes.Interface, namespace, labelSelector string) (map[types.UID]struct{}, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
	}

	uids := make(map[types.UID]struct{}, len(pods.Items))
	for _, pod := range pods.Items {
		uids[pod.UID] = struct{}{}
	}
	return uids, nil
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

// validateStreamingDomains validates streaming domains exist in gateway
func validateStreamingDomains(gateway GatewayResource, config GatewayConfig) error {
	streamingDomainNames := make([]string, len(gateway.Spec.StreamingDomains))
	for i, domain := range gateway.Spec.StreamingDomains {
		streamingDomainNames[i] = domain.Name
	}

	if !slices.Contains(streamingDomainNames, config.SourceName) {
		return fmt.Errorf("source streaming domain '%s' not found in gateway streamingDomains. Available domains: %v",
			config.SourceName, streamingDomainNames)
	}

	// if !slices.Contains(streamingDomainNames, config.DestinationName) {
	// 	return fmt.Errorf("destination streaming domain '%s' not found in gateway streamingDomains. Available domains: %v",
	// 		config.DestinationName, streamingDomainNames)
	// }

	return nil
}

// validateRoutes validates routes exist and have correct configuration
func validateRoutes(gateway GatewayResource, config GatewayConfig) error {
	routeNames := make([]string, len(gateway.Spec.Routes))
	for i, route := range gateway.Spec.Routes {
		routeNames[i] = route.Name

		if err := validateSourceRoute(route, config); err != nil {
			return err
		}

		if err := validateDestinationRoute(route, config); err != nil {
			return err
		}
	}

	if !slices.Contains(routeNames, config.SourceRouteName) {
		return fmt.Errorf("source route '%s' not found in gateway routes. Available routes: %v",
			config.SourceRouteName, routeNames)
	}

	// if !slices.Contains(routeNames, config.DestinationRouteName) {
	// 	return fmt.Errorf("destination route '%s' not found in gateway routes. Available routes: %v",
	// 		config.DestinationRouteName, routeNames)
	// }

	return nil
}

// validateSourceRoute validates source route configuration
func validateSourceRoute(route Route, config GatewayConfig) error {
	if route.Name != config.SourceRouteName {
		return nil
	}

	if route.StreamingDomain.Name != config.SourceName {
		return fmt.Errorf("source route '%s' streaming domain '%s' does not match expected source streaming domain '%s'",
			route.Name, route.StreamingDomain.Name, config.SourceName)
	}

	/*
		- If the `auth_mode` is passed as/defaulted to 'dest_swap', we would expect the user's route to be 'passthrough'.
			Then the future route would be 'swap' so that the clients do not need to update their credentials.
		- If the `auth_mode` is passed as 'source_swap', we would expect the user's route to be a 'swap'.
			Then the future route would be 'passthrough' as the clients already use the CC credentials.
	*/
	if config.AuthMode == "dest_swap" && route.Security.Auth != "passthrough" {
		return fmt.Errorf("source route '%s' expected to be 'passthrough', found '%s'",
			config.SourceRouteName, route.Security.Auth)
	}

	if config.AuthMode == "source_swap" && route.Security.Auth != "swap" {
		return fmt.Errorf("source route '%s' expected to be 'swap', found '%s'",
			config.SourceRouteName, route.Security.Auth)
	}

	return nil
}

// validateDestinationRoute validates destination route configuration
func validateDestinationRoute(route Route, config GatewayConfig) error {
	if route.Name != config.DestinationRouteName {
		return nil
	}

	if route.StreamingDomain.Name != config.DestinationName {
		return fmt.Errorf("destination route '%s' streaming domain '%s' does not match expected destination streaming domain '%s'",
			config.DestinationRouteName, route.StreamingDomain.Name, config.DestinationName)
	}

	if route.Security.Client.Authentication.Type == "" {
		return fmt.Errorf("destination route '%s' is missing client authentication configuration",
			config.DestinationRouteName)
	}

	if route.Security.Cluster.Authentication.Type == "" {
		return fmt.Errorf("destination route '%s' is missing cluster authentication configuration",
			config.DestinationRouteName)
	}

	return nil
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
