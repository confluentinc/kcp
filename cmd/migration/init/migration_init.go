package migration_init

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/goccy/go-yaml"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type MigrationInitOpts struct {
	stateFile string
	state     types.State

	gatewayNamespace string
	gatewayCrdName   string
	kubeConfigPath   string

	clusterId           string
	clusterRestEndpoint string
	clusterLinkName     string
	clusterApiKey       string
	clusterApiSecret    string
	topics              []string
	authMode            string
}

type MigrationInit struct {
	stateFile string
	state     types.State

	gatewayNamespace string
	gatewayCrdName   string
	kubeConfigPath   string

	clusterId           string
	clusterRestEndpoint string
	clusterLinkName     string
	clusterApiKey       string
	clusterApiSecret    string
	topics              []string
	authMode            string
}

func NewMigrationInit(opts MigrationInitOpts) *MigrationInit {
	return &MigrationInit{
		stateFile:           opts.stateFile,
		state:               opts.state,
		gatewayNamespace:    opts.gatewayNamespace,
		gatewayCrdName:      opts.gatewayCrdName,
		kubeConfigPath:      opts.kubeConfigPath,
		clusterId:           opts.clusterId,
		clusterRestEndpoint: opts.clusterRestEndpoint,
		clusterLinkName:     opts.clusterLinkName,
		clusterApiKey:       opts.clusterApiKey,
		clusterApiSecret:    opts.clusterApiSecret,
		topics:              opts.topics,
		authMode:            opts.authMode,
	}
}

func (m *MigrationInit) Run() error {
	slog.Info("parsing gateway resource", "gatewayName", m.gatewayNamespace, "kubeConfigPath", m.kubeConfigPath)

	config, err := clientcmd.BuildConfigFromFlags("", m.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %v", err)
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %v", err)
	}

	allowed, err := checkPermission(clientset, "update", "gateways", "platform.confluent.io", "confluent")
	if err != nil {
		return fmt.Errorf("permission check failed: %v", err)
	}

	if !allowed {
		return fmt.Errorf("you don't have permission to update gateway resources")
	}

	// create dynamic client for custom resources
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create dynamic client: %v\n", err)
		os.Exit(1)
	}

	listPods(clientset, m.gatewayNamespace)
	getGatewayAsYAML(dynamicClient, "confluent", m.gatewayCrdName)

	slog.Info("describing cluster link", "clusterId", m.clusterId, "clusterLinkName", m.clusterLinkName)
	clusterLinkTopics, err := listMirrorTopics(m.clusterRestEndpoint, m.clusterId, m.clusterLinkName, m.clusterApiKey, m.clusterApiSecret)
	if err != nil {
		return err
	}

	if len(m.topics) > 0 {
		slog.Info("validating topics in cluster link", "topic count", len(m.topics))
		if err := validateTopicsInClusterLink(m.topics, clusterLinkTopics); err != nil {
			return err
		}
	} else {
		m.topics = clusterLinkTopics
	}

	clusterLinkConfigs, err := listClusterLinkConfigs(m.clusterRestEndpoint, m.clusterId, m.clusterLinkName, m.clusterApiKey, m.clusterApiSecret)
	if err != nil {
		return err
	}

	migrationId := fmt.Sprintf("migration-%s", time.Now().Format("20060102-150405"))
	m.state.Migrations = append(m.state.Migrations, types.Migration{
		MigrationId:         migrationId,
		GatewayNamespace:    m.gatewayNamespace,
		GatewayCrdName:      m.gatewayCrdName,
		ClusterId:           m.clusterId,
		ClusterRestEndpoint: m.clusterRestEndpoint,
		ClusterLinkName:     m.clusterLinkName,
		Topics:              m.topics,
		AuthMode:            m.authMode,
		ClusterLinkConfigs:  clusterLinkConfigs,
		CreatedAt:           time.Now(),
	})

	return m.state.PersistStateFile(m.stateFile)
}

func checkPermission(clientset *kubernetes.Clientset, verb, resource, group, namespace string) (bool, error) {
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
		context.Background(),
		sar,
		metav1.CreateOptions{},
	)

	if err != nil {
		return false, fmt.Errorf("failed to check permissions: %w", err)
	}

	slog.Info("permission check response", "verb", verb, "allowed", response.Status.Allowed)

	return response.Status.Allowed, nil
}

func listPods(clientset *kubernetes.Clientset, namespace string) {
	fmt.Printf("📋 Pods in namespace '%s':\n\n", namespace)

	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list pods: %v\n", err)
		os.Exit(1)
	}

	if len(pods.Items) == 0 {
		fmt.Println("No pods found.")
		return
	}

	fmt.Printf("%-50s %-10s %-10s %-10s\n", "NAME", "STATUS", "RESTARTS", "AGE")
	fmt.Println(strings.Repeat("-", 80))

	for _, pod := range pods.Items {
		restarts := int32(0)
		if len(pod.Status.ContainerStatuses) > 0 {
			restarts = pod.Status.ContainerStatuses[0].RestartCount
		}
		age := time.Since(pod.CreationTimestamp.Time).Round(time.Second)
		fmt.Printf("%-50s %-10s %-10d %-10s\n",
			pod.Name,
			string(pod.Status.Phase),
			restarts,
			age.String())
	}
}

func printDeploymentYAML(clientset *kubernetes.Clientset, namespace, deployName string) {
	// Get deployment from cluster
	deploy, err := clientset.AppsV1().Deployments(namespace).Get(context.TODO(), deployName, metav1.GetOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get deployment: %v\n", err)
		os.Exit(1)
	}

	// Convert to YAML
	yamlBytes, err := yaml.Marshal(deploy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal to YAML: %v\n", err)
		os.Exit(1)
	}

	// Print formatted YAML
	fmt.Println(string(yamlBytes))
}

func getGatewayAsYAML(dynamicClient dynamic.Interface, namespace, gatewayName string) {
	// Define the GVR (GroupVersionResource) for Gateway
	// This matches: kubectl get gateway -n confluent
	gatewayGVR := schema.GroupVersionResource{
		Group:    "platform.confluent.io",
		Version:  "v1beta1",
		Resource: "gateways", // plural form
	}

	// Get the Gateway custom resource from the namespace
	gateway, err := dynamicClient.Resource(gatewayGVR).Namespace(namespace).
		Get(context.TODO(), gatewayName, metav1.GetOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get Gateway: %v\n", err)
		os.Exit(1)
	}

	// Convert to YAML
	yamlBytes, err := yaml.Marshal(gateway.Object)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal to YAML: %v\n", err)
		os.Exit(1)
	}

	// Print formatted YAML
	// fmt.Println(string(yamlBytes))
	_ = yamlBytes
}

func describeClusterLink(clusterRestEndpoint, clusterId, clusterLinkName, clusterApiKey, clusterApiSecret string) error {
	url := fmt.Sprintf("%s/kafka/v3/clusters/%s/links/%s/mirrors", clusterRestEndpoint, clusterId, clusterLinkName)
	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", clusterApiKey, clusterApiSecret)))

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Authorization", "Basic "+auth)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to describe cluster link: %v", err)
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to describe cluster link: %v", res.Status)
	}

	var response struct {
		LinkError        string `json:"link_error"`
		LinkErrorMessage string `json:"link_error_message"`
		LinkState        string `json:"link_state"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to unmarshal cluster link response: %v", err)
	}

	if response.LinkState != "ACTIVE" || response.LinkError != "NO_ERROR" {
		return fmt.Errorf("there is a problem with the cluster link", "link_state", response.LinkState, "link_error", response.LinkError, "link_error_message", response.LinkErrorMessage)
	}

	return nil
}

func listMirrorTopics(clusterRestEndpoint, clusterId, clusterLinkName, clusterApiKey, clusterApiSecret string) ([]string, error) {
	url := fmt.Sprintf("%s/kafka/v3/clusters/%s/links/%s/mirrors", clusterRestEndpoint, clusterId, clusterLinkName)
	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", clusterApiKey, clusterApiSecret)))

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Authorization", "Basic "+auth)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster link mirror topics: %v", err)
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list cluster link mirror topics: %v", res.Status)
	}

	var response struct {
		Data []struct {
			MirrorTopicName string `json:"mirror_topic_name"`
			MirrorStatus    string `json:"mirror_status"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cluster link mirror topics response: %v", err)
	}

	if len(response.Data) == 0 {
		return nil, fmt.Errorf("no mirror topics found in cluster link")
	}

	var topicNames []string
	var inactiveTopics []string
	for _, mirror := range response.Data {
		topicNames = append(topicNames, mirror.MirrorTopicName)
		if mirror.MirrorStatus != "ACTIVE" {
			inactiveTopics = append(inactiveTopics, fmt.Sprintf("%s (status: %s)", mirror.MirrorTopicName, mirror.MirrorStatus))
		}
	}

	if len(inactiveTopics) > 0 {
		return nil, fmt.Errorf("%d mirror topics are not active: %s", len(inactiveTopics), strings.Join(inactiveTopics, ", "))
	}

	return topicNames, nil
}

func validateTopicsInClusterLink(topics []string, clusterLinkTopics []string) error {
	for _, topic := range topics {
		if !slices.Contains(clusterLinkTopics, topic) {
			return fmt.Errorf("topic %s not found in cluster link", topic)
		}
	}

	return nil
}

func listClusterLinkConfigs(clusterRestEndpoint, clusterId, clusterLinkName, clusterApiKey, clusterApiSecret string) (map[string]string, error) {
	url := fmt.Sprintf("%s/kafka/v3/clusters/%s/links/%s/configs", clusterRestEndpoint, clusterId, clusterLinkName)
	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", clusterApiKey, clusterApiSecret)))

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Authorization", "Basic "+auth)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster link configs: %v", err)
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list cluster link configs: %v", res.Status)
	}

	var response struct {
		Data []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cluster link configs response: %v", err)
	}

	configs := make(map[string]string)
	for _, config := range response.Data {
		configs[config.Name] = config.Value
	}

	return configs, nil
}
