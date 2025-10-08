package kafka

import (
	"encoding/json"
	"fmt"
	"log/slog"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

type KafkaService struct {
	client     client.KafkaAdmin
	authType   types.AuthType
	clusterArn string
}

type KafkaServiceOpts struct {
	AuthType   types.AuthType
	ClusterArn string
}

func NewKafkaService(kafkaAdmin client.KafkaAdmin, opts KafkaServiceOpts) *KafkaService {
	return &KafkaService{
		client:     kafkaAdmin,
		authType:   opts.AuthType,
		clusterArn: opts.ClusterArn,
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

	topics, err := ks.scanClusterTopics()
	if err != nil {
		return nil, err
	}
	kafkaAdminClientInformation.SetTopics(topics)

	// Serverless clusters do not support Kafka Admin API and instead returns an EOF error - this should be handled gracefully
	if clusterType == kafkatypes.ClusterTypeServerless {
		slog.Warn("⚠️ Serverless clusters do not support querying Kafka ACLs, skipping ACLs scan")
		return kafkaAdminClientInformation, nil
	}

	acls, err := ks.scanKafkaAcls()
	if err != nil {
		return nil, err
	}
	kafkaAdminClientInformation.Acls = acls

	// Check and read Kafka Connect topics
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
		return nil, fmt.Errorf("❌ Failed to list topics with configs: %v", err)
	}

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
		return nil, fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}
	return clusterMetadata, nil
}

// scanKafkaAcls scans for Kafka ACLs in the cluster
func (ks *KafkaService) scanKafkaAcls() ([]types.Acls, error) {
	slog.Info("🔍 scanning for kafka acls", "clusterArn", ks.clusterArn)

	acls, err := ks.client.ListAcls()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to list acls: %v", err)
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

// scanSelfManagedConnectors scans for self-managed connectors in the connect-configs topic
// and returns a list of connector configurations
func (ks *KafkaService) scanSelfManagedConnectors(topics []types.TopicDetails) ([]types.SelfManagedConnector, error) {
	topicName := "connect-configs"

	// Check if the topic exists
	existingTopics := make(map[string]bool)
	for _, topic := range topics {
		existingTopics[topic.Name] = true
	}

	if !existingTopics[topicName] {
		slog.Debug("⏭️ skipping topic (does not exist)", "topic", topicName, "clusterArn", ks.clusterArn)
		return []types.SelfManagedConnector{}, nil
	}

	slog.Info("🔍 found connect-configs topic, attempting to read all connector configurations", "topic", topicName, "clusterArn", ks.clusterArn)

	connectorConfigs, err := ks.client.GetAllMessagesWithKeyFilter(topicName, "connector-")
	if err != nil {
		return nil, fmt.Errorf("failed to read connector configurations: %w", err)
	}

	// Also try to get connector status information
	statusTopicName := "connect-status"
	var connectorStatuses map[string]string
	if existingTopics[statusTopicName] {
		slog.Info("🔍 found connect-status topic, attempting to read connector status information", "topic", statusTopicName, "clusterArn", ks.clusterArn)
		connectorStatuses, err = ks.client.GetConnectorStatusMessages(statusTopicName)
		if err != nil {
			slog.Warn("⚠️ failed to read connector status information", "topic", statusTopicName, "error", err)
			connectorStatuses = make(map[string]string) // Continue without status data
		} else {
			slog.Info("✅ successfully read connector status information", "topic", statusTopicName, "clusterArn", ks.clusterArn, "count", len(connectorStatuses))
		}
	} else {
		slog.Debug("⏭️ skipping topic (does not exist)", "topic", statusTopicName, "clusterArn", ks.clusterArn)
		connectorStatuses = make(map[string]string)
	}

	var connectors []types.SelfManagedConnector
	for connectorKey, configJSON := range connectorConfigs {
		// Parse the JSON config into a map
		var configMap map[string]interface{}
		if err := json.Unmarshal([]byte(configJSON), &configMap); err != nil {
			slog.Warn("⚠️ failed to parse connector config JSON", "connector", connectorKey, "error", err)
			continue
		}

		// Remove the "connector-" prefix from the key to get the actual connector name
		connectorName := connectorKey
		if len(connectorKey) > 10 && connectorKey[:10] == "connector-" {
			connectorName = connectorKey[10:]
		}

		// Create the connector with basic info
		connector := types.SelfManagedConnector{
			Name:   connectorName,
			Config: configMap,
		}

		// Try to add status information if available
		if statusJSON, exists := connectorStatuses[connectorName]; exists {
			var statusMap map[string]interface{}
			if err := json.Unmarshal([]byte(statusJSON), &statusMap); err != nil {
				slog.Warn("⚠️ failed to parse connector status JSON", "connector", connectorName, "error", err)
			} else {
				// Extract state and worker_id from status
				if state, ok := statusMap["state"].(string); ok {
					connector.State = state
				}
				if connectHost, ok := statusMap["worker_id"].(string); ok {
					connector.ConnectHost = connectHost
				}
			}
		} else {
			slog.Warn("⚠️ no status found for connector", "connector", connectorName, "available_statuses", len(connectorStatuses))
		}

		connectors = append(connectors, connector)
	}

	slog.Info("✅ successfully read connector configurations", "topic", topicName, "clusterArn", ks.clusterArn, "count", len(connectors))
	return connectors, nil
}
