package types

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
	"github.com/looplab/fsm"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FSM State constants
const (
	StateUninitialized = "uninitialized"
	StateInitialized   = "initialized"
	StateMigrating     = "migrating"
	StateMigrated      = "migrated"
)

// FSM Event constants
const (
	EventKcpInit        = "kcp_init"
	EventKcpExecute     = "kcp_execute"
	EventTopicsPromoted = "topics_promoted"
)

type MigrationOpts struct {
	GatewayNamespace    string
	GatewayCrdName      string
	KubeConfigPath      string
	ClusterId           string
	ClusterRestEndpoint string
	ClusterLinkName     string
	Topics              []string
	AuthMode            string
	ClusterApiKey       string
	ClusterApiSecret    string
}

// Migration represents a gateway migration with a finite state machine

type Migration struct {
	MigrationId  string   `json:"migration_id"`
	CurrentState string   `json:"current_state"`
	FSM          *fsm.FSM `json:"-"`

	GatewayNamespace string `json:"gateway_namespace"`
	GatewayCrdName   string `json:"gateway_crd_name"`
	KubeConfigPath   string `json:"kube_config_path"`

	ClusterId           string            `json:"cluster_id"`
	ClusterRestEndpoint string            `json:"cluster_rest_endpoint"`
	ClusterLinkName     string            `json:"cluster_link_name"`
	ClusterApiKey       string            `json:"-"`
	ClusterApiSecret    string            `json:"-"`
	Topics              []string          `json:"topics"`
	AuthMode            string            `json:"auth_mode"`
	ClusterLinkConfigs  map[string]string `json:"cluster_link_configs"`
	GatewayOriginalYAML []byte            `json:"gateway_original_yaml"`
}

// initializeFSM sets up the FSM for the migration with the given initial state
func (m *Migration) initializeFSM(initialState string) {
	m.FSM = fsm.NewFSM(
		initialState,
		fsm.Events{
			//  transitions from uninitialized state
			// {Name: EventKcpInit, Src: []string{StateUninitialized}, Dst: StateUninitialized},
			{Name: EventKcpInit, Src: []string{StateUninitialized}, Dst: StateInitialized},
		},
		fsm.Callbacks{
			"leave_" + StateUninitialized: func(_ context.Context, e *fsm.Event) {
				fmt.Println("leaving uninitialized state")
				m.leaveInitialized(e)
			},
			"after_event": func(_ context.Context, e *fsm.Event) {
				m.CurrentState = m.FSM.Current()
			},
		},
	)
}

// NewMigration creates a new Migration with the given ID, starting in the uninitialized state.
// This is a constructor function for Migration.
func NewMigration(migrationId string, opts MigrationOpts) *Migration {
	m := &Migration{
		MigrationId:         migrationId,
		CurrentState:        StateUninitialized,
		GatewayNamespace:    opts.GatewayNamespace,
		GatewayCrdName:      opts.GatewayCrdName,
		KubeConfigPath:      opts.KubeConfigPath,
		ClusterId:           opts.ClusterId,
		ClusterRestEndpoint: opts.ClusterRestEndpoint,
		ClusterLinkName:     opts.ClusterLinkName,
		Topics:              opts.Topics,
		AuthMode:            opts.AuthMode,
		ClusterApiKey:       opts.ClusterApiKey,
		ClusterApiSecret:    opts.ClusterApiSecret,
	}

	m.initializeFSM(StateUninitialized)

	return m
}

// LoadMigration loads a Migration object from a JSON file by its ID.
// This is a static constructor-like function for Migration that reconstructs
// a previously saved migration from disk with its state intact.
func LoadMigration(migrationId string) (*Migration, error) {
	filename := fmt.Sprintf("migration_%s.json", migrationId)

	// Read the JSON file
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read migration file: %w", err)
	}

	// Unmarshal into Migration struct
	var m Migration
	err = json.Unmarshal(data, &m)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal migration: %w", err)
	}

	// Initialize the FSM with the loaded current state
	m.initializeFSM(m.CurrentState)

	fmt.Printf("Migration loaded from %s (state: %s)\n", filename, m.CurrentState)

	return &m, nil
}

func (m *Migration) leaveInitialized(e *fsm.Event) {

	slog.Info("parsing gateway resource", "gatewayName", m.GatewayNamespace, "kubeConfigPath", m.KubeConfigPath)

	config, err := clientcmd.BuildConfigFromFlags("", m.KubeConfigPath)
	if err != nil {
		e.Cancel(fmt.Errorf("failed to build config: %v", err))
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		e.Cancel(fmt.Errorf("failed to create clientset: %v", err))
	}

	allowed, err := checkPermission(clientset, "update", "gateways", "platform.confluent.io", "confluent")
	if err != nil {
		e.Cancel(fmt.Errorf("permission check failed: %v", err))
	}

	if !allowed {
		e.Cancel(fmt.Errorf("you don't have permission to update gateway resources"))
	}

	// create dynamic client for custom resources
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		e.Cancel(fmt.Errorf("failed to create dynamic client: %v", err))
	}

	err = listPods(clientset, m.GatewayNamespace)
	if err != nil {
		e.Cancel(fmt.Errorf("failed to list pods: %v", err))
	}
	gatewayYAML, err := getGatewayAsYAML(dynamicClient, "confluent", m.GatewayCrdName)
	if err != nil {
		e.Cancel(fmt.Errorf("failed to get gateway as YAML: %v", err))
	}
	m.GatewayOriginalYAML = gatewayYAML

	slog.Info("describing cluster link", "clusterId", m.ClusterId, "clusterLinkName", m.ClusterLinkName)
	clusterLinkTopics, err := listMirrorTopics(m.ClusterRestEndpoint, m.ClusterId, m.ClusterLinkName, m.ClusterApiKey, m.ClusterApiSecret)
	if err != nil {
		e.Cancel(fmt.Errorf("failed to list mirror topics: %v", err))
	}

	if len(m.Topics) > 0 {
		slog.Info("validating topics in cluster link", "topic count", len(m.Topics))
		if err := validateTopicsInClusterLink(m.Topics, clusterLinkTopics); err != nil {
			e.Cancel(fmt.Errorf("failed to validate topics in cluster link: %v", err))
		}
	} else {
		m.Topics = clusterLinkTopics
	}

	clusterLinkConfigs, err := listClusterLinkConfigs(m.ClusterRestEndpoint, m.ClusterId, m.ClusterLinkName, m.ClusterApiKey, m.ClusterApiSecret)
	if err != nil {
		e.Cancel(fmt.Errorf("failed to list cluster link configs: %v", err))
	}
	m.ClusterLinkConfigs = clusterLinkConfigs
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

func listPods(clientset *kubernetes.Clientset, namespace string) error {
	fmt.Printf("📋 Pods in namespace '%s':\n\n", namespace)

	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods: %v", err)
	}

	if len(pods.Items) == 0 {
		fmt.Println("No pods found.")
		return nil
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
	return nil
}

func getGatewayAsYAML(dynamicClient dynamic.Interface, namespace, gatewayName string) ([]byte, error) {
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
		return nil, fmt.Errorf("failed to get Gateway: %v", err)
	}

	// Convert to YAML
	yamlBytes, err := yaml.Marshal(gateway.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal to YAML: %v", err)
	}

	// Print formatted YAML
	fmt.Println(string(yamlBytes))
	return yamlBytes, nil
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

// func printDeploymentYAML(clientset *kubernetes.Clientset, namespace, deployName string) error {
// 	// Get deployment from cluster
// 	deploy, err := clientset.AppsV1().Deployments(namespace).Get(context.TODO(), deployName, metav1.GetOptions{})
// 	if err != nil {
// 		return fmt.Errorf("failed to get deployment: %v", err)
// 	}

// 	// Convert to YAML
// 	yamlBytes, err := yaml.Marshal(deploy)
// 	if err != nil {
// 		return fmt.Errorf("failed to marshal to YAML: %v", err)
// 	}

// 	// Print formatted YAML
// 	fmt.Println(string(yamlBytes))
// 	return nil
// }

// func describeClusterLink(clusterRestEndpoint, clusterId, clusterLinkName, clusterApiKey, clusterApiSecret string) error {
// 	url := fmt.Sprintf("%s/kafka/v3/clusters/%s/links/%s/mirrors", clusterRestEndpoint, clusterId, clusterLinkName)
// 	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", clusterApiKey, clusterApiSecret)))

// 	req, _ := http.NewRequest("GET", url, nil)
// 	req.Header.Add("Authorization", "Basic "+auth)
// 	res, err := http.DefaultClient.Do(req)
// 	if err != nil {
// 		return fmt.Errorf("failed to describe cluster link: %v", err)
// 	}

// 	defer res.Body.Close()
// 	body, err := io.ReadAll(res.Body)
// 	if err != nil {
// 		return fmt.Errorf("failed to read response body: %v", err)
// 	}

// 	if res.StatusCode != http.StatusOK {
// 		return fmt.Errorf("failed to describe cluster link: %v", res.Status)
// 	}

// 	var response struct {
// 		LinkError        string `json:"link_error"`
// 		LinkErrorMessage string `json:"link_error_message"`
// 		LinkState        string `json:"link_state"`
// 	}

// 	if err := json.Unmarshal(body, &response); err != nil {
// 		return fmt.Errorf("failed to unmarshal cluster link response: %v", err)
// 	}

// 	if response.LinkState != "ACTIVE" || response.LinkError != "NO_ERROR" {
// 		return fmt.Errorf("there is a problem with the cluster link: link_state=%s, link_error=%s, link_error_message=%s", response.LinkState, response.LinkError, response.LinkErrorMessage)
// 	}

// 	return nil
// }
