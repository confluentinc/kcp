package kafka

import (
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

// KafkaService handles all Kafka-related operations for cluster scanning
type KafkaService struct {
	mskService        MSKService
	kafkaAdminFactory KafkaAdminFactory
	authType          types.AuthType
	clusterArn        string
}

// KafkaAdminFactory is a function type that creates a KafkaAdmin client
type KafkaAdminFactory func(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error)

// MSKService interface defines the MSK operations needed by the Kafka service
type MSKService interface {
	ParseBrokerAddresses(brokers kafka.GetBootstrapBrokersOutput, authType types.AuthType) ([]string, error)
}

// KafkaServiceOpts contains options for creating a new KafkaService
type KafkaServiceOpts struct {
	MSKService        MSKService
	KafkaAdminFactory KafkaAdminFactory
	AuthType          types.AuthType
	ClusterArn        string
}

// NewKafkaService creates a new KafkaService instance
func NewKafkaService(opts KafkaServiceOpts) *KafkaService {
	return &KafkaService{
		mskService:        opts.MSKService,
		kafkaAdminFactory: opts.KafkaAdminFactory,
		authType:          opts.AuthType,
		clusterArn:        opts.ClusterArn,
	}
}

// ScanKafkaResources scans all Kafka-related resources and populates the cluster information
func (ks *KafkaService) ScanKafkaResources(clusterInfo *types.ClusterInformation) error {
	brokerAddresses, err := ks.mskService.ParseBrokerAddresses(clusterInfo.BootstrapBrokers, ks.authType)
	if err != nil {
		return err
	}

	clientBrokerEncryptionInTransit := types.GetClientBrokerEncryptionInTransit(clusterInfo.Cluster)
	kafkaVersion := ks.GetKafkaVersion(clusterInfo)

	admin, err := ks.kafkaAdminFactory(brokerAddresses, clientBrokerEncryptionInTransit, kafkaVersion)
	if err != nil {
		return fmt.Errorf("❌ Failed to setup admin client: %v", err)
	}

	defer admin.Close()

	// Get cluster metadata including broker information and ClusterID
	clusterMetadata, err := ks.describeKafkaCluster(admin)
	if err != nil {
		return err
	}

	clusterInfo.ClusterID = clusterMetadata.ClusterID

	topics, err := ks.scanClusterTopics(admin)
	if err != nil {
		return err
	}
	clusterInfo.Topics = topics

	// Serverless clusters do not support Kafka Admin API and instead returns an EOF error - this should be handled gracefully
	if clusterInfo.Cluster.ClusterType == kafkatypes.ClusterTypeProvisioned {
		acls, err := ks.scanKafkaAcls(admin)
		if err != nil {
			return err
		}
		clusterInfo.Acls = acls
	} else {
		slog.Warn("⚠️ Serverless clusters do not support querying Kafka ACLs, skipping ACLs scan")
	}

	return nil
}

// scanClusterTopics scans for topics in the Kafka cluster
func (ks *KafkaService) scanClusterTopics(admin client.KafkaAdmin) ([]string, error) {
	slog.Info("🔍 scanning for cluster topics", "clusterArn", ks.clusterArn)

	topics, err := admin.ListTopics()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to list topics: %v", err)
	}

	topicList := make([]string, 0, len(topics))
	for topic := range topics {
		topicList = append(topicList, topic)
	}

	return topicList, nil
}

// describeKafkaCluster gets cluster metadata and returns the cluster ID along with logging information
func (ks *KafkaService) describeKafkaCluster(admin client.KafkaAdmin) (*client.ClusterKafkaMetadata, error) {
	slog.Info("🔍 describing kafka cluster", "clusterArn", ks.clusterArn)

	clusterMetadata, err := admin.GetClusterKafkaMetadata()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to describe kafka cluster: %v", err)
	}
	return clusterMetadata, nil
}

// scanKafkaAcls scans for Kafka ACLs in the cluster
func (ks *KafkaService) scanKafkaAcls(admin client.KafkaAdmin) ([]types.Acls, error) {
	slog.Info("🔍 scanning for kafka acls", "clusterArn", ks.clusterArn)

	acls, err := admin.ListAcls()
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

// CreateKafkaAdmin creates a Kafka admin client directly (for direct broker connections)
func (ks *KafkaService) CreateKafkaAdmin(brokerAddresses []string, clientBrokerEncryptionInTransit kafkatypes.ClientBroker, kafkaVersion string) (client.KafkaAdmin, error) {
	return ks.kafkaAdminFactory(brokerAddresses, clientBrokerEncryptionInTransit, kafkaVersion)
}

// getKafkaVersion determines the Kafka version based on cluster type
func (ks *KafkaService) GetKafkaVersion(clusterInfo *types.ClusterInformation) string {
	switch clusterInfo.Cluster.ClusterType {
	case kafkatypes.ClusterTypeProvisioned:
		return utils.ConvertKafkaVersion(clusterInfo.Cluster.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion)
	case kafkatypes.ClusterTypeServerless:
		slog.Warn("⚠️ Serverless clusters do not return a Kafka version, defaulting to 4.0.0")
		return "4.0.0"
	default:
		slog.Warn(fmt.Sprintf("⚠️ Unknown cluster type: %v, defaulting to 4.0.0", clusterInfo.Cluster.ClusterType))
		return "4.0.0"
	}
}
