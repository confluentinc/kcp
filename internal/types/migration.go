package types

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
	StateFenced        = "fenced"
	StatePromoted      = "promoted"
	StateSwitched      = "switched"
)

// FSM Event constants
const (
	EventInitialize        = "initialize"
	EventFence     = "fence"
	EventPromote   = "promote"
	EventSwitch    = "switch"
)

type MigrationOpts struct {
	StateFile string
	State State
	GatewayNamespace     string
	GatewayCrdName       string
	SourceName           string
	DestinationName      string
	SourceRouteName      string
	DestinationRouteName string
	KubeConfigPath       string
	ClusterId            string
	ClusterRestEndpoint  string
	ClusterLinkName      string
	Topics               []string
	AuthMode             string
	ClusterApiKey        string
	ClusterApiSecret     string
}

// Migration represents a gateway migration with a finite state machine

type Migration struct {
	StateFile string `json:"-"`
	State State `json:"-"`
	ClusterLinkTopics []string `json:"cluster_link_topics"`
	MigrationId  string   `json:"migration_id"`
	CurrentState string   `json:"current_state"`
	FSM          *fsm.FSM `json:"-"`

	GatewayNamespace     string `json:"gateway_namespace"`
	GatewayCrdName       string `json:"gateway_crd_name"`
	SourceName           string `json:"source_name"`
	DestinationName      string `json:"destination_name"`
	SourceRouteName      string `json:"source_route_name"`
	DestinationRouteName string `json:"destination_route_name"`
	KubeConfigPath       string `json:"kube_config_path"`

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
			{Name: EventInitialize, Src: []string{StateUninitialized}, Dst: StateInitialized},
			{Name: EventFence, Src: []string{StateInitialized}, Dst: StateFenced},
			{Name: EventPromote, Src: []string{StateFenced}, Dst: StatePromoted},
			{Name: EventSwitch, Src: []string{StatePromoted}, Dst: StateSwitched},
		},
		fsm.Callbacks{

		"before_event": func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Before event", "event", e.Event, "currentState", m.FSM.Current(), "nextState", e.Dst)
		},
		"leave_state": func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Left state", "state", m.FSM.Current(), "triggered by event", e.Event)
		},
		"enter_state": func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Entered state", "state", m.FSM.Current(), "triggered by event", e.Event)
		},
		"after_event": func(_ context.Context, e *fsm.Event) {
			m.CurrentState = m.FSM.Current()
			m.State.UpsertMigration(*m)
			err := m.State.PersistStateFile(m.StateFile)
			if err != nil {
				e.Cancel(fmt.Errorf("failed to persist state file: %v", err))
			}
			slog.Info("FSM: After event", "event", e.Event, "currentState", m.FSM.Current())
		},
		"enter_" + StateUninitialized: func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Entering state", "state", e.Dst, "triggered by event", e.Event)
			// noop
		},
		"leave_" + StateUninitialized: func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
			m.initializeMigration()
			clusterLinkConfigs, gatewayYAML, err := m.initializeMigration()
			if err != nil {
				e.Cancel(err)
			}
			m.GatewayOriginalYAML = gatewayYAML
			m.ClusterLinkConfigs = clusterLinkConfigs
		},
		"enter_" + StateInitialized: func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Entering state", "state", e.Dst, "triggered by event", e.Event)
			// noop
		},
		"leave_" + StateInitialized: func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
			err := m.checkLags()
			if err != nil {
				e.Cancel(err)
			}
		},
		"enter_" + StateFenced: func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Entering state", "state", e.Dst, "triggered by event", e.Event)
			err := m.fenceGateway()
			if err != nil {
				e.Cancel(err)
			}
		},
		"leave_" + StateFenced: func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
			err := m.startTopicPromotion()
			if err != nil {
				e.Cancel(err)
			}
		},
		"enter_" + StatePromoted: func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Entering state", "state", e.Dst, "triggered by event", e.Event)
			err := m.checkPromotionCompletion()
			if err != nil {
				e.Cancel(err)
			}
		},
		"leave_" + StatePromoted: func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
			// noop
		},
		"enter_" + StateSwitched: func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Entering state", "state", e.Dst, "triggered by event", e.Event)
			err := m.switchOverGatewayConfig()
			if err != nil {
				e.Cancel(err)
			}
		},
		"leave_" + StateSwitched: func(_ context.Context, e *fsm.Event) {
			slog.Info("FSM: Leaving state", "state", m.FSM.Current(), "triggered by event", e.Event)
			// noop
		},
		},
	)
}

// NewMigration creates a new Migration with the given ID, starting in the uninitialized state.
// This is a constructor function for Migration.
func NewMigration(migrationId string, opts MigrationOpts) *Migration {
	m := &Migration{
		StateFile:            opts.StateFile,
		State:                opts.State,
		MigrationId:          migrationId,
		CurrentState:         StateUninitialized,
		GatewayNamespace:     opts.GatewayNamespace,
		GatewayCrdName:       opts.GatewayCrdName,
		SourceName:           opts.SourceName,
		DestinationName:      opts.DestinationName,
		SourceRouteName:      opts.SourceRouteName,
		DestinationRouteName: opts.DestinationRouteName,
		KubeConfigPath:       opts.KubeConfigPath,
		ClusterId:            opts.ClusterId,
		ClusterRestEndpoint:  opts.ClusterRestEndpoint,
		ClusterLinkName:      opts.ClusterLinkName,
		Topics:               opts.Topics,
		AuthMode:             opts.AuthMode,
		ClusterApiKey:        opts.ClusterApiKey,
		ClusterApiSecret:     opts.ClusterApiSecret,
	}

	m.initializeFSM(StateUninitialized)

	return m
}

// LoadMigration loads a Migration object from a JSON file by its ID.
// This is a static constructor-like function for Migration that reconstructs
// a previously saved migration from disk with its state intact.
func LoadMigration(stateFile string, state State, migrationId string) (*Migration, error) {

	m, err := state.GetMigrationById(migrationId)
	m.StateFile = stateFile
	m.State = state
	if err != nil {
		return nil, fmt.Errorf("failed to get migration: %v", err)
	}

	// Initialize the FSM with the loaded current state
	m.initializeFSM(m.CurrentState)

	return m, nil
}

func (m *Migration) initializeMigration() (map[string]string, []byte, error) {

	slog.Info("parsing gateway resource", "gatewayName", m.GatewayNamespace, "kubeConfigPath", m.KubeConfigPath)

	config, err := clientcmd.BuildConfigFromFlags("", m.KubeConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build config: %v", err)
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create clientset: %v", err)
	}

	allowed, err := checkPermission(clientset, "update", "gateways", "platform.confluent.io", "confluent")
	if err != nil {
		return nil, nil, fmt.Errorf("permission check failed: %v", err)
	}

	if !allowed {
		return nil, nil, fmt.Errorf("you don't have permission to update gateway resources")
	}

	// create dynamic client for custom resources
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create dynamic client: %v", err)
	}

	// err = listPods(clientset, m.GatewayNamespace)
	// if err != nil {
	// 	return fmt.Errorf("failed to list pods: %v", err)
	// }

	gatewayYAML, err := getGatewayAsYAML(dynamicClient, "confluent", m.GatewayCrdName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get gateway as YAML: %v", err)
	}

	// Validate gateway YAML contains expected source, destination, and route
	if err := ValidateGatewayYAML(gatewayYAML, m.SourceName, m.DestinationName, m.SourceRouteName, m.DestinationRouteName, m.AuthMode); err != nil {
		return nil, nil, fmt.Errorf("gateway validation failed: %v", err)
	}

	slog.Info("describing cluster link", "clusterId", m.ClusterId, "clusterLinkName", m.ClusterLinkName)
	clusterLinkTopics, err := listMirrorTopics(m.ClusterRestEndpoint, m.ClusterId, m.ClusterLinkName, m.ClusterApiKey, m.ClusterApiSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list mirror topics: %v", err)
	}

	if len(m.Topics) > 0 {
		slog.Info("validating topics in cluster link", "topic count", len(m.Topics))
		if err := validateTopicsInClusterLink(m.Topics, clusterLinkTopics); err != nil {
			return nil, nil, fmt.Errorf("failed to validate topics in cluster link: %v", err)
		}
	} else {
		m.Topics = clusterLinkTopics
	}

	clusterLinkConfigs, err := listClusterLinkConfigs(m.ClusterRestEndpoint, m.ClusterId, m.ClusterLinkName, m.ClusterApiKey, m.ClusterApiSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list cluster link configs: %v", err)
	}

	return clusterLinkConfigs, gatewayYAML, nil
}

func (m *Migration) checkLags() error {	
	// check the lags are below threshold, if not, cancel the migration
	slog.Info("checking lags are below threshold.....")
	time.Sleep(10 * time.Second)
	slog.Info("checking lags are below threshold...done")
	
	return nil
}

func (m *Migration) fenceGateway() error {
	// fence the gateway
	slog.Info("fencing the gateway...done")
	return nil
}

func (m *Migration) startTopicPromotion() error {
	// start topic promotion process
	slog.Info("topic promotion process started")
	return nil
}

func (m *Migration) checkPromotionCompletion() error {
	//wait for topic promotion to complete
	slog.Info("waiting for topic promotion to complete.....")
	time.Sleep(10 * time.Second)
	slog.Info("waiting for topic promotion to complete...done")
	return nil
}

func (m *Migration) switchOverGatewayConfig() error {
	// switch over to the new gateway	
	slog.Info("switching over to the new gateway config...done")
	return nil
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

func ValidateGatewayYAML(gatewayYAML []byte, sourceName, destinationName, sourceRoute, destinationRoute, authMode string) error {
	var gateway GatewayResource
	if err := yaml.Unmarshal(gatewayYAML, &gateway); err != nil {
		return fmt.Errorf("failed to parse gateway YAML: %w", err)
	}

	streamingDomainNames := make([]string, len(gateway.Spec.StreamingDomains))
	for i, domain := range gateway.Spec.StreamingDomains {
		streamingDomainNames[i] = domain.Name
	}

	if !slices.Contains(streamingDomainNames, sourceName) {
		return fmt.Errorf("source streaming domain '%s' not found in gateway streamingDomains. Available domains: %v", sourceName, streamingDomainNames)
	}
	if !slices.Contains(streamingDomainNames, destinationName) {
		return fmt.Errorf("destination streaming domain '%s' not found in gateway streamingDomains. Available domains: %v", destinationName, streamingDomainNames)
	}

	routeNames := make([]string, len(gateway.Spec.Routes))
	for i, route := range gateway.Spec.Routes {
		routeNames[i] = route.Name

		// Validate source route
		if route.Name == sourceRoute {
			// Validate streaming domain matches source
			if route.StreamingDomain.Name != sourceName {
				return fmt.Errorf("source route '%s' streaming domain '%s' does not match expected source streaming domain '%s'", route.Name, route.StreamingDomain.Name, sourceName)
			}

			/*
				- If the `auth_mode` is passed as/defaulted to 'dest_swap', we would expect the user's route to be 'passthrough'.
					Then the future route would be 'swap' so that the clients do not need to update their credentials.
				- If the `auth_mode` is passed as 'source_swap', we would expect the user's route to be a 'swap'.
					Then the future route would be 'passthrough' as the clients already use the CC credentials.
			*/
			if authMode == "dest_swap" && route.Security.Auth != "passthrough" {
				return fmt.Errorf("source route '%s' expected to be 'passthrough', found '%s'", sourceRoute, route.Security.Auth)
			}
			if authMode == "source_swap" && route.Security.Auth != "swap" {
				return fmt.Errorf("source route '%s' expected to be 'swap', found '%s'", sourceRoute, route.Security.Auth)
			}
		}

		// Validate destination route
		if route.Name == destinationRoute {
			// Validate streaming domain matches destination
			if route.StreamingDomain.Name != destinationName {
				return fmt.Errorf("destination route '%s' streaming domain '%s' does not match expected destination streaming domain '%s'", destinationRoute, route.StreamingDomain.Name, destinationName)
			}

			// Validate client and cluster objects exist
			if route.Security.Client.Authentication.Type == "" {
				return fmt.Errorf("destination route '%s' is missing client authentication configuration", destinationRoute)
			}
			if route.Security.Cluster.Authentication.Type == "" {
				return fmt.Errorf("destination route '%s' is missing cluster authentication configuration", destinationRoute)
			}
		}
	}

	if !slices.Contains(routeNames, sourceRoute) {
		return fmt.Errorf("source route '%s' not found in gateway routes. Available routes: %v", sourceRoute, routeNames)
	}

	if !slices.Contains(routeNames, destinationRoute) {
		return fmt.Errorf("destination route '%s' not found in gateway routes. Available routes: %v", destinationRoute, routeNames)
	}

	slog.Info("gateway validation successful",
		"source", sourceName,
		"destination", destinationName,
		"route", sourceRoute,
	)

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
