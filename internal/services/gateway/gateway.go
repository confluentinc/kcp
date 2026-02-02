package gateway

import (
	"context"
	"fmt"
	"slices"

	"github.com/goccy/go-yaml"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Kubernetes resource constants
const (
	// ConfluentNamespace     = "kcp"
	GatewayGroup           = "platform.confluent.io"
	GatewayVersion         = "v1beta1"
	GatewayResourcePlural  = "gateways"
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
	Name             string           `yaml:"name"`
	StreamingDomain  StreamingDomain  `yaml:"streamingDomain"`
	Security         Security         `yaml:"security"`
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
