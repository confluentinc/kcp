package kafka

import (
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
