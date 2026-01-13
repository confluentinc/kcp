package migration

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

	"github.com/goccy/go-yaml"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type MigrationOpts struct {
	gatewayName    string
	gatewayCrdName string
	kubeConfigPath string

	clusterId           string
	clusterRestEndpoint string
	clusterLinkName     string
	clusterApiKey       string
	clusterApiSecret    string
	topics              []string
}

type Migration struct {
	gatewayName    string
	gatewayCrdName string
	kubeConfigPath string

	clusterId           string
	clusterRestEndpoint string
	clusterLinkName     string
	clusterApiKey       string
	clusterApiSecret    string
	topics              []string
}

func NewMigration(opts MigrationOpts) *Migration {
	return &Migration{
		gatewayName:         opts.gatewayName,
		gatewayCrdName:      opts.gatewayCrdName,
		kubeConfigPath:      opts.kubeConfigPath,
		clusterId:           opts.clusterId,
		clusterRestEndpoint: opts.clusterRestEndpoint,
		clusterLinkName:     opts.clusterLinkName,
		clusterApiKey:       opts.clusterApiKey,
		clusterApiSecret:    opts.clusterApiSecret,
		topics:              opts.topics,
	}
}

func (m *Migration) Run() error {
	slog.Info("parsing gateway resource", "gatewayName", m.gatewayName, "kubeConfigPath", m.kubeConfigPath)

	config, err := clientcmd.BuildConfigFromFlags("", m.kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config: %v", err)
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %v", err)
	}

	// create dynamic client for custom resources
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create dynamic client: %v\n", err)
		os.Exit(1)
	}

	listPods(clientset, m.gatewayName)
	getGatewayAsYAML(dynamicClient, "confluent", m.gatewayCrdName)

	slog.Info("describing cluster link", "clusterId", m.clusterId, "clusterLinkName", m.clusterLinkName)
	respBody, err := describeClusterLink(m.clusterId, m.clusterRestEndpoint, m.clusterLinkName, m.clusterApiKey, m.clusterApiSecret)
	if err != nil {
		return err
	}

	if err := validateTopicsInClusterLink(m.topics, respBody); err != nil {
		return err
	}

	return nil
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
	fmt.Println(string(yamlBytes))
}

func describeClusterLink(clusterId, clusterRestEndpoint, clusterLinkName, clusterApiKey, clusterApiSecret string) (string, error) {
	url := fmt.Sprintf("%s/kafka/v3/clusters/%s/links/%s", clusterRestEndpoint, clusterId, clusterLinkName)
	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", clusterApiKey, clusterApiSecret)))

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Authorization", "Basic "+auth)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to describe cluster link: %v", err)
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to describe cluster link: %v", res.Status)
	}
	return string(body), nil
}

func validateTopicsInClusterLink(topics []string, clusterLinkBody string) error {
	var response struct {
		TopicNames []string `json:"topic_names"`
	}
	
	if err := json.Unmarshal([]byte(clusterLinkBody), &response); err != nil {
		return fmt.Errorf("failed to unmarshal cluster link body: %v", err)
	}
	clusterLinkTopics := response.TopicNames

	for _, topic := range topics {
		if !slices.Contains(clusterLinkTopics, topic) {
			return fmt.Errorf("topic %s not found in cluster link", topic)
		}

		fmt.Println("topic found in cluster link", topic)
	}

	return nil
}
