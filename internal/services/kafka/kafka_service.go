package kafka

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

type KafkaService struct {
	client     client.KafkaAdmin
	authType   types.AuthType
	clusterArn string
	skipTopics bool
	skipACLs   bool
}

type KafkaServiceOpts struct {
	AuthType   types.AuthType
	ClusterArn string
	SkipTopics bool
	SkipACLs   bool
}

func NewKafkaService(kafkaAdmin client.KafkaAdmin, opts KafkaServiceOpts) *KafkaService {
	return &KafkaService{
		client:     kafkaAdmin,
		authType:   opts.AuthType,
		clusterArn: opts.ClusterArn,
		skipTopics: opts.SkipTopics,
		skipACLs:   opts.SkipACLs,
	}
}

// ScanKafkaResources scans all Kafka-related resources and populates the cluster information
func (ks *KafkaService) ScanKafkaResources(clusterType kafkatypes.ClusterType) (*types.KafkaAdminClientInformation, error) {
	kafkaAdminClientInformation := &types.KafkaAdminClientInformation{}
	// Get cluster metadata including broker information and ClusterID
	clusterMetadata, err := ks.describeKafkaCluster()
	if err != nil {
		return nil, err
	}

	kafkaAdminClientInformation.ClusterID = clusterMetadata.ClusterID

	// Store discovered broker addresses
	brokerAddrs := make([]string, 0, len(clusterMetadata.Brokers))
	for _, broker := range clusterMetadata.Brokers {
		brokerAddrs = append(brokerAddrs, broker.Addr())
	}
	kafkaAdminClientInformation.DiscoveredBrokers = brokerAddrs

	var topics []types.TopicDetails
	if !ks.skipTopics {
		topics, err = ks.scanClusterTopics()
		if err != nil {
			return nil, err
		}
		kafkaAdminClientInformation.SetTopics(topics)
	}

	// Serverless clusters do not support Kafka Admin API and instead returns an EOF error - this should be handled gracefully
	if clusterType == kafkatypes.ClusterTypeServerless {
		slog.Warn("⚠️ Serverless clusters do not support querying Kafka ACLs, skipping ACLs scan")
		return kafkaAdminClientInformation, nil
	}

	if !ks.skipACLs {
		acls, err := ks.scanKafkaAcls()
		if err != nil {
			return nil, err
		}
		kafkaAdminClientInformation.Acls = acls
	}

	connectors, err := ks.scanSelfManagedConnectors(topics)
	if err != nil {
		slog.Warn("⚠️ failed to scan self-managed connectors", "clusterArn", ks.clusterArn, "error", err)
	} else {
		kafkaAdminClientInformation.SetSelfManagedConnectors(connectors)
	}

	return kafkaAdminClientInformation, nil
}

// scanClusterTopics scans for topics in the Kafka cluster
func (ks *KafkaService) scanClusterTopics() ([]types.TopicDetails, error) {
	slog.Info("🔍 scanning for cluster topics", "clusterArn", ks.clusterArn)

	topics, err := ks.client.ListTopicsWithConfigs()
	if err != nil {
		return nil, fmt.Errorf("failed to list topics with configs: %v", err)
	}

	slog.Info("🔍 found topics", "count", len(topics))

	var topicDetails []types.TopicDetails
	for topicName, topic := range topics {
		configurations := make(map[string]*string)
		for key, valuePtr := range topic.ConfigEntries {
			if valuePtr != nil {
				configurations[key] = valuePtr
			}
		}

		topicDetails = append(topicDetails, types.TopicDetails{
			Name:              topicName,
			Partitions:        int(topic.NumPartitions),
			ReplicationFactor: int(topic.ReplicationFactor),
			Configurations:    configurations,
		})
	}

	return topicDetails, nil
}

// describeKafkaCluster gets cluster metadata and returns the cluster ID along with logging information
func (ks *KafkaService) describeKafkaCluster() (*client.ClusterKafkaMetadata, error) {
	slog.Info("🔍 describing kafka cluster", "clusterArn", ks.clusterArn)

	clusterMetadata, err := ks.client.GetClusterKafkaMetadata()
	if err != nil {
		return nil, fmt.Errorf("failed to describe kafka cluster: %v", err)
	}
	return clusterMetadata, nil
}

// scanKafkaAcls scans for Kafka ACLs in the cluster
func (ks *KafkaService) scanKafkaAcls() ([]types.Acls, error) {
	slog.Info("🔍 scanning for kafka acls", "clusterArn", ks.clusterArn)

	acls, err := ks.client.ListAcls()
	if err != nil {
		return nil, fmt.Errorf("failed to list acls: %v", err)
	}

	// Flatten the ACLs for easier processing
	var flattenedAcls []types.Acls
	for _, resourceAcl := range acls {
		for _, acl := range resourceAcl.Acls {
			flattenedAcl := types.Acls{
				ResourceType:        resourceAcl.ResourceType.String(),
				ResourceName:        resourceAcl.ResourceName,
				ResourcePatternType: resourceAcl.ResourcePatternType.String(),
				Principal:           acl.Principal,
				Host:                acl.Host,
				Operation:           acl.Operation.String(),
				PermissionType:      acl.PermissionType.String(),
			}
			flattenedAcls = append(flattenedAcls, flattenedAcl)
		}
	}

	return flattenedAcls, nil
}

func (ks *KafkaService) scanSelfManagedConnectors(topics []types.TopicDetails) ([]types.SelfManagedConnector, error) {
	const (
		configTopicNameSuffix = "connect-configs"
		statusTopicNameSuffix = "connect-status"
		keyPrefix             = "connector-"
	)

	var configTopics, statusTopics []string
	for _, topic := range topics {
		if strings.HasSuffix(topic.Name, configTopicNameSuffix) {
			configTopics = append(configTopics, topic.Name)
		}
		if strings.HasSuffix(topic.Name, statusTopicNameSuffix) {
			statusTopics = append(statusTopics, topic.Name)
		}
	}

	if len(configTopics) == 0 {
		slog.Debug("⏭️ no topics ending with suffix found", "suffix", configTopicNameSuffix, "clusterArn", ks.clusterArn)
		return []types.SelfManagedConnector{}, nil
	}

	connectorConfigs := make(map[string]string)
	for _, configTopic := range configTopics {
		slog.Info("🔍 found connect-configs topic, attempting to read all connector configurations", "topic", configTopic, "clusterArn", ks.clusterArn)
		configs, err := ks.client.GetAllMessagesWithKeyFilter(configTopic, keyPrefix)
		if err != nil {
			return nil, fmt.Errorf("failed to read connector configurations from %s: %w", configTopic, err)
		}
		for k, v := range configs {
			connectorConfigs[k] = v
		}
	}

	connectorStatuses := make(map[string]string)
	for _, statusTopic := range statusTopics {
		slog.Info("🔍 found connect-status topic, attempting to read connector status information", "topic", statusTopic, "clusterArn", ks.clusterArn)
		statuses, err := ks.client.GetConnectorStatusMessages(statusTopic)
		if err != nil {
			slog.Warn("⚠️ failed to read connector status information", "topic", statusTopic, "error", err)
			continue
		}
		slog.Info("✅ successfully read connector status information", "topic", statusTopic, "count", len(statuses))
		for k, v := range statuses {
			connectorStatuses[k] = v
		}
	}

	connectors := []types.SelfManagedConnector{}
	for connectorKey, configJSON := range connectorConfigs {
		var rawConfig map[string]any
		if err := json.Unmarshal([]byte(configJSON), &rawConfig); err != nil {
			slog.Warn("⚠️ failed to parse connector config JSON", "connector", connectorKey, "error", err)
			continue
		}

		var configMap map[string]any
		if properties, ok := rawConfig["properties"].(map[string]any); ok {
			configMap = properties
		}

		connectorName := connectorKey
		if len(connectorKey) > len(keyPrefix) && connectorKey[:len(keyPrefix)] == keyPrefix {
			connectorName = connectorKey[len(keyPrefix):]
		}

		connector := types.SelfManagedConnector{
			Name:   connectorName,
			Config: configMap,
		}

		if statusJSON, exists := connectorStatuses[connectorName]; exists {
			var statusMap map[string]any
			if err := json.Unmarshal([]byte(statusJSON), &statusMap); err != nil {
				slog.Warn("⚠️ failed to parse connector status JSON", "connector", connectorName, "error", err)
			} else {
				if state, ok := statusMap["state"].(string); ok {
					connector.State = state
				}
				if connectHost, ok := statusMap["worker_id"].(string); ok {
					connector.ConnectHost = connectHost
				}
			}
		}

		connectors = append(connectors, connector)
	}

	slog.Info("✅ successfully read connector configurations", "configTopics", configTopics, "clusterArn", ks.clusterArn, "count", len(connectors))
	return connectors, nil
}
